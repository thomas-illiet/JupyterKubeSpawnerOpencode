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

	"github.com/spf13/cobra"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/api"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/auth"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/kube"
)

// main executes the mediator command and reports startup failures.
func main() {
	if err := newRootCommand().Execute(); err != nil {
		slog.Error("mediator stopped", "error", err)
		os.Exit(1)
	}
}

// newRootCommand constructs the Cobra command and binds its flags to Viper.
func newRootCommand() *cobra.Command {
	v := config.NewViper()
	command := &cobra.Command{
		Use:           "jupyter-kubeopencode-mediator",
		Short:         "Mediate authenticated Jupyter workspace Agent operations",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	flags := command.Flags()
	flags.String("listen-address", ":8080", "HTTP listen address")
	flags.String("workspaces-file", "/etc/kubeopencode-mediator/workspaces.json", "administrator-owned workspace configuration")
	flags.String("jupyterhub-user-url", "http://hub:8081/hub/api/user", "JupyterHub user introspection URL")
	flags.String("kubeopencode-server-url", "http://kubeopencode-server.kubeopencode-system.svc.cluster.local:2746", "KubeOpenCode server URL")
	flags.Duration("auth-timeout", 5*time.Second, "JupyterHub authentication timeout")
	flags.Duration("reconcile-interval", 2*time.Second, "task reconciliation interval")
	flags.Duration("task-timeout", time.Hour, "maximum task duration")
	flags.String("snapshot-image", "alpine:3.22", "snapshot helper image")
	flags.String("agent-cpu-request", "250m", "Agent CPU request")
	flags.String("agent-cpu-limit", "2", "Agent CPU limit")
	flags.String("agent-memory-request", "512Mi", "Agent memory request")
	flags.String("agent-memory-limit", "4Gi", "Agent memory limit")
	flags.String("agent-ephemeral-storage-limit", "4Gi", "Agent ephemeral storage limit")
	flags.String("cleanup-token-file", "", "file containing the administrative cleanup token")
	bindErr := v.BindPFlags(flags)
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		if bindErr != nil {
			return fmt.Errorf("bind command flags: %w", bindErr)
		}
		cfg, err := config.FromViper(v)
		if err != nil {
			return err
		}
		return run(cmd.Context(), cfg)
	}
	return command
}

// run starts the reconciliation loop and HTTP server until shutdown.
func run(parent context.Context, cfg config.Config) error {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := kube.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	service := api.NewService(cfg, client, auth.NewJupyterHub(cfg.JupyterHubUserURL, cfg.AuthTimeout))
	go service.Run(ctx)

	server := &http.Server{Addr: cfg.ListenAddress, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 65 * time.Second}
	go shutdownServer(ctx, server)
	slog.Info("mediator listening", "address", cfg.ListenAddress)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// shutdownServer gracefully stops the HTTP server after context cancellation.
func shutdownServer(ctx context.Context, server *http.Server) {
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}
