package admin_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/josuebrunel/ezauth/pkg/db/models"
	"github.com/josuebrunel/ezauth/pkg/service"
)

func TestUserEdit(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	newUser(t, auth, "alice@example.com", "")
	newUser(t, auth, "bob@example.com", "")
	id := userID(t, auth, "alice@example.com")
	base := srv.URL + "/admin/users/" + id

	t.Run("form is prefilled", func(t *testing.T) {
		u := getUser(t, auth, id)
		u.FirstName, u.Timezone = "Alice", "Europe/Paris"
		if _, err := auth.Service.UserUpdate(ctx, u); err != nil {
			t.Fatal(err)
		}
		code, body, _ := do(t, c, http.MethodGet, base+"/edit", nil, false)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, `value="Alice"`, `value="Europe/Paris"`, "Edit profile")
	})

	t.Run("saves edits and leaves every other column alone", func(t *testing.T) {
		// A suspended, verified account: the update must not undo either.
		u := getUser(t, auth, id)
		u.EmailVerified = true
		if _, err := auth.Service.UserUpdate(ctx, u); err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Service.UserSuspend(ctx, id); err != nil {
			t.Fatal(err)
		}
		before := getUser(t, auth, id)

		code, _, h := do(t, c, http.MethodPost, base+"/edit", url.Values{
			"first_name": {" Alicia "}, "last_name": {"Martin"}, "username": {"alicia_m"},
			"phone": {"+33612345678"}, "locale": {"fr"}, "timezone": {"Europe/Paris"},
		}, false)
		if code != http.StatusSeeOther || h.Get("Location") != "/admin/users/"+id+"?notice=saved" {
			t.Fatalf("status %d, Location %q; want 303 back to the user with a notice", code, h.Get("Location"))
		}

		after := getUser(t, auth, id)
		if after.FirstName != "Alicia" || after.LastName != "Martin" || after.Username != "alicia_m" ||
			after.Phone != "+33612345678" || after.Locale != "fr" || after.Timezone != "Europe/Paris" {
			t.Errorf("fields not saved as typed: %+v", after)
		}
		if after.IsActive != before.IsActive || after.EmailVerified != before.EmailVerified ||
			after.PhoneVerified != before.PhoneVerified || after.MfaEnabled != before.MfaEnabled {
			t.Errorf("editing the profile changed flags: before active=%t email=%t phone=%t mfa=%t, after active=%t email=%t phone=%t mfa=%t",
				before.IsActive, before.EmailVerified, before.PhoneVerified, before.MfaEnabled,
				after.IsActive, after.EmailVerified, after.PhoneVerified, after.MfaEnabled)
		}
		if after.IsActive || !after.EmailVerified {
			t.Errorf("expected still suspended and verified, got active=%t verified=%t", after.IsActive, after.EmailVerified)
		}
		if after.Email != "alice@example.com" {
			t.Errorf("email changed to %q", after.Email)
		}

		_, page, _ := do(t, c, http.MethodGet, base+"?notice=saved", nil, false)
		mustContain(t, page, "Profile saved.", "Alicia Martin")
	})

	t.Run("rejects invalid input and saves nothing", func(t *testing.T) {
		before := getUser(t, auth, id)
		code, body, _ := do(t, c, http.MethodPost, base+"/edit", url.Values{
			"first_name": {"Mallory"}, "username": {"no spaces allowed"}, "phone": {"nope"},
		}, false)
		if code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d, want 422", code)
		}
		mustContain(t, body, "Use 3 to 30 letters", "Use international format", `value="Mallory"`)
		if after := getUser(t, auth, id); after.FirstName != before.FirstName {
			t.Errorf("first name changed to %q despite invalid input", after.FirstName)
		}
	})

	t.Run("duplicate phone is a friendly error", func(t *testing.T) {
		bob := getUser(t, auth, userID(t, auth, "bob@example.com"))
		bob.Phone = "+33699999999"
		if _, err := auth.Service.UserUpdate(ctx, bob); err != nil {
			t.Fatal(err)
		}
		code, body, _ := do(t, c, http.MethodPost, base+"/edit", url.Values{"phone": {"+33699999999"}}, false)
		if code != http.StatusConflict {
			t.Fatalf("status %d, want 409", code)
		}
		mustContain(t, body, "Another user already has that username or phone number.")
	})

	t.Run("unknown user", func(t *testing.T) {
		code, _, _ := do(t, c, http.MethodGet, srv.URL+"/admin/users/nope/edit", nil, false)
		if code != http.StatusNotFound {
			t.Errorf("GET status %d, want 404", code)
		}
		code, _, _ = do(t, c, http.MethodPost, srv.URL+"/admin/users/nope/edit", url.Values{}, false)
		if code != http.StatusNotFound {
			t.Errorf("POST status %d, want 404", code)
		}
	})

	t.Run("a made-up notice is ignored", func(t *testing.T) {
		_, body, _ := do(t, c, http.MethodGet, base+"?notice=<b>pwned</b>", nil, false)
		mustNotContain(t, body, "pwned")
	})
}

// recordingHook records the delete and update hooks, and can veto them.
type recordingHook struct {
	service.DefaultHook
	mu            sync.Mutex
	beforeDeleted []string
	afterDeleted  []string
	afterUpdated  []string
	vetoDelete    bool
	vetoUpdate    bool
}

func (h *recordingHook) BeforeUserDeleted(_ context.Context, u *models.User) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.beforeDeleted = append(h.beforeDeleted, u.Email)
	if h.vetoDelete {
		return errors.New("secret internal reason: contract in force")
	}
	return nil
}

func (h *recordingHook) AfterUserDeleted(_ context.Context, u *models.User) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.afterDeleted = append(h.afterDeleted, u.Email)
	return nil
}

func (h *recordingHook) BeforeUserUpdated(context.Context, *models.User) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.vetoUpdate {
		return errors.New("secret internal reason: record is locked")
	}
	return nil
}

func (h *recordingHook) AfterUserUpdated(_ context.Context, u *models.User) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.afterUpdated = append(h.afterUpdated, u.Email)
	return nil
}

func TestUserDelete(t *testing.T) {
	auth, srv, c := adminSession(t)
	hook := &recordingHook{}
	auth.SetHook(hook)
	ctx := context.Background()
	newUser(t, auth, "alice@example.com", "")
	newUser(t, auth, "bob@example.com", "")
	alice := userID(t, auth, "alice@example.com")
	bob := userID(t, auth, "bob@example.com")
	exists := func(id string) bool { _, err := auth.Repo.UserGetByID(ctx, id); return err == nil }

	t.Run("needs the typed email", func(t *testing.T) {
		for name, confirm := range map[string]string{"empty": "", "wrong": "bob@example.com", "partial": "alice@"} {
			code, body, _ := do(t, c, http.MethodPost, srv.URL+"/admin/users/"+alice+"/delete", url.Values{"confirm": {confirm}}, false)
			if code != http.StatusBadRequest {
				t.Errorf("%s: status %d, want 400", name, code)
			}
			mustContain(t, body, "doesn't match")
		}
		if !exists(alice) {
			t.Fatal("user was deleted without a matching confirmation")
		}
		if len(hook.beforeDeleted) != 0 {
			t.Errorf("delete hooks ran for a rejected request: %v", hook.beforeDeleted)
		}
	})

	t.Run("a hook can veto, and its message stays private", func(t *testing.T) {
		hook.vetoDelete = true
		defer func() { hook.vetoDelete = false }()
		code, body, _ := do(t, c, http.MethodPost, srv.URL+"/admin/users/"+alice+"/delete", url.Values{"confirm": {"alice@example.com"}}, false)
		if code != http.StatusConflict {
			t.Fatalf("status %d, want 409", code)
		}
		mustContain(t, body, "An app rule blocked this deletion.")
		mustNotContain(t, body, "secret internal reason")
		if !exists(alice) {
			t.Error("user was deleted despite the veto")
		}
	})

	t.Run("deletes, runs both hooks and shows a notice", func(t *testing.T) {
		var logs bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
		defer slog.SetDefault(prev)

		code, _, h := do(t, c, http.MethodPost, srv.URL+"/admin/users/"+bob+"/delete", url.Values{"confirm": {"  BOB@example.com "}}, false)
		if code != http.StatusSeeOther || h.Get("Location") != "/admin/users?notice=deleted" {
			t.Fatalf("status %d, Location %q; want 303 to the list with a notice", code, h.Get("Location"))
		}
		if exists(bob) {
			t.Error("user still exists")
		}
		if got := strings.Join(hook.afterDeleted, ","); got != "bob@example.com" {
			t.Errorf("AfterUserDeleted ran for %q, want bob@example.com", got)
		}
		// With no audit row possible, the log line is the record: who and whom.
		me := userID(t, auth, "admin@example.com")
		for _, want := range []string{"user deleted", "user_id=" + bob, "deleted_by=" + me} {
			if !strings.Contains(logs.String(), want) {
				t.Errorf("log does not contain %q:\n%s", want, logs.String())
			}
		}
		_, page, _ := do(t, c, http.MethodGet, srv.URL+"/admin/users?notice=deleted", nil, false)
		mustContain(t, page, "User deleted.")
		mustNotContain(t, page, "bob@example.com")
	})

	t.Run("can't delete yourself", func(t *testing.T) {
		me := userID(t, auth, "admin@example.com")
		code, _, _ := do(t, c, http.MethodPost, srv.URL+"/admin/users/"+me+"/delete", url.Values{"confirm": {"admin@example.com"}}, false)
		if code != http.StatusBadRequest {
			t.Errorf("status %d, want 400", code)
		}
		if !exists(me) {
			t.Error("admin deleted their own account")
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		code, _, _ := do(t, c, http.MethodPost, srv.URL+"/admin/users/nope/delete", url.Values{"confirm": {"x"}}, false)
		if code != http.StatusNotFound {
			t.Errorf("status %d, want 404", code)
		}
	})

	t.Run("detail page offers delete for others, explains for yourself", func(t *testing.T) {
		_, other, _ := do(t, c, http.MethodGet, srv.URL+"/admin/users/"+alice, nil, false)
		mustContain(t, other, `data-dialog-open="delete-dialog"`, `data-confirm-value="alice@example.com"`)
		_, self, _ := do(t, c, http.MethodGet, srv.URL+"/admin/users/"+userID(t, auth, "admin@example.com"), nil, false)
		mustContain(t, self, "You can't delete your own account")
		mustNotContain(t, self, "delete-dialog")
	})
}

func TestUserEdit_Hooks(t *testing.T) {
	auth, srv, c := adminSession(t)
	hook := &recordingHook{}
	auth.SetHook(hook)
	newUser(t, auth, "alice@example.com", "")
	id := userID(t, auth, "alice@example.com")
	edit := srv.URL + "/admin/users/" + id + "/edit"

	code, _, _ := do(t, c, http.MethodPost, edit, url.Values{"first_name": {"Alicia"}}, false)
	if code != http.StatusSeeOther {
		t.Fatalf("status %d", code)
	}
	if len(hook.afterUpdated) != 1 {
		t.Errorf("AfterUserUpdated ran %d times, want 1", len(hook.afterUpdated))
	}

	hook.vetoUpdate = true
	code, body, _ := do(t, c, http.MethodPost, edit, url.Values{"first_name": {"Mallory"}}, false)
	if code != http.StatusConflict {
		t.Fatalf("vetoed update: status %d, want 409", code)
	}
	mustContain(t, body, "An app rule blocked this change.")
	mustNotContain(t, body, "secret internal reason")
	if got := getUser(t, auth, id).FirstName; got != "Alicia" {
		t.Errorf("first name = %q, a vetoed update was saved", got)
	}
}
