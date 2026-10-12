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
	static := http.StripPrefix("/static/", http.FileServerFS(staticSub()))
	mux := http.NewServeMux()
	mux.Handle("GET /static/", static)
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

	guarded := p.guard(mux, static)
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
// normally not an admin, so the panel stays closed. See impersonating for what
// that session can still reach.
func (p *Panel) guard(next, static http.Handler) http.Handler {
	protected := csrf.Protect(nil)(p.auth.RequireRole(p.role)(next))
	stop := csrf.Protect(nil)(http.HandlerFunc(p.auth.Handler.FormStopImpersonation))

	guarded := p.auth.LoginRequiredMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if actorID, impersonating := ezauth.CurrentImpersonatorID(r.Context()); impersonating {
			p.impersonating(w, r, actorID, static, stop)
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

// impersonating answers every request made while an admin is impersonating
// someone. Only four things are reachable:
//
//   - the panel's own stylesheet, scripts and fonts, because the page below is
//     unusable without them (they hold nothing private);
//   - POST /impersonate/stop, the way back;
//   - GET /, the impersonated user's own profile;
//   - anything else that only reads is sent to that profile.
//
// Anything that changes something is refused.
func (p *Panel) impersonating(w http.ResponseWriter, r *http.Request, actorID string, static, stop http.Handler) {
	reads := r.Method == http.MethodGet || r.Method == http.MethodHead
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/impersonate/stop":
		stop.ServeHTTP(w, r)
	case reads && strings.HasPrefix(path, "/static/"):
		static.ServeHTTP(w, r)
	case reads && path == "/":
		if err := p.impersonationProfile(w, r, actorID); err != nil {
			p.renderError(w, r, err)
		}
	case reads:
		http.Redirect(w, r, p.basePath+"/", http.StatusSeeOther)
	default:
		p.renderError(w, r, NewHTTPError(http.StatusForbidden,
			"You can't change anything while you're impersonating. Stop impersonating first."))
	}
}

// impersonationProfile renders the impersonated user's own profile. It returns
// an error for renderError to show, or nil once the page has been written.
func (p *Panel) impersonationProfile(w http.ResponseWriter, r *http.Request, actorID string) error {
	ctx := r.Context()
	user, err := currentUser(r)
	if err != nil || user == nil {
		return NewHTTPError(http.StatusForbidden, "You're not signed in.")
	}

	admin := "an admin"
	if a, err := p.auth.Repo.UserGetByID(ctx, actorID); err == nil && a.Email != "" {
		admin = a.Email
	}
	var roles []string
	if rs, err := p.auth.UserRolesList(ctx, user.ID); err != nil {
		slog.WarnContext(ctx, "admin: could not list the impersonated user's roles", "user_id", user.ID, "err", err)
	} else {
		for _, role := range rs {
			roles = append(roles, role.Name)
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	if err := render(w, r, http.StatusOK, views.ImpersonationPage(p.page(r, ""), views.ImpersonationData{
		User: user, Admin: admin, Roles: roles, Now: time.Now(),
	})); err != nil {
		return fmt.Errorf("render the impersonation page: %w", err)
	}
	return nil
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
