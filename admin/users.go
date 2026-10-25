package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/josuebrunel/ezauth"
	"github.com/josuebrunel/ezauth/pkg/db/models"
	"github.com/josuebrunel/ezauth/pkg/service"

	"github.com/josuebrunel/ezauth-admin/admin/views"
)

const (
	usersPageSize   = 10
	maxSearchLength = 100
	maxNotesLength  = 2000
	maxPage         = 10000
	activityLimit   = 25
)

// isHTMX reports whether the request came from htmx (and wants a fragment).
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && !isBoosted(r) && !isHistoryRestore(r)
}

// isBoosted reports whether the request is an hx-boost navigation, which wants
// the page content for #main rather than a fragment.
func isBoosted(r *http.Request) bool {
	return r.Header.Get("HX-Boosted") == "true" && !isHistoryRestore(r)
}

// isHistoryRestore reports whether htmx is refilling the page after a back or
// forward step, which needs the whole document.
func isHistoryRestore(r *http.Request) bool {
	return r.Header.Get("HX-History-Restore-Request") == "true"
}

// render writes a templ component as an HTML response with the given status.
func render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Add("Vary", "HX-Request, HX-Boosted, HX-History-Restore-Request")
	w.WriteHeader(status)
	return c.Render(r.Context(), w)
}

// done answers a successful action: an htmx request gets the updated fragment
// plus a toast, a plain form post is redirected back to the page (PRG).
func (p *Panel) done(w http.ResponseWriter, r *http.Request, redirectTo string, message string, fragment templ.Component) error {
	if !isHTMX(r) {
		http.Redirect(w, r, redirectTo, http.StatusSeeOther)
		return nil
	}
	if fragment != nil {
		if err := render(w, r, http.StatusOK, fragment); err != nil {
			return err
		}
	} else {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	return views.Toast("info", message).Render(r.Context(), w)
}

// loadUser fetches one user, mapping "no such user" to a 404.
func (p *Panel) loadUser(ctx context.Context, id string) (*models.User, error) {
	user, err := p.auth.Repo.UserGetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, NewHTTPError(http.StatusNotFound, "That user doesn't exist.")
		}
		return nil, fmt.Errorf("load user %s: %w", id, err)
	}
	return user, nil
}

// isSelf reports whether id is the signed-in admin's own account.
func isSelf(ctx context.Context, id string) bool {
	me, err := ezauth.GetUserID(ctx)
	return err == nil && me == id
}

func (p *Panel) usersList(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()

	status := q.Get("status")
	switch status {
	case "", models.UserStatusActive, models.UserStatusSuspended, models.UserStatusLocked:
	default:
		return NewHTTPError(http.StatusBadRequest, "Unknown status filter.")
	}
	search := strings.TrimSpace(q.Get("q"))
	if len([]rune(search)) > maxSearchLength {
		return NewHTTPError(http.StatusBadRequest, "That search is too long.")
	}
	page, err := pageParam(r)
	if err != nil {
		return err
	}

	res, err := p.auth.Service.UsersList(r.Context(), service.ListUsersOptions{
		Search: search,
		Status: status,
		Limit:  usersPageSize,
		Offset: (page - 1) * usersPageSize,
	})
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}

	data := views.UsersData{
		Users:    res.Users,
		Query:    search,
		Status:   status,
		Page:     page,
		PageSize: usersPageSize,
		HasMore:  res.HasMore,
		Now:      time.Now(),
	}
	pg := p.page(r, "users", views.Crumb{Label: "Dashboard", Href: "/"}, views.Crumb{Label: "Users"})
	if isHTMX(r) {
		return render(w, r, http.StatusOK, views.UsersResults(pg, data))
	}
	return render(w, r, http.StatusOK, views.UsersPage(pg, data))
}

// detail builds the view model shared by the page and its fragments. Only the
// parts a view needs are loaded: roles for the roles tab and fragment, audit
// and token history for the activity tab.
func (p *Panel) detail(ctx context.Context, user *models.User, tab string) (views.UserDetail, error) {
	d := views.UserDetail{
		User: user,
		Tab:  tab,
		Self: isSelf(ctx, user.ID),
		Now:  time.Now(),
	}
	notes, _ := models.GetAppMeta[string](user, notesKey)
	d.Notes = notes

	switch tab {
	case "roles":
		return d, p.loadRoles(ctx, &d)
	case "activity":
		audit, err := p.auth.Service.AuditLogs(ctx, user.ID, service.ListAuditLogsOptions{Limit: activityLimit})
		if err != nil {
			return d, fmt.Errorf("load audit log: %w", err)
		}
		d.Audit = audit.Events
		d.History, err = p.auth.Service.UserAuthHistory(ctx, user.ID, activityLimit)
		if err != nil {
			return d, fmt.Errorf("load auth history: %w", err)
		}
	}
	return d, nil
}

func (p *Panel) loadRoles(ctx context.Context, d *views.UserDetail) error {
	var err error
	if d.Roles, err = p.auth.UserRolesList(ctx, d.User.ID); err != nil {
		return fmt.Errorf("load user roles: %w", err)
	}
	if d.AllRoles, err = p.auth.Service.RolesList(ctx); err != nil {
		return fmt.Errorf("load roles: %w", err)
	}
	if d.Perms, err = p.auth.Repo.PermissionsByUserID(ctx, d.User.ID); err != nil {
		return fmt.Errorf("load permissions: %w", err)
	}
	return nil
}

func (p *Panel) userDetail(w http.ResponseWriter, r *http.Request) error {
	user, err := p.loadUser(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	tab := r.URL.Query().Get("tab")
	switch tab {
	case "", "overview":
		tab = "overview"
	case "roles", "activity":
	default:
		return NewHTTPError(http.StatusBadRequest, "Unknown tab.")
	}
	d, err := p.detail(r.Context(), user, tab)
	if err != nil {
		return err
	}
	pg := p.page(r, "users",
		views.Crumb{Label: "Dashboard", Href: "/"},
		views.Crumb{Label: "Users", Href: "/users"},
		views.Crumb{Label: user.DisplayName()},
	)
	return render(w, r, http.StatusOK, views.UserDetailPage(pg, d))
}

// setActive suspends or reactivates an account.
func (p *Panel) setActive(active bool) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		id := r.PathValue("id")
		if _, err := p.loadUser(ctx, id); err != nil {
			return err
		}
		if !active && isSelf(ctx, id) {
			return NewHTTPError(http.StatusBadRequest, "You can't suspend your own account.")
		}

		var err error
		msg := "Account reactivated."
		if active {
			_, err = p.auth.Service.UserReactivate(ctx, id)
		} else {
			_, err = p.auth.Service.UserSuspend(ctx, id)
			msg = "Account suspended."
		}
		if err != nil {
			return fmt.Errorf("set active=%t for user %s: %w", active, id, err)
		}

		user, err := p.loadUser(ctx, id)
		if err != nil {
			return err
		}
		d, err := p.detail(ctx, user, "overview")
		if err != nil {
			return err
		}
		pg := p.page(r, "users")
		return p.done(w, r, pg.Path("/users/"+id), msg, views.UserHeader(pg, d))
	}
}

// userNotes saves the admin-only note kept in the user's AppMetadata.
func (p *Panel) userNotes(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	notes := strings.TrimSpace(r.FormValue("notes"))
	if len([]rune(notes)) > maxNotesLength {
		return NewHTTPError(http.StatusBadRequest, "That note is too long.")
	}
	user, err := p.loadUser(ctx, id)
	if err != nil {
		return err
	}
	// UserUpdate only writes what is set and never the account flags, so
	// saving the user back changes just the note.
	user.SetAppMeta(notesKey, notes)
	if _, err := p.auth.Service.UserUpdate(ctx, user); err != nil {
		return fmt.Errorf("save notes for user %s: %w", id, err)
	}
	return p.done(w, r, p.page(r, "users").Path("/users/"+id), "Note saved.", nil)
}

func (p *Panel) rolesFragment(w http.ResponseWriter, r *http.Request, id, msg string) error {
	ctx := r.Context()
	user, err := p.loadUser(ctx, id)
	if err != nil {
		return err
	}
	d, err := p.detail(ctx, user, "roles")
	if err != nil {
		return err
	}
	pg := p.page(r, "users")
	return p.done(w, r, pg.Path("/users/"+id)+"?tab=roles", msg, views.UserRolesPanel(pg, d))
}

func (p *Panel) roleGrant(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	role := strings.TrimSpace(r.FormValue("role"))
	if _, err := p.loadUser(ctx, id); err != nil {
		return err
	}
	roles, err := p.auth.Service.RolesList(ctx)
	if err != nil {
		return fmt.Errorf("load roles: %w", err)
	}
	if !hasRole(roles, role) {
		return NewHTTPError(http.StatusBadRequest, "That role doesn't exist.")
	}
	actor, _ := ezauth.GetUserID(ctx)
	if err := p.auth.UserRoleGrant(ctx, actor, id, role); err != nil {
		return fmt.Errorf("grant %s to user %s: %w", role, id, err)
	}
	return p.rolesFragment(w, r, id, "Granted "+role+".")
}

func (p *Panel) roleRevoke(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	role := strings.TrimSpace(r.FormValue("role"))
	if role == "" {
		return NewHTTPError(http.StatusBadRequest, "Pick a role to revoke.")
	}
	if isSelf(ctx, id) && role == p.role {
		return NewHTTPError(http.StatusBadRequest, "You can't revoke your own "+role+" role. Ask another admin to do it.")
	}
	if _, err := p.loadUser(ctx, id); err != nil {
		return err
	}
	actor, _ := ezauth.GetUserID(ctx)
	if err := p.auth.UserRoleRevoke(ctx, actor, id, role); err != nil {
		return fmt.Errorf("revoke %s from user %s: %w", role, id, err)
	}
	return p.rolesFragment(w, r, id, "Revoked "+role+".")
}

func hasRole(roles []*models.Role, name string) bool {
	for _, r := range roles {
		if r.Name == name {
			return true
		}
	}
	return false
}

// userImpersonate starts an impersonation session through ezauth's own form
// handler, which owns the cookie swap, the hook and the redirect.
func (p *Panel) userImpersonate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	if isSelf(ctx, id) {
		return NewHTTPError(http.StatusBadRequest, "You can't impersonate yourself.")
	}
	if _, err := p.loadUser(ctx, id); err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return NewHTTPError(http.StatusBadRequest, "That request was malformed.")
	}
	r.Form.Set("target_user_id", id)
	p.auth.Handler.FormImpersonate(w, r)
	return nil
}

const notesKey = "notes"
