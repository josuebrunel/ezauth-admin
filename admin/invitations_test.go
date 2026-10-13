package admin_test

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/josuebrunel/ezauth"
	"github.com/josuebrunel/ezauth/pkg/service"
)

// failingMailer is a mailer whose delivery always fails. It is not the mock
// mailer, so the panel treats it as a real one that broke.
type failingMailer struct{}

func (failingMailer) Send(string, string, string) error { return errors.New("smtp is down") }

// recordingMailer is a working mailer that isn't the mock one, so the panel
// believes the email really went out.
type recordingMailer struct {
	mu   sync.Mutex
	sent []map[string]string
}

func (m *recordingMailer) Send(to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, map[string]string{"to": to, "subject": subject, "body": body})
	return nil
}

var linkTokenRe = regexp.MustCompile(`/invitation/accept\?token=([A-Za-z0-9_\-]+)`)

// linkToken pulls the invitation token out of the "Invitation link" field.
func linkToken(t *testing.T, body string) string {
	t.Helper()
	m := linkTokenRe.FindStringSubmatch(html.UnescapeString(body))
	if m == nil {
		t.Fatal("no invitation link in the response")
	}
	return m[1]
}

func invite(t *testing.T, c *http.Client, srv string, form url.Values, htmx bool) (int, string, http.Header) {
	t.Helper()
	return do(t, c, http.MethodPost, srv+"/admin/invitations", form, htmx)
}

// openInvitations returns the open invitations for one email.
func openInvitations(t *testing.T, auth *ezauth.EzAuth, email string) []service.InvitationInfo {
	t.Helper()
	res, err := auth.Service.InvitationsAll(context.Background(), service.ListInvitationsOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var out []service.InvitationInfo
	for _, inv := range res.Invitations {
		if inv.Email == email {
			out = append(out, inv)
		}
	}
	return out
}

func TestInvitations_InviteAndAccept(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	for _, role := range []string{"support", "billing"} {
		if _, err := auth.RoleCreate(ctx, role, ""); err != nil {
			t.Fatal(err)
		}
	}
	// The admin holds support, but not billing.
	if err := auth.UserRoleGrant(ctx, "", userID(t, auth, "admin@example.com"), "support"); err != nil {
		t.Fatal(err)
	}

	t.Run("the form only offers roles the admin holds", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodGet, srv.URL+"/admin/invitations", nil, false)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "<html", `id="invitations-region"`, `name="role" value="admin"`, `name="role" value="support"`, "No open invitations", `href="/admin/invitations"`)
		mustNotContain(t, body, `value="billing"`)
	})

	var token string
	t.Run("inviting shows the link, and lists the invitation", func(t *testing.T) {
		code, body, _ := invite(t, c, srv.URL, url.Values{"email": {"  New@Example.com "}, "role": {"support"}}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, `id="invitations-region"`, "Invitation for new@example.com", "No mail server is set up", "Invitation created.", "admin@example.com")
		mustNotContain(t, body, "<html")
		token = linkToken(t, body)

		list := openInvitations(t, auth, "new@example.com")
		if len(list) != 1 || list[0].Roles != "support" {
			t.Fatalf("open invitations = %+v, want one with the support role", list)
		}
	})

	t.Run("the invitee follows the link and gets a ready account", func(t *testing.T) {
		if _, err := auth.Service.InvitationPreview(ctx, token); err != nil {
			t.Fatalf("the link does not open the invitation: %v", err)
		}
		// This is EzAuth's own accept route, not the panel's.
		invitee := client(t)
		code, _, h := do(t, invitee, http.MethodPost, srv.URL+"/auth/invitation/accept", url.Values{
			"token": {token}, "password": {"correct-horse-battery"}, "password_confirm": {"correct-horse-battery"},
		}, false)
		if code != http.StatusFound && code != http.StatusSeeOther {
			t.Fatalf("accept: status %d (Location %q), want a redirect", code, h.Get("Location"))
		}
		u, err := auth.Repo.UserGetByEmail(ctx, "new@example.com")
		if err != nil {
			t.Fatalf("the account was not created: %v", err)
		}
		if !u.EmailVerified {
			t.Error("an invited account should have a verified email")
		}
		if ok, _ := auth.UserHasRole(ctx, u.ID, "support"); !ok {
			t.Error("the invited role was not granted")
		}
		if ok, _ := auth.UserHasRole(ctx, u.ID, "admin"); ok {
			t.Error("the invitee became an admin")
		}
	})

	t.Run("an accepted invitation leaves the list", func(t *testing.T) {
		_, body, _ := do(t, c, http.MethodGet, srv.URL+"/admin/invitations", nil, false)
		mustNotContain(t, body, "new@example.com")
		if got := openInvitations(t, auth, "new@example.com"); len(got) != 0 {
			t.Errorf("still open: %+v", got)
		}
	})
}

func TestInvitations_Refusals(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	if _, err := auth.RoleCreate(ctx, "billing", ""); err != nil {
		t.Fatal(err)
	}
	newUser(t, auth, "alice@example.com", "")

	for _, tt := range []struct {
		name string
		form url.Values
		want int
		msg  string
	}{
		{"no email", url.Values{}, http.StatusBadRequest, "Enter the email address"},
		{"not an email", url.Values{"email": {"nope"}}, http.StatusBadRequest, "doesn't look like an email address"},
		{"an email with a header injected", url.Values{"email": {"a@example.com\nBcc: x@evil.com"}}, http.StatusBadRequest, "doesn't look like an email address"},
		{"too long", url.Values{"email": {strings.Repeat("a", 250) + "@example.com"}}, http.StatusBadRequest, "doesn't look like an email address"},
		{"a role the admin doesn't hold", url.Values{"email": {"x@example.com"}, "role": {"billing"}}, http.StatusForbidden, "only give roles you hold"},
		{"a role that doesn't exist", url.Values{"email": {"x@example.com"}, "role": {"wizard"}}, http.StatusForbidden, "only give roles you hold"},
		{"an address that already has an account", url.Values{"email": {"alice@example.com"}}, http.StatusConflict, "already has an account"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, body, _ := invite(t, c, srv.URL, tt.form, true)
			if code != tt.want {
				t.Fatalf("status %d, want %d", code, tt.want)
			}
			mustContain(t, body, tt.msg)
			res, _ := auth.Service.InvitationsAll(ctx, service.ListInvitationsOptions{Limit: 100})
			if len(res.Invitations) != 0 {
				t.Errorf("a refused request created %d invitation(s)", len(res.Invitations))
			}
		})
	}

	t.Run("a second open invitation for the same address", func(t *testing.T) {
		if code, _, _ := invite(t, c, srv.URL, url.Values{"email": {"dup@example.com"}}, true); code != http.StatusOK {
			t.Fatalf("first: status %d", code)
		}
		code, body, _ := invite(t, c, srv.URL, url.Values{"email": {"DUP@example.com"}}, true)
		if code != http.StatusConflict {
			t.Fatalf("second: status %d, want 409", code)
		}
		mustContain(t, body, "already has an open invitation")
		if n := len(openInvitations(t, auth, "dup@example.com")); n != 1 {
			t.Errorf("%d open invitations, want 1", n)
		}
	})
}

func TestInvitations_EmailStatus(t *testing.T) {
	auth, srv, c := adminSession(t)

	t.Run("a mail failure keeps the invitation and shows the link", func(t *testing.T) {
		prev := auth.Service.Mailer
		auth.Service.Mailer = failingMailer{}
		defer func() { auth.Service.Mailer = prev }()

		code, body, _ := invite(t, c, srv.URL, url.Values{"email": {"nomail@example.com"}}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "The email couldn't be sent", "share the link below yourself")
		mustNotContain(t, body, "smtp is down") // the reason stays in the log
		token := linkToken(t, body)
		if _, err := auth.Service.InvitationPreview(context.Background(), token); err != nil {
			t.Errorf("the invitation was not kept: %v", err)
		}
	})

	t.Run("a working mailer sends the same link", func(t *testing.T) {
		m := &recordingMailer{}
		prev := auth.Service.Mailer
		auth.Service.Mailer = m
		defer func() { auth.Service.Mailer = prev }()

		code, body, _ := invite(t, c, srv.URL, url.Values{"email": {"mailed@example.com"}}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "We emailed the invitation to mailed@example.com")
		mustNotContain(t, body, "No mail server")
		token := linkToken(t, body)
		if len(m.sent) != 1 || m.sent[0]["to"] != "mailed@example.com" || !strings.Contains(m.sent[0]["body"], token) {
			t.Errorf("the email did not carry the link: %v", m.sent)
		}
	})

	t.Run("a plain form post shows the page, not a redirect carrying the link", func(t *testing.T) {
		code, body, h := invite(t, c, srv.URL, url.Values{"email": {"plain@example.com"}}, false)
		if code != http.StatusOK {
			t.Fatalf("status %d, want 200", code)
		}
		if h.Get("Location") != "" {
			t.Errorf("redirected to %q", h.Get("Location"))
		}
		mustContain(t, body, "<html", "Invitation for plain@example.com")
		linkToken(t, body)
	})
}

func TestInvitations_Revoke(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	newUser(t, auth, "admin2@example.com", "admin")
	admin2, err := auth.Repo.UserGetByEmail(ctx, "admin2@example.com")
	if err != nil {
		t.Fatal(err)
	}
	// An invitation another admin sent.
	issued, err := auth.Service.InvitationIssue(ctx, admin2, service.RequestInvitation{Email: "theirs@example.com"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("the list says who invited", func(t *testing.T) {
		_, body, _ := do(t, c, http.MethodGet, srv.URL+"/admin/invitations", nil, false)
		mustContain(t, body, "theirs@example.com", "admin2@example.com")
	})

	t.Run("revokes an invitation someone else sent, and its link stops working", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodPost, srv.URL+"/admin/invitations/"+issued.ID+"/revoke", url.Values{}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "Invitation revoked.", "No open invitations")
		if _, err := auth.Service.InvitationPreview(ctx, tokenOf(t, issued.Link)); err == nil {
			t.Error("the link still works after revoking")
		}
	})

	t.Run("unknown and already-gone invitations are a 404", func(t *testing.T) {
		for _, id := range []string{"nope", issued.ID} {
			code, _, _ := do(t, c, http.MethodPost, srv.URL+"/admin/invitations/"+id+"/resend", url.Values{}, true)
			if code != http.StatusNotFound {
				t.Errorf("resend %s: status %d, want 404", id, code)
			}
		}
		code, _, _ := do(t, c, http.MethodPost, srv.URL+"/admin/invitations/nope/revoke", url.Values{}, true)
		if code != http.StatusNotFound {
			t.Errorf("revoke nope: status %d, want 404", code)
		}
	})

	t.Run("a plain form post is redirected back to the list", func(t *testing.T) {
		again, err := auth.Service.InvitationIssue(ctx, admin2, service.RequestInvitation{Email: "again@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		code, _, h := do(t, c, http.MethodPost, srv.URL+"/admin/invitations/"+again.ID+"/revoke", url.Values{}, false)
		if code != http.StatusSeeOther || h.Get("Location") != "/admin/invitations" {
			t.Errorf("status %d, Location %q; want 303 to the list", code, h.Get("Location"))
		}
	})
}

func tokenOf(t *testing.T, link string) string {
	t.Helper()
	_, token, ok := strings.Cut(link, "token=")
	if !ok {
		t.Fatalf("no token in %q", link)
	}
	return token
}

func TestInvitations_Resend(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	admin, _ := auth.Repo.UserGetByEmail(ctx, "admin@example.com")
	if _, err := auth.RoleCreate(ctx, "support", ""); err != nil {
		t.Fatal(err)
	}
	if err := auth.UserRoleGrant(ctx, "", admin.ID, "support"); err != nil {
		t.Fatal(err)
	}
	old, err := auth.Service.InvitationIssue(ctx, admin, service.RequestInvitation{Email: "later@example.com", Roles: "support"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("issues a new link and retires the old one", func(t *testing.T) {
		code, body, _ := do(t, c, http.MethodPost, srv.URL+"/admin/invitations/"+old.ID+"/resend", url.Values{}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "Invitation for later@example.com", "Invitation sent again.")
		fresh := linkToken(t, body)
		if fresh == tokenOf(t, old.Link) {
			t.Fatal("the link did not change")
		}
		if _, err := auth.Service.InvitationPreview(ctx, tokenOf(t, old.Link)); err == nil {
			t.Error("the old link still works")
		}
		info, err := auth.Service.InvitationPreview(ctx, fresh)
		if err != nil {
			t.Fatalf("the new link does not work: %v", err)
		}
		if info.Roles != "support" {
			t.Errorf("roles = %q, want support carried over", info.Roles)
		}
		if n := len(openInvitations(t, auth, "later@example.com")); n != 1 {
			t.Errorf("%d open invitations for the address, want exactly 1", n)
		}
	})

	t.Run("an expired invitation can be resent", func(t *testing.T) {
		prev := auth.Service.Cfg.Invitation.TTL
		auth.Service.Cfg.Invitation.TTL = -time.Hour
		stale, err := auth.Service.InvitationIssue(ctx, admin, service.RequestInvitation{Email: "stale@example.com"})
		auth.Service.Cfg.Invitation.TTL = prev
		if err != nil {
			t.Fatal(err)
		}
		_, page, _ := do(t, c, http.MethodGet, srv.URL+"/admin/invitations", nil, false)
		mustContain(t, page, "stale@example.com", "Expired")

		code, body, _ := do(t, c, http.MethodPost, srv.URL+"/admin/invitations/"+stale.ID+"/resend", url.Values{}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		if _, err := auth.Service.InvitationPreview(ctx, linkToken(t, body)); err != nil {
			t.Errorf("the new link of a resent expired invitation does not work: %v", err)
		}
	})

	t.Run("refuses when the admin doesn't hold the invited role, and keeps the old invitation", func(t *testing.T) {
		newUser(t, auth, "admin2@example.com", "admin")
		admin2, _ := auth.Repo.UserGetByEmail(ctx, "admin2@example.com")
		if _, err := auth.RoleCreate(ctx, "billing", ""); err != nil {
			t.Fatal(err)
		}
		if err := auth.UserRoleGrant(ctx, "", admin2.ID, "billing"); err != nil {
			t.Fatal(err)
		}
		theirs, err := auth.Service.InvitationIssue(ctx, admin2, service.RequestInvitation{Email: "billing@example.com", Roles: "billing"})
		if err != nil {
			t.Fatal(err)
		}
		code, body, _ := do(t, c, http.MethodPost, srv.URL+"/admin/invitations/"+theirs.ID+"/resend", url.Values{}, true)
		if code != http.StatusForbidden {
			t.Fatalf("status %d, want 403", code)
		}
		mustContain(t, body, "you can't resend it")
		if _, err := auth.Service.InvitationPreview(ctx, tokenOf(t, theirs.Link)); err != nil {
			t.Errorf("the old invitation was lost: %v", err)
		}
	})

	t.Run("refuses when the address now has an account", func(t *testing.T) {
		pending, err := auth.Service.InvitationIssue(ctx, admin, service.RequestInvitation{Email: "joined@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		newUser(t, auth, "joined@example.com", "")
		code, body, _ := do(t, c, http.MethodPost, srv.URL+"/admin/invitations/"+pending.ID+"/resend", url.Values{}, true)
		if code != http.StatusConflict {
			t.Fatalf("status %d, want 409", code)
		}
		mustContain(t, body, "already has an account")
	})
}

func TestInvitations_Pagination(t *testing.T) {
	auth, srv, c := adminSession(t)
	ctx := context.Background()
	admin, _ := auth.Repo.UserGetByEmail(ctx, "admin@example.com")
	for i := 1; i <= 12; i++ {
		if _, err := auth.Service.InvitationIssue(ctx, admin, service.RequestInvitation{Email: fmt.Sprintf("person%02d@example.com", i)}); err != nil {
			t.Fatal(err)
		}
	}
	list := srv.URL + "/admin/invitations"

	_, p1, _ := do(t, c, http.MethodGet, list, nil, false)
	mustContain(t, p1, "Showing 1 to 10", `href="/admin/invitations?page=2"`)
	_, p2, _ := do(t, c, http.MethodGet, list+"?page=2", nil, true)
	mustContain(t, p2, `id="invitations-region"`, "Showing 11 to 12")
	mustNotContain(t, p2, "<html", "page=3")

	// A page past the end steps back to the last one with rows.
	code, far, _ := do(t, c, http.MethodGet, list+"?page=9", nil, false)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	mustContain(t, far, "Showing 11 to 12")

	for _, qs := range []string{"?page=0", "?page=abc", "?page=999999"} {
		if code, _, _ := do(t, c, http.MethodGet, list+qs, nil, false); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", qs, code)
		}
	}

	t.Run("an action keeps you on your page", func(t *testing.T) {
		res, _ := auth.Service.InvitationsAll(ctx, service.ListInvitationsOptions{Limit: 10, Offset: 10})
		code, body, _ := do(t, c, http.MethodPost, list+"/"+res.Invitations[0].ID+"/revoke", url.Values{"page": {"2"}}, true)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		mustContain(t, body, "Showing 11 to 11")
	})
}

func TestInvitations_AdminOnly(t *testing.T) {
	auth := newAuth(t)
	newUser(t, auth, "member@example.com", "")
	newUser(t, auth, "admin@example.com", "admin")
	srv := newServer(t, auth)
	c := client(t)
	login(t, c, srv, "member@example.com")

	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, "/admin/invitations"},
		{http.MethodPost, "/admin/invitations"},
		{http.MethodPost, "/admin/invitations/x/revoke"},
		{http.MethodPost, "/admin/invitations/x/resend"},
	} {
		code, _, _ := do(t, c, tt.method, srv.URL+tt.path, url.Values{"email": {"hacker@example.com"}}, true)
		if code != http.StatusForbidden {
			t.Errorf("%s %s as a member: status %d, want 403", tt.method, tt.path, code)
		}
	}
	res, _ := auth.Service.InvitationsAll(context.Background(), service.ListInvitationsOptions{Limit: 10})
	if len(res.Invitations) != 0 {
		t.Error("a member created an invitation")
	}
}
