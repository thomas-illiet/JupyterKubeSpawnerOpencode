// Package app assembles and runs the mediator process.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/api"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/auth"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/kube"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/version"
)

// Run starts the reconciler and HTTP server and supervises their shutdown.
func Run(parent context.Context, cfg config.Config) error {
	configureLogger(cfg)
	ctx, stopSignals := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(ctx)
	defer stopSignals()
	defer cancel()

	client, err := kube.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	service := api.NewService(cfg, client, auth.NewJupyterHub(cfg.JupyterHubUserURL, cfg.AuthTimeout))
	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       65 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		service.Run(ctx)
	}()

	serverErrors := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErrors <- err
	}()

	buildVersion, commit, date := version.Info()
	slog.Info("mediator listening", "address", cfg.ListenAddress, "version", buildVersion, "commit", commit, "build_date", date)
	select {
	case err := <-serverErrors:
		cancel()
		workers.Wait()
		if err != nil {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful HTTP shutdown failed", "error", err)
		if closeErr := server.Close(); closeErr != nil {
			slog.Error("force HTTP shutdown failed", "error", closeErr)
		}
	}
	cancel()
	workers.Wait()
	if err := <-serverErrors; err != nil {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

// configureLogger installs a process-wide structured logger without request
// bodies, credentials, or query parameters.
func configureLogger(cfg config.Config) {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewJSONHandler(os.Stdout, options)
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(os.Stdout, options)
	}
	slog.SetDefault(slog.New(handler).With("service", "jupyter-kubeopencode-mediator"))
}
