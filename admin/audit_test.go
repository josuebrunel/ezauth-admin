package admin_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/josuebrunel/ezauth/pkg/db/models"
)

func TestAuditLog(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	newUser(t, auth, "alice@example.com", "")
	alice := userID(t, auth, "alice@example.com")
	audit := srv.URL + "/admin/audit"

	record := func(id *string, typ string, meta models.JSONMap, at time.Time) {
		t.Helper()
		if _, err := auth.Repo.AuditLogCreate(ctx, &models.AuditLog{UserID: id, EventType: typ, Metadata: meta, CreatedAt: at}); err != nil {
			t.Fatalf("AuditLogCreate failed: %v", err)
		}
	}
	base := time.Now().UTC().Add(-time.Hour)
	record(&alice, models.AuditEventLoginFailed, models.JSONMap{"reason": "invalid_password"}, base)
	record(&alice, models.AuditEventRoleGranted, models.JSONMap{"role": "support", "actor_id": "someone"}, base.Add(time.Minute))
	record(nil, models.AuditEventUserDeleted, models.JSONMap{"email": "bob@example.com", "user_id": "gone"}, base.Add(2*time.Minute))

	t.Run("lists events newest first, with who and details", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodGet, audit, nil, false)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "<html", `id="audit-results"`, "login.failed", "invalid_password", "role.granted", "support", "alice@example.com")
		// Newest first: the deletion (+2m), then the grant (+1m), then the failed
		// login. Each is found by something only it carries.
		del, grant, failed := strings.Index(body, "bob@example.com"), strings.Index(body, "someone"), strings.Index(body, "invalid_password")
		if del < 0 || grant < 0 || failed < 0 || del > grant || grant > failed {
			t.Errorf("not newest first (deleted at %d, granted at %d, failed login at %d)", del, grant, failed)
		}
		// An event whose account is gone is named from its own details.
		mustContain(t, body, "bob@example.com", "(deleted)")
		// A live account links to its page.
		mustContain(t, body, `href="/admin/users/`+alice+`"`)
	})

	t.Run("filters by event type, as a fragment for htmx", func(t *testing.T) {
		code, body, h := do(t, c, http.MethodGet, audit+"?event=login.failed", nil, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, `id="audit-results"`, "invalid_password")
		mustNotContain(t, body, "<html", "role.granted")
		if vary := strings.Join(h.Values("Vary"), ","); !strings.Contains(vary, "HX-Request") {
			t.Errorf("Vary = %q, want HX-Request", vary)
		}
	})

	t.Run("rejects an unknown event type and bad pages", func(t *testing.T) {
		for _, qs := range []string{"?event=login.failed%27%20OR%201=1", "?event=nope", "?page=0", "?page=x", "?page=999999"} {
			if code, _, _ := do(t, c, http.MethodGet, audit+qs, nil, false); code != http.StatusBadRequest {
				t.Errorf("%s: status %d, want 400", qs, code)
			}
		}
	})

	t.Run("escapes event details", func(t *testing.T) {
		record(&alice, models.AuditEventLoginFailed, models.JSONMap{"reason": `<img src=x onerror=alert(1)>`}, time.Now().UTC())
		_, body, _ := do(t, c, http.MethodGet, audit, nil, false)
		mustNotContain(t, body, "<img src=x onerror")
	})

	t.Run("admin only", func(t *testing.T) {
		newUser(t, auth, "member@example.com", "")
		m := client(t)
		login(t, m, srv, "member@example.com")
		if code, _, _ := do(t, m, http.MethodGet, audit, nil, false); code != http.StatusForbidden {
			t.Errorf("status %d, want 403", code)
		}
	})
}

func TestAuditLog_Pagination(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	newUser(t, auth, "alice@example.com", "")
	alice := userID(t, auth, "alice@example.com")
	typ := models.AuditEventLoginFailed
	base := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < 30; i++ {
		if _, err := auth.Repo.AuditLogCreate(ctx, &models.AuditLog{
			UserID: &alice, EventType: typ, Metadata: models.JSONMap{"n": fmt.Sprintf("event-%02d", i)}, CreatedAt: base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	list := srv.URL + "/admin/audit?event=" + url.QueryEscape(typ)

	_, p1, _ := do(t, c, http.MethodGet, list, nil, false)
	mustContain(t, p1, "Showing 1 to 25", "event-29", `href="/admin/audit?event=login.failed&page=2"`)
	mustNotContain(t, p1, "event-04")

	_, p2, _ := do(t, c, http.MethodGet, list+"&page=2", nil, false)
	mustContain(t, p2, "Showing 26 to 30", "event-04", "event-00")
	mustNotContain(t, p2, "event-29", `page=3`)
}
