package admin_test

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/josuebrunel/ezauth"
	"github.com/josuebrunel/ezauth/pkg/db/models"
	"github.com/josuebrunel/ezauth/pkg/service"
)

// adminSession returns an auth, a server and a client already logged in as an
// admin (admin@example.com).
func adminSession(t *testing.T) (*ezauth.EzAuth, *httptest.Server, *http.Client) {
	t.Helper()
	auth := newAuth(t)
	newUser(t, auth, "admin@example.com", "admin")
	srv := newServer(t, auth)
	c := client(t)
	login(t, c, srv, "admin@example.com")
	return auth, srv, c
}

func userID(t *testing.T, auth *ezauth.EzAuth, email string) string {
	t.Helper()
	u, err := auth.Repo.UserGetByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("find %s: %v", email, err)
	}
	return u.ID
}

func getUser(t *testing.T, auth *ezauth.EzAuth, id string) *models.User {
	t.Helper()
	u, err := auth.Repo.UserGetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("load user %s: %v", id, err)
	}
	return u
}

// do sends a request and returns the status and body. htmx adds the header
// htmx sends, so the handler answers with a fragment.
func do(t *testing.T, c *http.Client, method, url string, form url.Values, htmx bool) (int, string, http.Header) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b), resp.Header
}

// mustContain checks the rendered text, so HTML escapes like &#39; are undone.
func mustContain(t *testing.T, body string, subs ...string) {
	t.Helper()
	body = html.UnescapeString(body)
	for _, s := range subs {
		if !strings.Contains(body, s) {
			t.Errorf("response does not contain %q", s)
		}
	}
}

func mustNotContain(t *testing.T, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if strings.Contains(body, s) {
			t.Errorf("response unexpectedly contains %q", s)
		}
	}
}

func TestUsersList(t *testing.T) {
	auth, srv, c := adminSession(t)
	for i := 1; i <= 12; i++ {
		newUser(t, auth, fmt.Sprintf("member%02d@example.com", i), "")
	}
	suspended := userID(t, auth, "member03@example.com")
	if _, err := auth.Service.UserSuspend(context.Background(), suspended); err != nil {
		t.Fatal(err)
	}
	list := srv.URL + "/admin/users"

	t.Run("full page", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodGet, list, nil, false)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "<html", `id="user-filters"`, `id="users-results"`)
	})

	t.Run("htmx request gets only the results fragment", func(t *testing.T) {
		code, body, h := do(t, c, http.MethodGet, list+"?q=member05", nil, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, `id="users-results"`, "member05@example.com")
		mustNotContain(t, body, "<html", "member06@example.com")
		if vary := strings.Join(h.Values("Vary"), ","); !strings.Contains(vary, "HX-Request") {
			t.Errorf("Vary = %q, want HX-Request so caches keep page and fragment apart", vary)
		}
	})

	t.Run("pagination keeps the filters", func(t *testing.T) {
		// 13 accounts (12 members and the admin), 10 per page.
		_, p1, _ := do(t, c, http.MethodGet, list, nil, false)
		mustContain(t, p1, "Showing 1 to 10", `href="/admin/users?page=2"`)
		_, p2, _ := do(t, c, http.MethodGet, list+"?page=2", nil, false)
		mustContain(t, p2, "Showing 11 to 13", `href="/admin/users"`)
		mustNotContain(t, p2, `href="/admin/users?page=3"`)
	})

	t.Run("status filter", func(t *testing.T) {
		_, body, _ := do(t, c, http.MethodGet, list+"?status=suspended", nil, false)
		mustContain(t, body, "member03@example.com", "Suspended")
		mustNotContain(t, body, "member04@example.com")
	})

	t.Run("no match shows an empty state", func(t *testing.T) {
		_, body, _ := do(t, c, http.MethodGet, list+"?q=nobody-by-that-name", nil, false)
		mustContain(t, body, "No users match that search")
	})

	for name, qs := range map[string]string{
		"unknown status":   "?status=banana",
		"page zero":        "?page=0",
		"page not numeric": "?page=abc",
		"page too large":   "?page=999999",
		"search too long":  "?q=" + strings.Repeat("a", 101),
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			code, _, _ := do(t, c, http.MethodGet, list+qs, nil, false)
			if code != http.StatusBadRequest {
				t.Errorf("status %d, want 400", code)
			}
		})
	}

	t.Run("escapes user data", func(t *testing.T) {
		if _, err := auth.Service.UserCreate(context.Background(), &service.RequestBasicAuth{
			Email: "mallory@example.com", Password: testPassword, FirstName: `<img src=x onerror=alert(1)>`,
		}); err != nil {
			t.Fatal(err)
		}
		_, body, _ := do(t, c, http.MethodGet, list+"?q=mallory", nil, false)
		mustContain(t, body, "mallory@example.com")
		mustNotContain(t, body, "<img src=x onerror")
	})
}

func TestUserDetail(t *testing.T) {
	auth, srv, c := adminSession(t)
	newUser(t, auth, "alice@example.com", "")
	id := userID(t, auth, "alice@example.com")
	base := srv.URL + "/admin/users/" + id

	for _, tt := range []struct {
		name, path string
		want       int
		contains   []string
	}{
		{"overview", base, 200, []string{"alice@example.com", "Admin notes", "Impersonate", "Suspend"}},
		{"roles tab", base + "?tab=roles", 200, []string{"Effective permissions", "Grant role"}},
		{"activity tab", base + "?tab=activity", 200, []string{"Audit log", "Sessions and tokens"}},
		{"unknown tab", base + "?tab=nope", 400, nil},
		{"unknown user", srv.URL + "/admin/users/does-not-exist", 404, []string{"That user doesn't exist."}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, body, _ := do(t, c, http.MethodGet, tt.path, nil, false)
			if code != tt.want {
				t.Fatalf("status %d, want %d", code, tt.want)
			}
			mustContain(t, body, tt.contains...)
		})
	}

	t.Run("own account has no suspend or impersonate", func(t *testing.T) {
		_, body, _ := do(t, c, http.MethodGet, srv.URL+"/admin/users/"+userID(t, auth, "admin@example.com"), nil, false)
		mustContain(t, body, "This is your account")
		mustNotContain(t, body, ">Impersonate<", ">Suspend<")
	})
}

func TestUserSuspendReactivate(t *testing.T) {
	auth, srv, c := adminSession(t)
	newUser(t, auth, "alice@example.com", "")
	id := userID(t, auth, "alice@example.com")
	base := srv.URL + "/admin/users/" + id

	code, body, _ := do(t, c, http.MethodPost, base+"/suspend", url.Values{}, true)
	if code != http.StatusOK {
		t.Fatalf("suspend: status %d", code)
	}
	mustContain(t, body, `id="user-header"`, "Suspended", "Reactivate", "Account suspended.")
	if getUser(t, auth, id).IsActive {
		t.Error("user is still active after suspend")
	}

	code, body, _ = do(t, c, http.MethodPost, base+"/reactivate", url.Values{}, true)
	if code != http.StatusOK {
		t.Fatalf("reactivate: status %d", code)
	}
	mustContain(t, body, "Active", "Account reactivated.")
	if !getUser(t, auth, id).IsActive {
		t.Error("user is not active after reactivate")
	}

	t.Run("plain form post redirects back", func(t *testing.T) {
		code, _, h := do(t, c, http.MethodPost, base+"/suspend", url.Values{}, false)
		if code != http.StatusSeeOther || h.Get("Location") != "/admin/users/"+id {
			t.Errorf("status %d, Location %q; want 303 to the user page", code, h.Get("Location"))
		}
	})

	t.Run("can't suspend yourself", func(t *testing.T) {
		me := userID(t, auth, "admin@example.com")
		code, body, _ := do(t, c, http.MethodPost, srv.URL+"/admin/users/"+me+"/suspend", url.Values{}, true)
		if code != http.StatusBadRequest {
			t.Errorf("status %d, want 400", code)
		}
		mustContain(t, body, "can't suspend your own account")
		if !getUser(t, auth, me).IsActive {
			t.Error("admin was suspended")
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		code, _, _ := do(t, c, http.MethodPost, srv.URL+"/admin/users/nope/suspend", url.Values{}, true)
		if code != http.StatusNotFound {
			t.Errorf("status %d, want 404", code)
		}
	})
}

// ezauth's user update writes every column, so saving a note must not undo
// a suspension (or flip any other flag) on the way.
func TestUserNotes_DoesNotClobberOtherFields(t *testing.T) {
	auth, srv, c := adminSession(t)
	newUser(t, auth, "alice@example.com", "")
	id := userID(t, auth, "alice@example.com")
	ctx := context.Background()
	u := getUser(t, auth, id)
	u.EmailVerified = true
	if _, err := auth.Service.UserUpdate(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Service.UserSuspend(ctx, id); err != nil {
		t.Fatal(err)
	}
	before := getUser(t, auth, id)

	code, body, _ := do(t, c, http.MethodPost, srv.URL+"/admin/users/"+id+"/notes", url.Values{"notes": {"  refund asked on Oct 3  "}}, true)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	mustContain(t, body, "Note saved.")

	after := getUser(t, auth, id)
	if got, _ := models.GetAppMeta[string](after, "notes"); got != "refund asked on Oct 3" {
		t.Errorf("notes = %q, want the trimmed note", got)
	}
	if after.IsActive != before.IsActive || after.EmailVerified != before.EmailVerified ||
		after.PhoneVerified != before.PhoneVerified || after.MfaEnabled != before.MfaEnabled {
		t.Errorf("saving a note changed flags: before active=%t email=%t phone=%t mfa=%t, after active=%t email=%t phone=%t mfa=%t",
			before.IsActive, before.EmailVerified, before.PhoneVerified, before.MfaEnabled,
			after.IsActive, after.EmailVerified, after.PhoneVerified, after.MfaEnabled)
	}
	if after.IsActive {
		t.Error("suspended user became active")
	}

	t.Run("note is shown on the page", func(t *testing.T) {
		_, page, _ := do(t, c, http.MethodGet, srv.URL+"/admin/users/"+id, nil, false)
		mustContain(t, page, "refund asked on Oct 3")
	})

	t.Run("rejects a huge note", func(t *testing.T) {
		code, _, _ := do(t, c, http.MethodPost, srv.URL+"/admin/users/"+id+"/notes", url.Values{"notes": {strings.Repeat("x", 2001)}}, true)
		if code != http.StatusBadRequest {
			t.Errorf("status %d, want 400", code)
		}
	})
}

func TestUserRoles(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	newUser(t, auth, "alice@example.com", "")
	id := userID(t, auth, "alice@example.com")
	base := srv.URL + "/admin/users/" + id

	if _, err := auth.RoleCreate(ctx, "support", "Support staff"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.PermissionCreate(ctx, "tickets.read", "Read tickets"); err != nil {
		t.Fatal(err)
	}
	if err := auth.RolePermissionGrant(ctx, "support", "tickets.read"); err != nil {
		t.Fatal(err)
	}

	code, body, _ := do(t, c, http.MethodPost, base+"/roles/grant", url.Values{"role": {"support"}}, true)
	if code != http.StatusOK {
		t.Fatalf("grant: status %d", code)
	}
	mustContain(t, body, `id="roles-panel"`, "support", "tickets.read", "Granted support.")
	if ok, err := auth.UserHasRole(ctx, id, "support"); err != nil || !ok {
		t.Errorf("user does not hold support after grant (err=%v)", err)
	}

	t.Run("revoke", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodPost, base+"/roles/revoke", url.Values{"role": {"support"}}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "Revoked support.", "No roles granted yet.")
		if ok, _ := auth.UserHasRole(ctx, id, "support"); ok {
			t.Error("user still holds support after revoke")
		}
	})

	t.Run("can't grant a role that doesn't exist", func(t *testing.T) {
		code, _, _ := do(t, c, http.MethodPost, base+"/roles/grant", url.Values{"role": {"wizard"}}, true)
		if code != http.StatusBadRequest {
			t.Errorf("status %d, want 400", code)
		}
	})

	t.Run("can't revoke your own admin role", func(t *testing.T) {
		me := userID(t, auth, "admin@example.com")
		code, body, _ := do(t, c, http.MethodPost, srv.URL+"/admin/users/"+me+"/roles/revoke", url.Values{"role": {"admin"}}, true)
		if code != http.StatusBadRequest {
			t.Errorf("status %d, want 400", code)
		}
		mustContain(t, body, "can't revoke your own admin role")
		if ok, _ := auth.UserHasRole(ctx, me, "admin"); !ok {
			t.Error("admin lost their own role")
		}
	})
}

func TestImpersonation(t *testing.T) {
	auth, srv, c := adminSession(t)
	newUser(t, auth, "alice@example.com", "")
	id := userID(t, auth, "alice@example.com")

	t.Run("can't impersonate yourself", func(t *testing.T) {
		code, _, _ := do(t, c, http.MethodPost, srv.URL+"/admin/users/"+userID(t, auth, "admin@example.com")+"/impersonate", url.Values{}, false)
		if code != http.StatusBadRequest {
			t.Errorf("status %d, want 400", code)
		}
	})

	code, _, _ := do(t, c, http.MethodPost, srv.URL+"/admin/users/"+id+"/impersonate", url.Values{}, false)
	if code != http.StatusFound && code != http.StatusSeeOther {
		t.Fatalf("impersonate: status %d, want a redirect", code)
	}

	// The session user is now alice, who isn't an admin: the panel is closed
	// except for the page that explains why.
	code, body, _ := do(t, c, http.MethodGet, srv.URL+"/admin/users", nil, false)
	if code != http.StatusForbidden {
		t.Fatalf("panel while impersonating: status %d, want 403", code)
	}
	mustContain(t, body, "You're impersonating a user", "Stop impersonating")

	code, _, _ = do(t, c, http.MethodPost, srv.URL+"/admin/impersonate/stop", url.Values{}, false)
	if code != http.StatusFound && code != http.StatusSeeOther {
		t.Fatalf("stop: status %d, want a redirect", code)
	}
	code, _, _ = do(t, c, http.MethodGet, srv.URL+"/admin/users", nil, false)
	if code != http.StatusOK {
		t.Errorf("panel after stopping: status %d, want 200", code)
	}
}
