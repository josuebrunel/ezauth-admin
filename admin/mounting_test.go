package admin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/josuebrunel/ezauth-admin/admin"
)

// The panel is a plain http.Handler, so the guide promises it mounts on more
// than chi. These tests are those promises.

func serve(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// loginAndGet signs in as the admin and fetches path, returning status and body.
func loginAndGet(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()
	c := client(t)
	login(t, c, srv, "admin@example.com")
	code, body, _ := do(t, c, http.MethodGet, srv.URL+path, nil, false)
	return code, body
}

func TestMounting_StdlibServeMux(t *testing.T) {
	auth := newAuth(t)
	newUser(t, auth, "admin@example.com", "admin")
	panel, err := admin.New(auth)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/auth/", http.StripPrefix("/auth", auth.Handler)) // ezauth was created with path ""
	mux.Handle("/admin/", panel.Handler())
	mux.Handle("/admin", panel.Handler())
	srv := serve(t, auth.SessionMiddleware(mux))

	for _, path := range []string{"/admin", "/admin/", "/admin/users", "/admin/static/admin.css"} {
		if code, _ := loginAndGet(t, srv, path); code != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", path, code)
		}
	}
	// Still default-deny without a session.
	if code, _, _ := do(t, client(t), http.MethodGet, srv.URL+"/admin/users", nil, false); code != http.StatusFound {
		t.Errorf("anonymous: status %d, want a redirect to login", code)
	}
}

// A different base path: the option must match where the host mounts the panel,
// and every link the panel writes must follow it.
func TestMounting_CustomBasePath(t *testing.T) {
	auth := newAuth(t)
	newUser(t, auth, "admin@example.com", "admin")
	panel, err := admin.New(auth, admin.WithBasePath("/internal/ops/"))
	if err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Use(auth.SessionMiddleware)
	r.Mount("/auth", auth.Handler)
	r.Mount("/internal/ops", panel.Handler())
	srv := serve(t, r)

	code, body := loginAndGet(t, srv, "/internal/ops/users")
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	mustContain(t, body, `href="/internal/ops/users"`, `href="/internal/ops/audit"`, `/internal/ops/static/admin.css`, `hx-get="/internal/ops/users"`)
	mustNotContain(t, body, `href="/admin/`)

	// The old default path is not the panel any more.
	c := client(t)
	login(t, c, srv, "admin@example.com")
	if code, _, _ := do(t, c, http.MethodGet, srv.URL+"/admin/users", nil, false); code == http.StatusOK {
		t.Error("the panel also answers on /admin/ although it is mounted elsewhere")
	}
}

// A host may strip the prefix itself (http.StripPrefix) instead of leaving it to
// the panel. The panel copes with both, and still writes links under its base path.
func TestMounting_HostStripsThePrefix(t *testing.T) {
	auth := newAuth(t)
	newUser(t, auth, "admin@example.com", "admin")
	panel, err := admin.New(auth)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/auth/", http.StripPrefix("/auth", auth.Handler)) // ezauth was created with path ""
	mux.Handle("/admin/", http.StripPrefix("/admin", panel.Handler()))
	srv := serve(t, auth.SessionMiddleware(mux))

	code, body := loginAndGet(t, srv, "/admin/users")
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	mustContain(t, body, `href="/admin/users"`, `/admin/static/admin.css`)
}

// The panel needs the host's session middleware to know who is signed in. Without
// it nobody is, so everything stays closed instead of opening up, with a plain
// 500 and a hint in the log.
func TestMounting_WithoutSessionMiddlewareStaysClosed(t *testing.T) {
	auth := newAuth(t)
	newUser(t, auth, "admin@example.com", "admin")
	panel, err := admin.New(auth)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/auth/", http.StripPrefix("/auth", auth.Handler)) // ezauth was created with path ""
	mux.Handle("/admin/", panel.Handler())
	srv := serve(t, mux) // note: no auth.SessionMiddleware

	c := client(t)
	login(t, c, srv, "admin@example.com")
	code, body, _ := do(t, c, http.MethodGet, srv.URL+"/admin/users", nil, false)
	if code != http.StatusInternalServerError {
		t.Fatalf("status %d, want a clear 500 (not a dropped connection, and never 200)", code)
	}
	mustContain(t, body, "Something went wrong on our side.")
	mustNotContain(t, body, "session", "panic")
}
