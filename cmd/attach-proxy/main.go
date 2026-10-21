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
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: jko-attach-proxy <mediator-attach-url>")
		os.Exit(2)
	}
	token := os.Getenv("JUPYTERHUB_API_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "JUPYTERHUB_API_TOKEN is required")
		os.Exit(2)
	}
	target, err := url.Parse(os.Args[1])
	if err != nil || target.Scheme == "" || target.Host == "" {
		fmt.Fprintln(os.Stderr, "invalid mediator attach URL")
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot allocate the local attachment port")
		os.Exit(1)
	}
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
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 5 * time.Second}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	localURL := "http://" + listener.Addr().String()
	command := exec.CommandContext(ctx, "/usr/local/libexec/opencode-real", "attach", localURL)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	for _, value := range os.Environ() {
		if len(value) < len("JUPYTERHUB_API_TOKEN=") || value[:len("JUPYTERHUB_API_TOKEN=")] != "JUPYTERHUB_API_TOKEN=" {
			command.Env = append(command.Env, value)
		}
	}
	if err := command.Run(); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "OpenCode attachment ended with an error")
		_ = server.Close()
		os.Exit(1)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	select {
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "local attachment proxy failed")
		}
	default:
	}
}
