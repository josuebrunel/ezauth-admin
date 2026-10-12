package admin_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/josuebrunel/ezauth"
	ezconfig "github.com/josuebrunel/ezauth/pkg/config"
	"github.com/josuebrunel/ezauth/pkg/service"

	"github.com/josuebrunel/ezauth-admin/admin"
)

const testPassword = "correct-horse-battery"

// newAuth returns an EzAuth backed by a throwaway sqlite file.
func newAuth(t *testing.T) *ezauth.EzAuth {
	t.Helper()
	t.Setenv("EZAUTH_API_KEY", "test-api-key")
	t.Setenv("EZAUTH_JWT_SECRET", "test-jwt-secret-at-least-32-characters-long")
	t.Setenv("EZAUTH_HASHING_BCRYPT_COST", "4") // correctness doesn't need slow hashes
	t.Setenv("EZAUTH_DB_DIALECT", "sqlite")
	t.Setenv("EZAUTH_DB_DSN", "file:"+filepath.Join(t.TempDir(), "test.db"))
	cfg, err := ezconfig.LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	auth, err := ezauth.New(&cfg, "") // "" because chi's Mount adds the /auth prefix
	if err != nil {
		t.Fatalf("new ezauth: %v", err)
	}
	if err := auth.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return auth
}

// newUser creates a user, optionally holding role.
func newUser(t *testing.T, auth *ezauth.EzAuth, email, role string) {
	t.Helper()
	ctx := context.Background()
	u, err := auth.Service.UserCreate(ctx, &service.RequestBasicAuth{Email: email, Password: testPassword})
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	if role == "" {
		return
	}
	if _, err := auth.Repo.RoleGetByName(ctx, role); err != nil {
		if _, err := auth.RoleCreate(ctx, role, role); err != nil {
			t.Fatalf("create role %s: %v", role, err)
		}
	}
	if err := auth.UserRoleGrant(ctx, u.ID, role); err != nil {
		t.Fatalf("grant role %s: %v", role, err)
	}
}

// newServer mounts ezauth under /auth and the panel under /admin on a chi
// router, the way a host app would.
func newServer(t *testing.T, auth *ezauth.EzAuth, opts ...admin.Option) *httptest.Server {
	t.Helper()
	panel, err := admin.New(auth, opts...)
	if err != nil {
		t.Fatalf("new panel: %v", err)
	}
	r := chi.NewRouter()
	r.Use(auth.SessionMiddleware)
	r.Mount("/auth", auth.Handler)
	r.Mount("/admin", panel.Handler())
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// client returns a cookie-keeping client that does not follow redirects.
func client(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func login(t *testing.T, c *http.Client, srv *httptest.Server, email string) {
	t.Helper()
	resp, err := c.PostForm(srv.URL+"/auth/login", url.Values{"email": {email}, "password": {testPassword}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		t.Fatalf("login %s: status %d", email, resp.StatusCode)
	}
}

func TestNew_Validation(t *testing.T) {
	if _, err := admin.New(nil); err == nil {
		t.Error("New(nil) should fail")
	}
	auth := newAuth(t)
	if _, err := admin.New(auth, admin.WithRole("")); err == nil {
		t.Error("New with an empty role should fail")
	}
}

func TestPanel_Authz(t *testing.T) {
	auth := newAuth(t)
	newUser(t, auth, "admin@example.com", "admin")
	newUser(t, auth, "member@example.com", "")
	newUser(t, auth, "editor@example.com", "editor")
	srv := newServer(t, auth)

	tests := []struct {
		name    string
		email   string // empty means not logged in
		path    string
		want    int
		wantLoc string
	}{
		{"anonymous is redirected to login", "", "/admin/", http.StatusFound, "/login"},
		{"anonymous cannot fetch assets", "", "/admin/static/admin.css", http.StatusFound, "/login"},
		{"user without role is forbidden", "member@example.com", "/admin/", http.StatusForbidden, ""},
		{"user with another role is forbidden", "editor@example.com", "/admin/", http.StatusForbidden, ""},
		{"admin sees the dashboard", "admin@example.com", "/admin/", http.StatusOK, ""},
		{"admin without trailing slash", "admin@example.com", "/admin", http.StatusOK, ""},
		{"admin gets built css", "admin@example.com", "/admin/static/admin.css", http.StatusOK, ""},
		{"admin gets htmx", "admin@example.com", "/admin/static/htmx.min.js", http.StatusOK, ""},
		{"unknown path is 404", "admin@example.com", "/admin/nope", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := client(t)
			if tt.email != "" {
				login(t, c, srv, tt.email)
			}
			resp, err := c.Get(srv.URL + tt.path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tt.want {
				t.Fatalf("GET %s as %q: status %d, want %d", tt.path, tt.email, resp.StatusCode, tt.want)
			}
			if tt.wantLoc != "" && !strings.Contains(resp.Header.Get("Location"), tt.wantLoc) {
				t.Errorf("Location = %q, want it to contain %q", resp.Header.Get("Location"), tt.wantLoc)
			}
		})
	}
}

func TestPanel_Options(t *testing.T) {
	auth := newAuth(t)
	newUser(t, auth, "ops@example.com", "ops")
	newUser(t, auth, "admin@example.com", "admin")
	srv := newServer(t, auth, admin.WithRole("ops"), admin.WithAppName("Acme"))

	c := client(t)
	login(t, c, srv, "ops@example.com")
	resp, err := c.Get(srv.URL + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("custom role: status %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Acme") {
		t.Error("page does not show the custom app name")
	}

	// The default admin role no longer grants access once WithRole changes it.
	c2 := client(t)
	login(t, c2, srv, "admin@example.com")
	resp2, err := c2.Get(srv.URL + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("default admin role with WithRole(ops): status %d, want 403", resp2.StatusCode)
	}
}

func TestPanel_CSRF(t *testing.T) {
	auth := newAuth(t)
	newUser(t, auth, "admin@example.com", "admin")
	srv := newServer(t, auth)
	c := client(t)
	login(t, c, srv, "admin@example.com")

	// A cross-site POST must be rejected by the panel's CSRF guard even for
	// an authenticated admin. No POST route exists yet, so a rejection (403)
	// proves the guard runs before routing, rather than a 405 from the mux.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site POST: status %d, want 403", resp.StatusCode)
	}
}
