// Command webhookmock runs the fake payment provider.
package main

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oplosy/disavery/internal/webhookmock"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("webhookmock exited", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cidrs, err := webhookmock.ParseCIDRs(os.Getenv("WEBHOOKMOCK_ALLOW_CIDRS"))
	if err != nil {
		return errors.Join(errors.New("WEBHOOKMOCK_ALLOW_CIDRS"), err)
	}
	secret := os.Getenv("WEBHOOKMOCK_SECRET")
	if secret == "" {
		return errors.New("WEBHOOKMOCK_SECRET is required")
	}
	delay, err := time.ParseDuration(cmp.Or(os.Getenv("WEBHOOKMOCK_DELAY"), "1s"))
	if err != nil {
		return errors.Join(errors.New("WEBHOOKMOCK_DELAY"), err)
	}
	listen := cmp.Or(os.Getenv("WEBHOOKMOCK_LISTEN"), ":8081")

	srv := webhookmock.New(webhookmock.Config{AllowCIDRs: cidrs, Secret: []byte(secret), Delay: delay, Log: log})
	httpSrv := &http.Server{Addr: listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()
	log.Info("webhookmock listening", "addr", listen, "allow", cidrs)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = httpSrv.Shutdown(shutdownCtx)
	srv.Wait()
	return err
}
