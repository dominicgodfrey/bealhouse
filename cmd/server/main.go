// Command server is the single Beal House binary: JSON API, embedded SPA, and
// (from build-order step 4) the background job runner.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bealhouse/internal/admin"
	"bealhouse/internal/booking"
	"bealhouse/internal/config"
	"bealhouse/internal/console"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
	"bealhouse/internal/gateway"
	"bealhouse/internal/httpx"
	"bealhouse/internal/jobs"
	"bealhouse/internal/media"
	"bealhouse/internal/payments"
	"bealhouse/internal/rates"
	"bealhouse/web"
)

func main() {
	// Two subcommands, and both exist because a deploy is one file on a server
	// with no repository beside it: `enroll` because the admin console has no
	// other way to admit its first phone, and `migrate` because the schema this
	// binary expects has to be applied by something, and the binary itself is
	// the one thing guaranteed to be the right version. Everything else is the
	// server.
	if len(os.Args) > 1 {
		var err error
		switch os.Args[1] {
		case "enroll":
			err = enroll(os.Args[2:])
		case "migrate":
			err = migrate(os.Args[2:])
		default:
			err = fmt.Errorf("unknown command %q; want enroll or migrate", os.Args[1])
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	setupLogging(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := connectDB(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	if pool != nil {
		defer pool.Close()
	}

	if !web.Built() {
		slog.Warn("SPA bundle is empty; non-API routes will 503 until `make web` runs")
	}

	// Chosen before anything is wired, because the background jobs need it as
	// much as the HTTP surface does. It refuses rather than guessing when the
	// settings contradict each other — notably STRIPE_FAKE anywhere it has no
	// business being — so a server that cannot say for certain which processor
	// it has does not start.
	processor, webhookSecret, err := gateway.New(cfg)
	if err != nil {
		return err
	}
	if !cfg.StripeConfigured() && !cfg.StripeFake {
		slog.Warn("Stripe is not configured; rooms can be held but not paid for")
	}

	// Likewise chosen up front. The queue accepts and drains messages either
	// way — what changes is whether draining one puts it in front of a guest or
	// in the log. Said plainly rather than left to be discovered: an inn that
	// believes it is emailing guests and is not finds out from a guest.
	sender := emailSender(cfg)

	// The manage-booking capability (decision #19). Nil without a secret, which
	// leaves confirmations without the link and its endpoints refusing — said
	// out loud, because a guest with no way to cancel is one who telephones.
	links := booking.NewLinks(cfg.BookingLinkSecret)
	if links == nil {
		slog.Warn("BOOKING_LINK_SECRET is unset; confirmation emails will carry " +
			"no manage-booking link and self-service cancellation is off")
	}

	// The owner's console (decision #15). Nil when there is no origin to bind a
	// passkey to, which leaves its endpoints answering 503: a WebAuthn
	// assertion is verified against an origin, and a server that had to guess
	// which one would be accepting assertions minted for somewhere else.
	adminAuth, err := adminConsole(cfg, pool)
	if err != nil {
		return err
	}

	// The renderer, hoisted above the job wiring because two things need it now:
	// the email.send handler that puts a message in front of a guest, and the
	// console's copy editor, which reads the words currently in force and has to
	// see exactly what the sender would.
	//
	// It reads the owner's overrides from the database on every render, so a
	// save in the console applies to the next message rather than the next
	// deploy — which is only true if these two share one renderer rather than
	// each building its own view of the templates.
	var mail *email.Renderer
	if pool != nil {
		mail, err = email.New(email.Brand{
			LogoURL: cfg.EmailLogoURL,
			SiteURL: cfg.SiteURL,
		}, db.New(pool))
		if err != nil {
			return err
		}
	}

	// Everything the owner's console does once somebody is signed in, and the
	// source of the marketing site's owner-managed content. Nil without a
	// database, which leaves both answering 503 rather than 500.
	var ops *console.Ops
	if pool != nil {
		// The same two things httpx attaches to a Charge, for the same reason:
		// a booking the owner takes on the phone earns the guest the same
		// confirmation — with the same manage link — as one taken on the
		// website. Both are allowed to be absent and both fail softly.
		letterhead := console.Letterhead{
			OwnerEmail: cfg.OwnerEmail,
			SiteURL:    cfg.SiteURL,
		}
		if links != nil && cfg.SiteURL != "" {
			letterhead.ManageURL = func(code string, expires time.Time) string {
				return links.URL(cfg.SiteURL, code, expires)
			}
		}
		_, isFake := processor.(*gateway.Fake)
		ops = console.New(pool, mail, letterhead, console.Processor{
			Gateway:        processor,
			PublishableKey: cfg.StripePublishableKey,
			Fake:           isFake,
		})
	}

	// Where uploaded photographs live (decision #16).
	//
	// A failure here does not stop the binary: the rest of the site works
	// without it and the routes behind it answer 503 saying so. It is logged
	// loudly, because the symptom otherwise is an owner pressing an upload
	// button that never works and no reason anywhere.
	store, err := media.New(cfg.MediaDir)
	if err != nil {
		slog.Error("photograph storage is unavailable; uploads will be refused",
			"dir", cfg.MediaDir, "err", err)
		store = nil
	}

	// Background work. Without the sweep an abandoned checkout holds its room
	// forever, which is worse than the double-booking the hold exists to
	// prevent.
	if pool != nil {
		q := db.New(pool)

		runner := jobs.New(q)
		runner.Every(booking.SweepJobKind, booking.SweepInterval, booking.SweepJob(q))

		// The departure-morning note. It needs the pool for the same reason the
		// warning does: each message has to be queued and marked sent in one
		// transaction, or a guest hears from the inn every quarter of an hour.
		runner.Every(booking.CheckoutJobKind, booking.CheckoutInterval,
			booking.CheckoutJob(q, pool))

		// Decision #6's T-8 heads-up. It needs the pool rather than the shared
		// queries handle because each warning has to queue its email and mark
		// the booking warned in one transaction.
		runner.Every(payments.WarnJobKind, payments.WarnInterval, payments.WarnJob(q, pool))

		// And the charge itself. With no processor configured this runs, finds
		// whatever is due, and fails every one of them loudly — which is the
		// honest behaviour: money that should have been collected was not.
		runner.Every(payments.ChargeJobKind, payments.ChargeInterval,
			payments.ChargeJob(q, pool, processor))

		// Money going back for a stay the inn could not honour (decision #24).
		// Queued by RecordCharge inside the transaction that cancelled the
		// booking, so a cancelled stay with no refund behind it is not a state
		// this system can reach — and the queue's retry is what carries it
		// through a Stripe outage.
		runner.Handle(payments.RefundJobKind, payments.RefundJob(pool, processor))

		// Rolls the nightly calendar's far edge forward. Without it the horizon
		// creeps closer every month until a guest planning next autumn finds no
		// price and the room quietly stops appearing in the search.
		runner.Every(rates.RebuildJobKind, rates.RebuildInterval, rates.RebuildJob(q))

		// Email is queued, never sent inline, so a provider outage delays a
		// confirmation rather than failing the booking that earned it.
		//
		// The renderer above is where the owner's edited copy is read from: a
		// message with a row in email_templates renders from that instead of the
		// blank file it ships with. Nothing is cached, so an edit saved in the
		// console applies to the next message rather than the next deploy.
		runner.Handle(email.JobKind, mail.Handler(sender))

		go runner.Run(ctx)
	}

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpx.NewRouter(httpx.Deps{
			Pool:                 pool,
			SPA:                  web.Dist(),
			BehindProxy:          cfg.BehindProxy,
			Gateway:              processor,
			StripePublishableKey: cfg.StripePublishableKey,
			StripeWebhookSecret:  webhookSecret,
			OwnerEmail:           cfg.OwnerEmail,
			Links:                links,
			SiteURL:              cfg.SiteURL,
			Console:              adminAuth,
			Ops:                  ops,
			Media:                store,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// adminConsole builds admin authentication, or explains why there is none.
//
// Three ways to get nothing back, and only one of them is an error:
//
//   - No database. The console's whole state — accounts, passkeys, sessions —
//     is rows, so there is nothing to authenticate against. The binary still
//     boots, the same way it does for frontend work.
//   - No SITE_URL outside dev. There is no origin to verify a passkey against
//     and guessing one would mean accepting assertions minted elsewhere, so the
//     console stays shut and says so.
//   - A SITE_URL that is not an origin. That is a typo in a deploy, and
//     starting anyway would leave a console nobody can open with no clue why.
func adminConsole(cfg config.Config, pool *pgxpool.Pool) (*admin.Console, error) {
	if pool == nil {
		slog.Warn("no database; the admin console is off")
		return nil, nil
	}

	rp, err := admin.NewRP(cfg.SiteURL, cfg.IsDev())
	if errors.Is(err, admin.ErrNotConfigured) {
		slog.Error("SITE_URL is unset, so the admin console cannot verify a passkey and is off")
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	console, err := admin.New(rp, db.New(pool), pool)
	if err != nil {
		return nil, err
	}

	slog.Info("admin console enabled", "rp_id", rp.ID, "origins", rp.Origins)
	return console, nil
}

// emailSender picks the provider, or the one that only pretends to be one.
//
// Half a configuration is called out at error level and then treated as none.
// It does not refuse to start, which is where this parts company with
// gateway.New: the whole reason mail is queued rather than sent inline is that
// an email problem must never stop the inn taking bookings, and a binary that
// will not boot over a mistyped from address is that failure in its largest
// form. So it says so as loudly as a log can and serves guests.
func emailSender(cfg config.Config) email.Sender {
	if cfg.EmailConfigured() {
		slog.Info("email provider configured", "from", cfg.EmailFrom)
		return email.NewResend(cfg.ResendAPIKey, cfg.EmailFrom)
	}

	if cfg.ResendAPIKey != "" || cfg.EmailFrom != "" {
		slog.Error("email is half configured; RESEND_API_KEY and EMAIL_FROM are required together")
	}

	slog.Warn("no email provider configured; queued messages will be logged, not sent")
	return email.LogSender{}
}

// connectDB returns a nil pool when no DATABASE_URL is set, so the binary boots
// for frontend work without Postgres. A configured-but-unreachable database is
// a real error and refuses to start.
func connectDB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		slog.Warn("DATABASE_URL is unset; starting without a database")
		return nil, nil
	}

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}

	slog.Info("database connected")
	return pool, nil
}

func setupLogging(cfg config.Config) {
	level := slog.LevelInfo
	if cfg.IsDev() {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}
