package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/api"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/auth"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/kube"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := kube.NewClient(cfg)
	if err != nil {
		slog.Error("create Kubernetes client", "error", err)
		os.Exit(1)
	}

	service := api.NewService(cfg, client, auth.NewJupyterHub(cfg.JupyterHubUserURL, cfg.AuthTimeout))
	go service.Run(ctx)

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       65 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	slog.Info("mediator listening", "address", cfg.ListenAddress)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve", "error", err)
		os.Exit(1)
	}
}
