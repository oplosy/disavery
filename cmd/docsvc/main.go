// Command docsvc runs the document service protected by disavery.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/oplosy/disavery/internal/docsvc/api"
	"github.com/oplosy/disavery/internal/docsvc/blob"
	"github.com/oplosy/disavery/internal/docsvc/config"
	"github.com/oplosy/disavery/internal/docsvc/store"
	"github.com/oplosy/disavery/internal/docsvc/webhook"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("docsvc exited", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	// Bounded so an unreachable database fails fast and systemd restarts us.
	migrateCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := st.Migrate(migrateCtx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	bl, err := blob.New(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3Bucket, cfg.S3UseTLS)
	if err != nil {
		return err
	}
	srv := &api.Server{
		Store: st,
		Blob:  bl,
		Notifier: &webhook.Notifier{
			URL:         cfg.PaymentWebhookURL,
			CallbackURL: strings.TrimRight(cfg.PublicBaseURL, "/") + "/callbacks/payment",
			Client:      &http.Client{Timeout: 5 * time.Second},
		},
		WebhookSecret:  []byte(cfg.WebhookSecret),
		MaxUploadBytes: cfg.MaxUploadBytes,
		Log:            log,
	}
	httpSrv := &http.Server{Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	log.Info("docsvc listening", "addr", cfg.Listen)

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	return httpSrv.Shutdown(shutdownCtx)
}
