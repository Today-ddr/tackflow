package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/today/tack/internal/api"
	"github.com/today/tack/internal/auth"
	"github.com/today/tack/internal/config"
	"github.com/today/tack/internal/store"
	"github.com/today/tack/web"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout))
}

func run(args []string, getenv func(string) string, stdout io.Writer) int {
	if len(args) == 0 {
		if err := serve(getenv, stdout); err != nil {
			_, _ = fmt.Fprintln(stdout, "error:", err)
			return 1
		}
		return 0
	}

	switch args[0] {
	case "healthcheck":
		if err := healthcheck(getenv); err != nil {
			_, _ = fmt.Fprintln(stdout, "healthcheck failed:", err)
			return 1
		}
		return 0
	case "migrate":
		if err := migrate(getenv); err != nil {
			_, _ = fmt.Fprintln(stdout, "migrate failed:", err)
			return 1
		}
		return 0
	case "backup":
		if err := backup(getenv, stdout); err != nil {
			_, _ = fmt.Fprintln(stdout, "backup failed:", err)
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprintln(stdout, "usage: tack [healthcheck|migrate|backup]")
		return 1
	}
}

func serve(getenv func(string) string, stdout io.Writer) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	st, err := store.Open(context.Background(), cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	if err := ensureAdmin(ctx, cfg, st); err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stdout, nil))
	handler := api.NewServer(
		cfg,
		st,
		web.Assets(),
		api.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime},
		logger,
		time.Now,
	).Handler()
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	stop, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-stop.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func ensureAdmin(ctx context.Context, cfg config.Config, st *store.Store) error {
	if _, err := st.UserByID(ctx, 1); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if len([]rune(cfg.AdminPassword)) < 12 {
		return fmt.Errorf("TACK_ADMIN_PASSWORD must be at least 12 characters on first boot")
	}
	hash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		return err
	}
	_, err = st.BootstrapAdmin(ctx, cfg.AdminUsername, hash, time.Now().UTC())
	return err
}

func migrate(getenv func(string) string) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	st, err := store.Open(context.Background(), cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return st.Migrate(context.Background())
}

func backup(getenv func(string) string, stdout io.Writer) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	st, err := store.Open(context.Background(), cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if err := st.Migrate(context.Background()); err != nil {
		return err
	}

	destination := filepath.Join(
		cfg.DataDir,
		"backups",
		"tack-"+time.Now().UTC().Format("20060102-150405")+".db",
	)
	if err := st.Backup(context.Background(), destination); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, destination)
	return err
}

func healthcheck(getenv func(string) string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(healthcheckURL(getenv))
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", response.StatusCode)
	}
	return nil
}

func healthcheckURL(getenv func(string) string) string {
	addr := strings.TrimSpace(getenv("TACK_ADDR"))
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080/healthz"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz"
}
