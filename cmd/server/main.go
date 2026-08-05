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
	"bealhouse/internal/httpx"
	"bealhouse/internal/jobs"
	"bealhouse/internal/payments"
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

		// Email is queued, never sent inline, so a provider outage delays a
		// confirmation rather than failing the booking that earned it.
		mail, err := email.New(email.Brand{
			LogoURL: cfg.EmailLogoURL,
			SiteURL: cfg.SiteURL,
		})
		if err != nil {
			return err
		}
		runner.Handle(email.JobKind, mail.Handler(email.LogSender{}))

		go runner.Run(ctx)
	}

	if !cfg.StripeConfigured() {
		slog.Warn("Stripe is not configured; rooms can be held but not paid for")
	}
	// Said plainly rather than left to be discovered: the queue will accept and
	// drain messages, and every one of them will be logged instead of delivered.
	slog.Warn("no email provider configured; queued messages will be logged, not sent")

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpx.NewRouter(httpx.Deps{
			Pool:        pool,
			SPA:         web.Dist(),
			BehindProxy: cfg.BehindProxy,
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
