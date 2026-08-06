// Command server is the single Beal House binary: JSON API, embedded SPA, and
// (from build-order step 4) the background job runner.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bealhouse/internal/booking"
	"bealhouse/internal/config"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
	"bealhouse/internal/gateway"
	"bealhouse/internal/httpx"
	"bealhouse/internal/jobs"
	"bealhouse/internal/payments"
	"bealhouse/internal/rates"
	"bealhouse/web"
)

func main() {
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

	// Background work. Without the sweep an abandoned checkout holds its room
	// forever, which is worse than the double-booking the hold exists to
	// prevent.
	if pool != nil {
		q := db.New(pool)

		runner := jobs.New(q)
		runner.Every(booking.SweepJobKind, booking.SweepInterval, booking.SweepJob(q))

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
		mail, err := email.New(email.Brand{
			LogoURL: cfg.EmailLogoURL,
			SiteURL: cfg.SiteURL,
		})
		if err != nil {
			return err
		}
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
