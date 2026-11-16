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
	"sort"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	tack "github.com/today/tack"
	"github.com/today/tack/internal/api"
	"github.com/today/tack/internal/auth"
	"github.com/today/tack/internal/config"
	"github.com/today/tack/internal/store"
	"github.com/today/tack/internal/update"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	if err := ensureAdmin(ctx, cfg, st); err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stdout, nil))
	updateService := update.NewService(
		st,
		update.NewChecker(cfg.GitHubRepo, cfg.GitHubToken),
		version,
		time.Now,
		logger,
	)
	updateService.Start(ctx)
	go startMaintenance(ctx, cfg, st, logger)

	apiServer := api.NewServer(
		cfg,
		st,
		web.Assets(),
		api.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime},
		logger,
		time.Now,
	)
	apiServer.SetChangelog(tack.Changelog)
	apiServer.SetUpdateService(
		updateService,
		update.NewUpdaterClient(cfg.UpdaterURL, cfg.UpdaterToken),
	)
	handler := apiServer.Handler()
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
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownCtx)
	}
}

func ensureAdmin(ctx context.Context, cfg config.Config, st *store.Store) error {
	if _, err := st.UserByID(ctx, 1); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := auth.ValidatePassword(cfg.AdminPassword); err != nil {
		return fmt.Errorf("TACK_ADMIN_PASSWORD is invalid on first boot: %w", err)
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

func startMaintenance(
	ctx context.Context,
	cfg config.Config,
	st *store.Store,
	logger *slog.Logger,
) {
	run := func() {
		now := time.Now().In(cfg.Timezone)
		today := now.Format("2006-01-02")
		last, ok, err := st.GetSetting(ctx, "maintenance.daily_date")
		if err != nil {
			logger.Warn("read maintenance setting", "error", err)
			return
		}
		if !ok || last != today {
			destination := filepath.Join(
				cfg.DataDir,
				"backups",
				"daily-"+now.Format("20060102")+".db",
			)
			if _, statErr := os.Stat(destination); errors.Is(statErr, os.ErrNotExist) {
				if err := st.Backup(ctx, destination); err != nil {
					logger.Warn("daily backup failed", "error", err)
				} else {
					logger.Info("daily backup written", "path", destination)
				}
			}
			if err := pruneBackups(filepath.Dir(destination), "daily-", 14); err != nil {
				logger.Warn("prune daily backups failed", "error", err)
			}
			if err := st.SetSetting(ctx, "maintenance.daily_date", today); err != nil {
				logger.Warn("write maintenance setting", "error", err)
			}
		}
		if purged, err := st.PurgeTrash(ctx, time.Now().UTC().AddDate(0, 0, -30)); err != nil {
			logger.Warn("purge trash failed", "error", err)
		} else if purged > 0 {
			logger.Info("purged trashed tasks", "count", purged)
		}
	}

	run()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func pruneBackups(dir, prefix string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	names := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) &&
			strings.HasSuffix(entry.Name(), ".db") {
			names = append(names, entry.Name())
		}
	}
	if len(names) <= keep {
		return nil
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}
