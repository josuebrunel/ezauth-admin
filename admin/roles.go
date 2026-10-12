package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/josuebrunel/ezauth/pkg/db/models"

	"github.com/josuebrunel/ezauth-admin/admin/views"
)

// Role and permission names are identifiers an app checks in code, so keep
// them to a safe, readable alphabet: invoices.read, billing-admin, org:owner.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{1,49}$`)

const (
	maxDescriptionLength = 200
	rolesPageSize        = 10
	permissionsPageSize  = 20
)

// pageOf returns the page-th slice of n items (clamped to the last page) and
// how many pages there are.
func pageOf(n, page, size int) (from, to, pages, clamped int) {
	pages = (n + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	clamped = min(max(page, 1), pages)
	from = (clamped - 1) * size
	to = min(from+size, n)
	return from, to, pages, clamped
}

// parseNameDescription reads and validates the create form shared by roles
// and permissions.
func parseNameDescription(r *http.Request, kind string) (name, description string, err error) {
	name = strings.TrimSpace(r.FormValue("name"))
	description = strings.TrimSpace(r.FormValue("description"))
	if !nameRe.MatchString(name) {
		return "", "", NewHTTPError(http.StatusBadRequest,
			"A "+kind+" name is 2 to 50 characters: letters, numbers, dots, colons, underscores or hyphens, starting with a letter or number.")
	}
	if len([]rune(description)) > maxDescriptionLength {
		return "", "", NewHTTPError(http.StatusBadRequest, "Keep the description under 200 characters.")
	}
	return name, description, nil
}

// rolesData loads one page of roles with their permissions. Permissions are
// only fetched for the roles on the page, one query each.
func (p *Panel) rolesData(ctx context.Context, page int) (views.RolesData, error) {
	roles, err := p.auth.Service.RolesList(ctx)
	if err != nil {
		return views.RolesData{}, fmt.Errorf("list roles: %w", err)
	}
	all, err := p.auth.Service.PermissionsList(ctx)
	if err != nil {
		return views.RolesData{}, fmt.Errorf("list permissions: %w", err)
	}
	slices.SortFunc(roles, func(a, b *models.Role) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(all, func(a, b *models.Permission) int { return strings.Compare(a.Name, b.Name) })

	from, to, pages, page := pageOf(len(roles), page, rolesPageSize)
	d := views.RolesData{AdminRole: p.role, Page: page, Pages: pages, Total: len(roles), Rows: make([]views.RoleRow, 0, to-from)}
	for _, role := range roles[from:to] {
		held, err := p.auth.Repo.PermissionsByRoleID(ctx, role.ID)
		if err != nil {
			return d, fmt.Errorf("list permissions of role %s: %w", role.ID, err)
		}
		slices.SortFunc(held, func(a, b *models.Permission) int { return strings.Compare(a.Name, b.Name) })
		has := make(map[string]bool, len(held))
		for _, perm := range held {
			has[perm.ID] = true
		}
		row := views.RoleRow{Role: role, Perms: held}
		for _, perm := range all {
			if !has[perm.ID] {
				row.Grantable = append(row.Grantable, perm)
			}
		}
		d.Rows = append(d.Rows, row)
	}
	return d, nil
}

func (p *Panel) rolesPage(w http.ResponseWriter, r *http.Request) error {
	page, err := pageParam(r)
	if err != nil {
		return err
	}
	d, err := p.rolesData(r.Context(), page)
	if err != nil {
		return err
	}
	pg := p.page(r, "roles", views.Crumb{Label: "Dashboard", Href: "/"}, views.Crumb{Label: "Roles"})
	if isHTMX(r) && r.Method == http.MethodGet {
		return render(w, r, http.StatusOK, views.RolesRegion(pg, d))
	}
	return render(w, r, http.StatusOK, views.RolesPage(pg, d))
}

// rolesDone answers a successful roles action with the refreshed list, on the
// page the admin was looking at (the forms post it back as "page").
func (p *Panel) rolesDone(w http.ResponseWriter, r *http.Request, message string) error {
	page, err := pageParam(r)
	if err != nil {
		return err
	}
	d, err := p.rolesData(r.Context(), page)
	if err != nil {
		return err
	}
	pg := p.page(r, "roles")
	return p.done(w, r, pg.Path("/roles"), message, views.RolesRegion(pg, d))
}

// loadRole fetches a role by id, mapping "no such role" to a 404.
func (p *Panel) loadRole(ctx context.Context, id string) (*models.Role, error) {
	role, err := p.auth.Repo.RoleGetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, NewHTTPError(http.StatusNotFound, "That role doesn't exist.")
		}
		return nil, fmt.Errorf("load role %s: %w", id, err)
	}
	return role, nil
}

func (p *Panel) roleCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	name, description, err := parseNameDescription(r, "role")
	if err != nil {
		return err
	}
	if _, err := p.auth.Repo.RoleGetByName(ctx, name); err == nil {
		return NewHTTPError(http.StatusConflict, "A role named "+name+" already exists.")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check role %s: %w", name, err)
	}
	if _, err := p.auth.RoleCreate(ctx, name, description); err != nil {
		return fmt.Errorf("create role %s: %w", name, err)
	}
	return p.rolesDone(w, r, "Created "+name+".")
}

func (p *Panel) roleDelete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	role, err := p.loadRole(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	if role.Name == p.role {
		return NewHTTPError(http.StatusBadRequest,
			"The "+role.Name+" role opens this panel, so deleting it would lock every admin out.")
	}
	if err := p.auth.Service.RoleDelete(ctx, role.ID); err != nil {
		return fmt.Errorf("delete role %s: %w", role.ID, err)
	}
	return p.rolesDone(w, r, "Deleted "+role.Name+".")
}

// rolePermission grants or revokes one permission on a role.
func (p *Panel) rolePermission(grant bool) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		role, err := p.loadRole(ctx, r.PathValue("id"))
		if err != nil {
			return err
		}
		name := strings.TrimSpace(r.FormValue("permission"))
		if _, err := p.auth.Repo.PermissionGetByName(ctx, name); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return NewHTTPError(http.StatusBadRequest, "That permission doesn't exist.")
			}
			return fmt.Errorf("load permission %s: %w", name, err)
		}
		msg := "Removed " + name + " from " + role.Name + "."
		if grant {
			err = p.auth.RolePermissionGrant(ctx, role.Name, name)
			msg = "Added " + name + " to " + role.Name + "."
		} else {
			err = p.auth.RolePermissionRevoke(ctx, role.Name, name)
		}
		if err != nil {
			return fmt.Errorf("grant=%t permission %s on role %s: %w", grant, name, role.ID, err)
		}
		return p.rolesDone(w, r, msg)
	}
}

func (p *Panel) permissionsData(ctx context.Context, page int) (views.PermissionsData, error) {
	perms, err := p.auth.Service.PermissionsList(ctx)
	if err != nil {
		return views.PermissionsData{}, fmt.Errorf("list permissions: %w", err)
	}
	slices.SortFunc(perms, func(a, b *models.Permission) int { return strings.Compare(a.Name, b.Name) })
	from, to, pages, page := pageOf(len(perms), page, permissionsPageSize)
	return views.PermissionsData{Perms: perms[from:to], Page: page, Pages: pages, Total: len(perms)}, nil
}

func (p *Panel) permissionsPage(w http.ResponseWriter, r *http.Request) error {
	page, err := pageParam(r)
	if err != nil {
		return err
	}
	d, err := p.permissionsData(r.Context(), page)
	if err != nil {
		return err
	}
	pg := p.page(r, "permissions", views.Crumb{Label: "Dashboard", Href: "/"}, views.Crumb{Label: "Permissions"})
	if isHTMX(r) && r.Method == http.MethodGet {
		return render(w, r, http.StatusOK, views.PermissionsRegion(pg, d))
	}
	return render(w, r, http.StatusOK, views.PermissionsPage(pg, d))
}

func (p *Panel) permissionsDone(w http.ResponseWriter, r *http.Request, message string) error {
	page, err := pageParam(r)
	if err != nil {
		return err
	}
	d, err := p.permissionsData(r.Context(), page)
	if err != nil {
		return err
	}
	pg := p.page(r, "permissions")
	return p.done(w, r, pg.Path("/permissions"), message, views.PermissionsRegion(pg, d))
}

func (p *Panel) permissionCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	name, description, err := parseNameDescription(r, "permission")
	if err != nil {
		return err
	}
	if _, err := p.auth.Repo.PermissionGetByName(ctx, name); err == nil {
		return NewHTTPError(http.StatusConflict, "A permission named "+name+" already exists.")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check permission %s: %w", name, err)
	}
	if _, err := p.auth.PermissionCreate(ctx, name, description); err != nil {
		return fmt.Errorf("create permission %s: %w", name, err)
	}
	return p.permissionsDone(w, r, "Created "+name+".")
}

func (p *Panel) permissionDelete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	perm, err := p.auth.Repo.PermissionGetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NewHTTPError(http.StatusNotFound, "That permission doesn't exist.")
		}
		return fmt.Errorf("load permission %s: %w", id, err)
	}
	if err := p.auth.Service.PermissionDelete(ctx, perm.ID); err != nil {
		return fmt.Errorf("delete permission %s: %w", id, err)
	}
	return p.permissionsDone(w, r, "Deleted "+perm.Name+".")
}
