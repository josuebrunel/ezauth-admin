// Package admin is an embeddable admin panel for EzAuth.
//
// Mount it on any router that accepts an http.Handler:
//
//	panel, err := admin.New(auth, admin.WithAppName("Acme"))
//	r.Mount("/admin", panel.Handler())
//
// The host app must run auth.SessionMiddleware on its router (it needs that
// for login anyway). The panel adds its own guard on top: every route
// requires a logged-in user holding the admin role, and state-changing
// requests are checked for same-origin (CSRF).
package admin

import (
	"errors"
	"net/http"
	"strings"

	csrf "filippo.io/csrf/gorilla"
	"github.com/josuebrunel/ezauth"
	"github.com/josuebrunel/ezauth-admin/admin/views"
)

const (
	defaultBasePath = "/admin"
	defaultAppName  = "EzAuth"
)

// Panel is the admin panel. Create one with New and mount Handler().
type Panel struct {
	auth     *ezauth.EzAuth
	basePath string
	role     string
	appName  string
}

// New returns a Panel running on top of the given EzAuth instance.
func New(auth *ezauth.EzAuth, opts ...Option) (*Panel, error) {
	if auth == nil {
		return nil, errors.New("admin: ezauth instance is required")
	}
	p := &Panel{
		auth:     auth,
		basePath: defaultBasePath,
		role:     auth.Config.AdminRole,
		appName:  defaultAppName,
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.role == "" {
		return nil, errors.New("admin: an admin role is required (set EZAUTH_ADMIN_ROLE or use WithRole)")
	}
	return p, nil
}

// Handler returns the panel as an http.Handler. It is safe to mount with or
// without prefix stripping: the base path is removed from the request path
// when present, so chi's Mount, Echo's WrapHandler and http.StripPrefix all
// work.
func (p *Panel) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticSub())))
	mux.Handle("GET /{$}", p.handle(p.dashboard))

	guarded := p.guard(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = p.stripBase(r.URL.Path)
		r2.URL.RawPath = ""
		if r2.URL.Path == "" {
			r2.URL.Path = "/"
		}
		guarded.ServeHTTP(w, r2)
	})
}

func (p *Panel) stripBase(path string) string {
	if p.basePath == "" {
		return path
	}
	if path == p.basePath {
		return "/"
	}
	if rest, ok := strings.CutPrefix(path, p.basePath+"/"); ok {
		return "/" + rest
	}
	return path
}

// guard denies by default: login, then the admin role, then CSRF for
// state-changing methods (safe methods always pass).
func (p *Panel) guard(next http.Handler) http.Handler {
	h := csrf.Protect(nil)(next)
	h = p.auth.RequireRole(p.role)(h)
	return p.auth.LoginRequiredMiddleware(h)
}

// page builds the data every full page needs.
func (p *Panel) page(r *http.Request) views.Page {
	user, _ := ezauth.GetUser(r.Context())
	return views.Page{
		AppName:  p.appName,
		BasePath: p.basePath,
		User:     user,
	}
}

func (p *Panel) dashboard(w http.ResponseWriter, r *http.Request) error {
	return views.Dashboard(p.page(r)).Render(r.Context(), w)
}
