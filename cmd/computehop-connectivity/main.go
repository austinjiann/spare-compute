package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/austinjiann/spare-compute/internal/connectivity/rendezvous"
)

var version = "dev"

type options struct {
	listenAddress       string
	showVersion         bool
	maxRoutes           int
	maxSignalsPerRoute  int
	maxPayloadBytes     int
	maxRequestsPerRoute int
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "computehop-connectivity: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	parsed, err := parseOptions(arguments, stderr)
	if err != nil {
		return err
	}
	if parsed.showVersion {
		_, err := fmt.Fprintln(stdout, version)
		return err
	}
	if parsed.listenAddress == "" || parsed.maxRoutes <= 0 || parsed.maxSignalsPerRoute <= 0 ||
		parsed.maxPayloadBytes <= 0 || parsed.maxRequestsPerRoute <= 0 {
		return errors.New("listen address and resource limits must be positive")
	}
	handler, err := rendezvous.New(rendezvous.Config{
		MaxRoutes: parsed.maxRoutes, MaxSignalsPerRoute: parsed.maxSignalsPerRoute,
		MaxPayloadBytes: parsed.maxPayloadBytes, MaxRequestsPerRoute: parsed.maxRequestsPerRoute,
	})
	if err != nil {
		return fmt.Errorf("configure rendezvous service: %w", err)
	}
	listener, err := net.Listen("tcp", parsed.listenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", parsed.listenAddress, err)
	}
	defer listener.Close()
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    8 << 10,
	}
	if _, err := fmt.Fprintf(stdout, "computehop-connectivity %s listening on %s\n", version, listener.Addr()); err != nil {
		return err
	}
	serverResult := make(chan error, 1)
	go func() { serverResult <- server.Serve(listener) }()
	select {
	case err := <-serverResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve rendezvous traffic: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shut down rendezvous service: %w", err)
		}
		err := <-serverResult
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve rendezvous traffic: %w", err)
		}
		return nil
	}
}

func parseOptions(arguments []string, stderr io.Writer) (options, error) {
	parsed := options{}
	flags := flag.NewFlagSet("computehop-connectivity", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&parsed.listenAddress, "listen", defaultListenAddress(), "HTTP listen address behind the TLS edge")
	flags.BoolVar(&parsed.showVersion, "version", false, "print version and exit")
	flags.IntVar(&parsed.maxRoutes, "max-routes", 10_000, "maximum concurrent anonymous pair routes")
	flags.IntVar(&parsed.maxSignalsPerRoute, "max-signals-per-route", 64, "maximum queued signals per pair route")
	flags.IntVar(&parsed.maxPayloadBytes, "max-payload-bytes", 32<<10, "maximum encrypted presence or signal payload")
	flags.IntVar(&parsed.maxRequestsPerRoute, "max-requests-per-route", 240, "maximum requests per pair route per minute")
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	return parsed, nil
}

func defaultListenAddress() string {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	return net.JoinHostPort("", port)
}
