// Command chi is a minimal host app that mounts the EzAuth admin panel on a
// chi router, backed by SQLite. It runs with no configuration:
//
//	go run ./examples/chi
//
// On first start it creates an admin user and a few demo users and logs
// their random passwords once. Then open http://localhost:8080.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/josuebrunel/ezauth"
	ezconfig "github.com/josuebrunel/ezauth/pkg/config"

	"github.com/josuebrunel/ezauth-admin/admin"
)

func main() {
	if err := run(); err != nil {
		slog.Error("example failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll("data", 0o755); err != nil {
		return err
	}
	applyDemoDefaults()

	cfg, err := ezconfig.LoadConfig()
	if err != nil {
		return err
	}
	// "" because chi's Mount below adds the /auth prefix.
	auth, err := ezauth.New(&cfg, "")
	if err != nil {
		return err
	}
	if err := auth.Migrate(); err != nil {
		return err
	}
	if err := seed(ctx, auth, getenv("APP_ADMIN_EMAIL", "admin@example.com"), os.Getenv("APP_ADMIN_PASSWORD"), os.Getenv("APP_DEMO_PASSWORD")); err != nil {
		return err
	}

	panel, err := admin.New(auth, admin.WithAppName("Acme"))
	if err != nil {
		return err
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger, middleware.Recoverer)
	r.Use(auth.SessionMiddleware) // needed for login, and by the panel

	// App-owned pages. They live outside /auth and /admin and must not sit
	// behind LoginRequired, or login would redirect to itself.
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/account", http.StatusFound)
	})
	r.Get("/login", loginPage(auth))
	r.Get("/register", registerPage(auth))
	r.Get("/forgot-password", forgotPage(auth))
	r.Get("/reset-password", resetPage(auth))
	// The emailed reset link is a GET that ezauth's POST-only route can't
	// serve. Chi matches this exact path before the /auth mount below.
	r.Get("/auth/password-reset/confirm", resetRedirect)

	acct := account{auth}
	r.Get("/account", acct.profilePage)
	r.Post("/account/profile", acct.profileUpdate)
	r.Post("/account/password", acct.passwordUpdate)
	r.Get("/account/api-keys", acct.apiKeysPage)
	r.Get("/account/sessions", acct.sessionsPage)
	r.Get("/invitation/accept", invitationPage(auth)) // where the emailed invitation link lands

	r.Mount("/auth", auth.Handler)
	r.Mount("/admin", panel.Handler()) // the one line this repo is about

	srv := &http.Server{
		Addr:              getenv("APP_ADDR", ":8080"),
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", "addr", srv.Addr, "admin", "http://localhost"+srv.Addr+"/admin/")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// applyDemoDefaults makes the example runnable with zero setup. These are
// throwaway development values: a real app sets its own EZAUTH_* secrets.
func applyDemoDefaults() {
	defaults := map[string]string{
		"EZAUTH_API_KEY":              "demo-api-key-change-me",
		"EZAUTH_JWT_SECRET":           "demo-jwt-secret-change-me-at-least-32-chars",
		"EZAUTH_DB_DIALECT":           "sqlite",
		"EZAUTH_DB_DSN":               "file:data/ezauth.db",
		"EZAUTH_LOGIN_PAGE_URL":       "/login",
		"EZAUTH_REDIRECT_AFTER_LOGIN": "/account",
	}
	// On Render, invitation links must use the public address, not localhost.
	if u := os.Getenv("RENDER_EXTERNAL_URL"); u != "" {
		defaults["EZAUTH_BASE_URL"] = u
	}
	for k, v := range defaults {
		if _, ok := os.LookupEnv(k); !ok {
			_ = os.Setenv(k, v)
		}
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
