package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRoles(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	roles := srv.URL + "/admin/roles"
	roleIDOf := func(name string) string {
		t.Helper()
		r, err := auth.Repo.RoleGetByName(ctx, name)
		if err != nil {
			t.Fatalf("find role %s: %v", name, err)
		}
		return r.ID
	}
	hasRole := func(name string) bool { _, err := auth.Repo.RoleGetByName(ctx, name); return err == nil }

	t.Run("page lists roles and marks the one that opens the panel", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodGet, roles, nil, false)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "<html", `id="roles-region"`, "admin", "Opens this panel", `href="/admin/roles"`, `href="/admin/permissions"`)
		mustNotContain(t, body, "Delete role") // the admin role can't be deleted
	})

	t.Run("create", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodPost, roles, url.Values{"name": {" support "}, "description": {"Answers tickets"}}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, `id="roles-region"`, "support", "Answers tickets", "Created support.")
		mustNotContain(t, body, "<html")
		if !hasRole("support") {
			t.Fatal("role was not created")
		}

		code, _, h := do(t, c, http.MethodPost, roles, url.Values{"name": {"billing"}}, false)
		if code != http.StatusSeeOther || h.Get("Location") != "/admin/roles" {
			t.Errorf("plain post: status %d, Location %q; want 303 to the list", code, h.Get("Location"))
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodPost, roles, url.Values{"name": {"support"}}, true)
		if code != http.StatusConflict {
			t.Fatalf("status %d, want 409", code)
		}
		mustContain(t, body, "already exists")
	})

	for name, form := range map[string]url.Values{
		"empty name":       {"name": {""}},
		"one character":    {"name": {"a"}},
		"with a space":     {"name": {"bad name"}},
		"markup":           {"name": {"<script>x</script>"}},
		"starts with dash": {"name": {"-admin"}},
		"too long":         {"name": {strings.Repeat("a", 51)}},
		"long description": {"name": {"ok-name"}, "description": {strings.Repeat("d", 201)}},
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			code, _, _ := do(t, c, http.MethodPost, roles, form, true)
			if code != http.StatusBadRequest {
				t.Errorf("status %d, want 400", code)
			}
			if hasRole(form.Get("name")) && form.Get("name") != "" {
				t.Errorf("role %q was created", form.Get("name"))
			}
		})
	}

	t.Run("escapes descriptions", func(t *testing.T) {
		do(t, c, http.MethodPost, roles, url.Values{"name": {"sneaky"}, "description": {`<img src=x onerror=alert(1)>`}}, true)
		_, body, _ := do(t, c, http.MethodGet, roles, nil, false)
		mustContain(t, body, "sneaky")
		mustNotContain(t, body, "<img src=x onerror")
	})

	t.Run("delete removes the role from the users who held it", func(t *testing.T) {
		newUser(t, auth, "alice@example.com", "billing")
		alice := userID(t, auth, "alice@example.com")
		code, body, _ := do(t, c, http.MethodPost, roles+"/"+roleIDOf("billing")+"/delete", url.Values{}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "Deleted billing.")
		if hasRole("billing") {
			t.Error("role still exists")
		}
		if ok, _ := auth.UserHasRole(ctx, alice, "billing"); ok {
			t.Error("user still holds the deleted role")
		}
	})

	t.Run("the role that opens the panel can't be deleted", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodPost, roles+"/"+roleIDOf("admin")+"/delete", url.Values{}, true)
		if code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400", code)
		}
		mustContain(t, body, "lock every admin out")
		if !hasRole("admin") {
			t.Fatal("the admin role was deleted")
		}
		// And the admin can still use the panel afterwards.
		if code, _, _ := do(t, c, http.MethodGet, roles, nil, false); code != http.StatusOK {
			t.Errorf("panel closed after the attempt: status %d", code)
		}
	})

	t.Run("unknown role", func(t *testing.T) {
		for _, path := range []string{"/nope/delete", "/nope/permissions/grant", "/nope/permissions/revoke"} {
			code, _, _ := do(t, c, http.MethodPost, roles+path, url.Values{"permission": {"x"}}, true)
			if code != http.StatusNotFound {
				t.Errorf("POST %s: status %d, want 404", path, code)
			}
		}
	})
}

// cardOf returns the HTML of one role's card out of the roles region.
func cardOf(t *testing.T, body, roleID string) string {
	t.Helper()
	start := strings.Index(body, `id="role-`+roleID+`"`)
	if start < 0 {
		t.Fatalf("no card for role %s", roleID)
	}
	end := strings.Index(body[start:], "</article>")
	if end < 0 {
		t.Fatalf("card for role %s is not closed", roleID)
	}
	return body[start : start+end]
}

func TestRolePermissions(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	if _, err := auth.RoleCreate(ctx, "support", "Support staff"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"tickets.read", "tickets.write"} {
		if _, err := auth.PermissionCreate(ctx, p, p); err != nil {
			t.Fatal(err)
		}
	}
	role, err := auth.Repo.RoleGetByName(ctx, "support")
	if err != nil {
		t.Fatal(err)
	}
	base := srv.URL + "/admin/roles/" + role.ID + "/permissions"
	newUser(t, auth, "alice@example.com", "support")
	alicePage := srv.URL + "/admin/users/" + userID(t, auth, "alice@example.com") + "?tab=roles"

	t.Run("grant", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodPost, base+"/grant", url.Values{"permission": {"tickets.read"}}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "Added tickets.read to support.", "Remove tickets.read from support")
		// Within this role's card, a held permission is no longer offered and a
		// free one still is. (Other roles still offer both.)
		card := cardOf(t, body, role.ID)
		mustContain(t, card, `<option value="tickets.write">`)
		mustNotContain(t, card, `<option value="tickets.read">`)

		// It shows up for a user who holds the role.
		_, page, _ := do(t, c, http.MethodGet, alicePage, nil, false)
		mustContain(t, page, "tickets.read")
		mustNotContain(t, page, "tickets.write")
	})

	t.Run("revoke", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodPost, base+"/revoke", url.Values{"permission": {"tickets.read"}}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "Removed tickets.read from support.")
		_, page, _ := do(t, c, http.MethodGet, alicePage, nil, false)
		mustContain(t, page, "No permissions")
	})

	t.Run("unknown permission", func(t *testing.T) {
		for _, path := range []string{"/grant", "/revoke"} {
			code, _, _ := do(t, c, http.MethodPost, base+path, url.Values{"permission": {"nope.never"}}, true)
			if code != http.StatusBadRequest {
				t.Errorf("%s: status %d, want 400", path, code)
			}
		}
	})
}

func TestPermissions(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	perms := srv.URL + "/admin/permissions"
	exists := func(name string) bool { _, err := auth.Repo.PermissionGetByName(ctx, name); return err == nil }

	t.Run("empty state", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodGet, perms, nil, false)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "No permissions yet", `id="permissions-region"`)
	})

	t.Run("create, listed in order", func(t *testing.T) {
		do(t, c, http.MethodPost, perms, url.Values{"name": {"invoices.write"}}, true)
		code, body, _ := do(t, c, http.MethodPost, perms, url.Values{"name": {"invoices.read"}, "description": {"View invoices"}}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "Created invoices.read.", "View invoices")
		if r, w := strings.Index(body, "invoices.read"), strings.Index(body, "invoices.write"); r < 0 || w < 0 || r > w {
			t.Errorf("permissions are not sorted by name (read at %d, write at %d)", r, w)
		}
	})

	t.Run("duplicate and invalid names", func(t *testing.T) {
		code, _, _ := do(t, c, http.MethodPost, perms, url.Values{"name": {"invoices.read"}}, true)
		if code != http.StatusConflict {
			t.Errorf("duplicate: status %d, want 409", code)
		}
		for _, bad := range []string{"", "x", "has space", "<b>", strings.Repeat("a", 51)} {
			code, _, _ := do(t, c, http.MethodPost, perms, url.Values{"name": {bad}}, true)
			if code != http.StatusBadRequest {
				t.Errorf("name %q: status %d, want 400", bad, code)
			}
		}
	})

	t.Run("delete also removes it from roles", func(t *testing.T) {
		if _, err := auth.RoleCreate(ctx, "finance", ""); err != nil {
			t.Fatal(err)
		}
		if err := auth.RolePermissionGrant(ctx, "finance", "invoices.read"); err != nil {
			t.Fatal(err)
		}
		p, err := auth.Repo.PermissionGetByName(ctx, "invoices.read")
		if err != nil {
			t.Fatal(err)
		}
		code, body, _ := do(t, c, http.MethodPost, perms+"/"+p.ID+"/delete", url.Values{}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "Deleted invoices.read.")
		if exists("invoices.read") {
			t.Error("permission still exists")
		}
		_, rolesPage, _ := do(t, c, http.MethodGet, srv.URL+"/admin/roles", nil, false)
		mustNotContain(t, rolesPage, "invoices.read")
	})

	t.Run("unknown permission", func(t *testing.T) {
		code, _, _ := do(t, c, http.MethodPost, perms+"/nope/delete", url.Values{}, true)
		if code != http.StatusNotFound {
			t.Errorf("status %d, want 404", code)
		}
	})
}

// Roles and permissions decide who can do what, so only admins may touch them.
func TestRolesPermissions_AdminOnly(t *testing.T) {
	auth := newAuth(t)
	newUser(t, auth, "member@example.com", "")
	newUser(t, auth, "admin@example.com", "admin")
	srv := newServer(t, auth)
	c := client(t)
	login(t, c, srv, "member@example.com")

	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, "/admin/roles"},
		{http.MethodPost, "/admin/roles"},
		{http.MethodPost, "/admin/roles/x/delete"},
		{http.MethodPost, "/admin/roles/x/permissions/grant"},
		{http.MethodGet, "/admin/permissions"},
		{http.MethodPost, "/admin/permissions"},
		{http.MethodPost, "/admin/permissions/x/delete"},
	} {
		code, _, _ := do(t, c, tt.method, srv.URL+tt.path, url.Values{"name": {"hacker"}}, true)
		if code != http.StatusForbidden {
			t.Errorf("%s %s as a member: status %d, want 403", tt.method, tt.path, code)
		}
	}
	if _, err := auth.Repo.RoleGetByName(context.Background(), "hacker"); err == nil {
		t.Error("a member created a role")
	}
}
