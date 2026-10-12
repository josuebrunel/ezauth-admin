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
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	csrf "filippo.io/csrf/gorilla"
	"github.com/josuebrunel/ezauth"
	"github.com/josuebrunel/ezauth/pkg/db/models"

	"github.com/josuebrunel/ezauth-admin/admin/views"
)

const (
	defaultBasePath = "/admin"
	defaultAppName  = "EzAuth"

	// defaultDashboardCache is how long the dashboard numbers are reused.
	defaultDashboardCache = 30 * time.Second
)

// Panel is the admin panel. Create one with New and mount Handler().
type Panel struct {
	auth     *ezauth.EzAuth
	basePath string
	role     string
	appName  string
	counts   countsCache
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
		counts:   countsCache{ttl: defaultDashboardCache},
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
	mux.Handle("GET /users", p.handle(p.usersList))
	mux.Handle("GET /users/{id}", p.handle(p.userDetail))
	mux.Handle("GET /users/{id}/edit", p.handle(p.userEditPage))
	mux.Handle("POST /users/{id}/edit", p.handle(p.userEditSave))
	mux.Handle("POST /users/{id}/delete", p.handle(p.userDelete))
	mux.Handle("POST /users/{id}/suspend", p.handle(p.setActive(false)))
	mux.Handle("POST /users/{id}/reactivate", p.handle(p.setActive(true)))
	mux.Handle("POST /users/{id}/impersonate", p.handle(p.userImpersonate))
	mux.Handle("POST /users/{id}/notes", p.handle(p.userNotes))
	mux.Handle("POST /users/{id}/roles/grant", p.handle(p.roleGrant))
	mux.Handle("POST /users/{id}/roles/revoke", p.handle(p.roleRevoke))
	mux.Handle("GET /audit", p.handle(p.auditPage))
	mux.Handle("GET /roles", p.handle(p.rolesPage))
	mux.Handle("POST /roles", p.handle(p.roleCreate))
	mux.Handle("POST /roles/{id}/delete", p.handle(p.roleDelete))
	mux.Handle("POST /roles/{id}/permissions/grant", p.handle(p.rolePermission(true)))
	mux.Handle("POST /roles/{id}/permissions/revoke", p.handle(p.rolePermission(false)))
	mux.Handle("GET /permissions", p.handle(p.permissionsPage))
	mux.Handle("POST /permissions", p.handle(p.permissionCreate))
	mux.Handle("POST /permissions/{id}/delete", p.handle(p.permissionDelete))
	mux.Handle("/", p.handle(func(http.ResponseWriter, *http.Request) error {
		return NewHTTPError(http.StatusNotFound, "That page doesn't exist.")
	}))

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
//
// While an admin impersonates someone, the session user is the target, who is
// normally not an admin. The panel stays closed to them, except for a page
// that explains why and a button to stop impersonating.
func (p *Panel) guard(next http.Handler) http.Handler {
	protected := csrf.Protect(nil)(p.auth.RequireRole(p.role)(next))
	stop := csrf.Protect(nil)(http.HandlerFunc(p.auth.Handler.FormStopImpersonation))

	guarded := p.auth.LoginRequiredMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, impersonating := ezauth.CurrentImpersonatorID(r.Context()); impersonating {
			if r.Method == http.MethodPost && r.URL.Path == "/impersonate/stop" {
				stop.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			if err := views.ImpersonatingPage(p.page(r, "")).Render(r.Context(), w); err != nil {
				slog.ErrorContext(r.Context(), "admin impersonating page render failed", "err", err)
			}
			return
		}
		protected.ServeHTTP(w, r)
	}))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			// Almost always: the host router doesn't run auth.SessionMiddleware, so
			// there is no session to read. Say so instead of dropping the connection.
			slog.ErrorContext(r.Context(), "admin: request panicked. If the message mentions a session, "+
				"mount auth.SessionMiddleware on the router the panel is mounted on",
				"panic", fmt.Sprint(v), "method", r.Method, "path", r.URL.Path)
			p.renderError(w, r, errors.New("panel panicked"))
		}()
		guarded.ServeHTTP(w, r)
	})
}

// page builds the data every full page needs.
func (p *Panel) page(r *http.Request, active string, crumbs ...views.Crumb) views.Page {
	user, _ := currentUser(r)
	return views.Page{
		AppName:  p.appName,
		BasePath: p.basePath,
		User:     user,
		Active:   active,
		Crumbs:   crumbs,
		Notice:   notices[r.URL.Query().Get("notice")],
	}
}

// currentUser returns the signed-in user, if any.
func currentUser(r *http.Request) (*models.User, error) {
	return ezauth.GetUser(r.Context())
}
