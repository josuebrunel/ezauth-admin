package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/josuebrunel/ezauth/pkg/db/models"
	"github.com/josuebrunel/ezauth/pkg/service"

	"github.com/josuebrunel/ezauth-admin/admin/views"
)

const (
	maxEmailLength = 254
	// maxInvitationScan bounds the search for an existing open invitation to the
	// same address. A panel with more open invitations than this has bigger
	// problems than a duplicate.
	maxInvitationScan = 1000
)

// signedInUser returns the admin who is acting. The guard already checked that
// they hold the admin role, so a missing user can only mean a broken setup.
func signedInUser(r *http.Request) (*models.User, error) {
	user, err := currentUser(r)
	if err != nil || user == nil {
		return nil, NewHTTPError(http.StatusForbidden, "You're not signed in.")
	}
	return user, nil
}

// validEmail applies the same checks ezauth does before it accepts an address.
func validEmail(email string) bool {
	if email == "" || len(email) > maxEmailLength || strings.ContainsAny(email, "\r\n") {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

func splitRoles(s string) []string {
	var out []string
	for _, r := range strings.Split(s, ",") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// grantableRoles are the roles the admin may put on an invitation. ezauth
// refuses any role the inviter doesn't hold, so the form only offers these.
func (p *Panel) grantableRoles(ctx context.Context, actor *models.User) ([]string, error) {
	held, err := p.auth.UserRolesList(ctx, actor.ID)
	if err != nil {
		return nil, fmt.Errorf("list the admin's roles: %w", err)
	}
	names := make([]string, 0, len(held))
	for _, r := range held {
		names = append(names, r.Name)
	}
	slices.Sort(names)
	return names, nil
}

// invitationsData loads one page of open invitations. A page past the end steps
// back to the last one that has rows.
func (p *Panel) invitationsData(ctx context.Context, actor *models.User, page int) (views.InvitationsData, error) {
	var res *service.ListInvitationsResult
	for {
		var err error
		res, err = p.auth.Service.InvitationsAll(ctx, service.ListInvitationsOptions{
			Limit:  views.InvitationsPageSize,
			Offset: (page - 1) * views.InvitationsPageSize,
		})
		if err != nil {
			return views.InvitationsData{}, fmt.Errorf("list invitations: %w", err)
		}
		if len(res.Invitations) > 0 || page == 1 {
			break
		}
		page--
	}

	roles, err := p.grantableRoles(ctx, actor)
	if err != nil {
		return views.InvitationsData{}, err
	}

	inviters := map[string]string{} // id -> email, one lookup per distinct inviter
	rows := make([]views.InvitationRow, 0, len(res.Invitations))
	for _, inv := range res.Invitations {
		email, seen := inviters[inv.InviterID]
		if !seen {
			email = "Deleted user"
			if u, err := p.auth.Repo.UserGetByID(ctx, inv.InviterID); err == nil && u.Email != "" {
				email = u.Email
			}
			inviters[inv.InviterID] = email
		}
		rows = append(rows, views.InvitationRow{
			ID: inv.ID, Email: inv.Email, Roles: splitRoles(inv.Roles), InviterEmail: email,
			CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt,
		})
	}
	return views.InvitationsData{Rows: rows, Page: page, HasMore: res.HasMore, Now: time.Now(), Roles: roles}, nil
}

func (p *Panel) invitationsPage(w http.ResponseWriter, r *http.Request) error {
	actor, err := signedInUser(r)
	if err != nil {
		return err
	}
	page, err := pageParam(r)
	if err != nil {
		return err
	}
	d, err := p.invitationsData(r.Context(), actor, page)
	if err != nil {
		return err
	}
	pg := p.page(r, "invitations", views.Crumb{Label: "Dashboard", Href: "/"}, views.Crumb{Label: "Invitations"})
	if isHTMX(r) {
		return render(w, r, http.StatusOK, views.InvitationsRegion(pg, d))
	}
	return render(w, r, http.StatusOK, views.InvitationsPage(pg, d))
}

// invitationsDone answers an action with the refreshed list. When the action
// made a link (invite, resend) a plain form post renders the page itself
// instead of redirecting: the link is a secret and must not travel in a URL.
func (p *Panel) invitationsDone(w http.ResponseWriter, r *http.Request, created *views.InvitationCreated, toast string) error {
	actor, err := signedInUser(r)
	if err != nil {
		return err
	}
	page, err := pageParam(r)
	if err != nil {
		return err
	}
	d, err := p.invitationsData(r.Context(), actor, page)
	if err != nil {
		return err
	}
	d.Created = created
	pg := p.page(r, "invitations", views.Crumb{Label: "Dashboard", Href: "/"}, views.Crumb{Label: "Invitations"})

	if isHTMX(r) {
		return p.done(w, r, "", toast, views.InvitationsRegion(pg, d))
	}
	if created == nil {
		http.Redirect(w, r, pg.Path("/invitations"), http.StatusSeeOther)
		return nil
	}
	return render(w, r, http.StatusOK, views.InvitationsPage(pg, d))
}

// createdNote says what happened to the invitation email.
func (p *Panel) createdNote(issued *service.IssuedInvitation) (note string, warn bool) {
	switch {
	case issued.EmailErr != nil:
		return "The email couldn't be sent, so share the link below yourself.", true
	case isMockMailer(p.auth.Service.Mailer):
		return "No mail server is set up, so nothing was emailed. Share the link below.", false
	default:
		return "We emailed the invitation to " + issued.Email + ". The link below does the same, in case it doesn't arrive.", false
	}
}

func isMockMailer(m service.Mailer) bool {
	_, ok := m.(*service.MockMailer)
	return ok
}

// hasOpenInvitation reports whether email already has an invitation that hasn't
// expired. Several open invitations for one address would only confuse: the
// first to be accepted turns the others into dead links.
func (p *Panel) hasOpenInvitation(ctx context.Context, email string) (bool, error) {
	now := time.Now()
	for offset := 0; offset < maxInvitationScan; offset += 100 {
		res, err := p.auth.Service.InvitationsAll(ctx, service.ListInvitationsOptions{Limit: 100, Offset: offset})
		if err != nil {
			return false, fmt.Errorf("check for an open invitation: %w", err)
		}
		for _, inv := range res.Invitations {
			if strings.EqualFold(inv.Email, email) && inv.ExpiresAt.After(now) {
				return true, nil
			}
		}
		if !res.HasMore {
			return false, nil
		}
	}
	return false, nil
}

// issueError maps ezauth's invitation errors to what the admin should read.
func issueError(err error, email string) error {
	switch {
	case errors.Is(err, service.ErrEmailAlreadyRegistered):
		return NewHTTPError(http.StatusConflict, email+" already has an account.")
	case errors.Is(err, service.ErrCannotGrantRole):
		return NewHTTPError(http.StatusForbidden, "You can only give roles you hold yourself.")
	}
	return fmt.Errorf("issue invitation: %w", err)
}

func (p *Panel) invitationCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	actor, err := signedInUser(r)
	if err != nil {
		return err
	}

	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	if email == "" {
		return NewHTTPError(http.StatusBadRequest, "Enter the email address to invite.")
	}
	if !validEmail(email) {
		return NewHTTPError(http.StatusBadRequest, "That doesn't look like an email address.")
	}

	held, err := p.grantableRoles(ctx, actor)
	if err != nil {
		return err
	}
	var roles []string
	for _, role := range r.Form["role"] {
		role = strings.TrimSpace(role)
		if role == "" || slices.Contains(roles, role) {
			continue
		}
		if !slices.Contains(held, role) {
			return NewHTTPError(http.StatusForbidden, "You can only give roles you hold yourself.")
		}
		roles = append(roles, role)
	}

	if open, err := p.hasOpenInvitation(ctx, email); err != nil {
		return err
	} else if open {
		return NewHTTPError(http.StatusConflict, email+" already has an open invitation. Resend or revoke it instead.")
	}

	issued, err := p.auth.Service.InvitationIssue(ctx, actor, service.RequestInvitation{Email: email, Roles: strings.Join(roles, ",")})
	if err != nil {
		return issueError(err, email)
	}
	slog.InfoContext(ctx, "admin: invitation created", "invitation_id", issued.ID, "invited_by", actor.ID)

	note, warn := p.createdNote(issued)
	return p.invitationsDone(w, r, &views.InvitationCreated{Email: email, Link: issued.Link, Note: note, Warn: warn}, "Invitation created.")
}

func (p *Panel) invitationRevoke(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if err := p.auth.Service.InvitationRevokeByID(r.Context(), id); err != nil {
		if errors.Is(err, service.ErrInvitationNotFound) {
			return NewHTTPError(http.StatusNotFound, "That invitation doesn't exist.")
		}
		return fmt.Errorf("revoke invitation %s: %w", id, err)
	}
	actor, _ := signedInUser(r)
	if actor != nil {
		slog.InfoContext(r.Context(), "admin: invitation revoked", "invitation_id", id, "revoked_by", actor.ID)
	}
	return p.invitationsDone(w, r, nil, "Invitation revoked.")
}

// invitationResend issues a fresh invitation with the same email and roles and
// revokes the old one. Only a hash of the old link is stored, so it can't be sent
// again, and the new invitation is issued first so a failure leaves the old one
// in place.
func (p *Panel) invitationResend(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	actor, err := signedInUser(r)
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	old, err := p.auth.Repo.TokenGetByID(ctx, id)
	if err != nil || old.TokenType != models.TokenTypeInvitation || old.Revoked {
		return NewHTTPError(http.StatusNotFound, "That invitation doesn't exist.")
	}
	email, _ := old.Metadata["email"].(string)
	roles, _ := old.Metadata["roles"].(string)
	data, _ := old.Metadata["data"].(map[string]any)

	issued, err := p.auth.Service.InvitationIssue(ctx, actor, service.RequestInvitation{Email: email, Roles: roles, Data: data})
	if err != nil {
		if errors.Is(err, service.ErrCannotGrantRole) {
			return NewHTTPError(http.StatusForbidden, "This invitation gives a role you don't hold, so you can't resend it.")
		}
		return issueError(err, email)
	}
	if err := p.auth.Service.InvitationRevokeByID(ctx, id); err != nil {
		slog.WarnContext(ctx, "admin: could not revoke the invitation that was resent", "invitation_id", id, "err", err)
	}
	slog.InfoContext(ctx, "admin: invitation resent", "invitation_id", issued.ID, "replaces", id, "resent_by", actor.ID)

	note, warn := p.createdNote(issued)
	return p.invitationsDone(w, r, &views.InvitationCreated{Email: email, Link: issued.Link, Note: note, Warn: warn}, "Invitation sent again.")
}
