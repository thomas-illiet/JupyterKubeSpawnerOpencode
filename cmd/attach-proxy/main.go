package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// main executes the local attachment proxy command.
func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newRootCommand constructs the Cobra command and its environment-backed configuration.
func newRootCommand() *cobra.Command {
	v := viper.New()
	v.AutomaticEnv()
	command := &cobra.Command{
		Use:           "jko-attach-proxy <mediator-attach-url>",
		Short:         "Attach OpenCode through an authenticated loopback proxy",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), args[0], v.GetString("JUPYTERHUB_API_TOKEN"))
		},
	}
	return command
}

// run validates configuration, serves the loopback proxy, and launches OpenCode.
func run(parent context.Context, rawTarget, token string) error {
	if token == "" {
		return errors.New("JUPYTERHUB_API_TOKEN is required")
	}
	target, err := url.Parse(rawTarget)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return errors.New("invalid mediator attach URL")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return errors.New("cannot allocate the local attachment port")
	}
	proxy := newProxy(target, token)
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 5 * time.Second}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	command := exec.CommandContext(ctx, "/usr/local/libexec/opencode-real", "attach", "http://"+listener.Addr().String())
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "JUPYTERHUB_API_TOKEN=") {
			command.Env = append(command.Env, value)
		}
	}
	if err := command.Run(); err != nil && ctx.Err() == nil {
		_ = server.Close()
		return errors.New("OpenCode attachment ended with an error")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	select {
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return errors.New("local attachment proxy failed")
		}
	default:
	}
	return nil
}

// newProxy creates a reverse proxy that injects authentication only toward the mediator.
func newProxy(target *url.URL, token string) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalPath := req.URL.Path
		originalDirector(req)
		req.URL.Path = target.Path + originalPath
		req.Host = target.Host
		req.Header.Set("Authorization", "Bearer "+token)
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "agent attachment unavailable", http.StatusBadGateway)
	}
	return proxy
}
