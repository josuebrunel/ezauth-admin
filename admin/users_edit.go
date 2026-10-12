package admin

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/josuebrunel/ezauth"
	"github.com/josuebrunel/ezauth-admin/admin/views"
)

// notices are the only messages a redirect can ask the next page to show.
// Keeping them in a fixed set means the query string can't inject text.
var notices = map[string]string{
	"saved":   "Profile saved.",
	"deleted": "User deleted.",
}

func isUniqueViolation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}

func (p *Panel) userEditPage(w http.ResponseWriter, r *http.Request) error {
	user, err := p.loadUser(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	form := profileForm{
		FirstName: user.FirstName, LastName: user.LastName, Username: user.Username,
		Phone: user.Phone, Locale: user.Locale, Timezone: user.Timezone,
	}
	return p.renderEdit(w, r, http.StatusOK, user.ID, user.DisplayName(), form, nil)
}

func (p *Panel) renderEdit(w http.ResponseWriter, r *http.Request, status int, id, name string, f profileForm, errs map[string]string) error {
	pg := p.page(r, "users",
		views.Crumb{Label: "Dashboard", Href: "/"},
		views.Crumb{Label: "Users", Href: "/users"},
		views.Crumb{Label: name, Href: "/users/" + id},
		views.Crumb{Label: "Edit"},
	)
	return render(w, r, status, views.UserEditPage(pg, views.UserEdit{
		ID: id, Name: name, Errors: errs,
		FirstName: f.FirstName, LastName: f.LastName, Username: f.Username,
		Phone: f.Phone, Locale: f.Locale, Timezone: f.Timezone,
	}))
}

// userEditSave updates the editable profile fields.
//
// ezauth's update writes every column, so the user is loaded in full and only
// the edited fields are changed. Never save a partly filled user: that would
// silently reset is_active, email_verified, mfa_enabled and the rest.
func (p *Panel) userEditSave(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	user, err := p.loadUser(ctx, id)
	if err != nil {
		return err
	}

	form := parseProfile(r)
	if errs := form.validate(); len(errs) > 0 {
		return p.renderEdit(w, r, http.StatusUnprocessableEntity, id, user.DisplayName(), form, errs)
	}

	user.FirstName, user.LastName, user.Username = form.FirstName, form.LastName, form.Username
	user.Phone, user.Locale, user.Timezone = form.Phone, form.Locale, form.Timezone

	if err := p.auth.Service.Hook.BeforeUserUpdated(ctx, user); err != nil {
		slog.WarnContext(ctx, "admin: BeforeUserUpdated hook rejected the update", "user_id", id, "err", err)
		return NewHTTPError(http.StatusConflict, "An app rule blocked this change.")
	}
	if _, err := p.auth.Service.UserUpdate(ctx, user); err != nil {
		if isUniqueViolation(err) {
			return p.renderEdit(w, r, http.StatusConflict, id, user.DisplayName(), form,
				map[string]string{"form": "Another user already has that username or phone number."})
		}
		return fmt.Errorf("update user %s: %w", id, err)
	}
	if err := p.auth.Service.Hook.AfterUserUpdated(ctx, user); err != nil {
		slog.ErrorContext(ctx, "admin: AfterUserUpdated hook failed", "user_id", id, "err", err)
	}
	http.Redirect(w, r, p.basePath+"/users/"+id+"?notice=saved", http.StatusSeeOther)
	return nil
}

// userDelete deletes an account after the admin typed its email to confirm.
//
// ezauth's UserDelete is a bare database delete: it doesn't run the delete
// hooks or write the audit event, which its own HTTP route does around it. So
// the panel does the same, in the same order.
//
// There is no "last admin" count here on purpose. The signed-in admin must
// hold the role to reach this code, and they can't delete, suspend or demote
// themselves, so at least one admin always remains.
func (p *Panel) userDelete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	if isSelf(ctx, id) {
		return NewHTTPError(http.StatusBadRequest, "You can't delete your own account.")
	}
	user, err := p.loadUser(ctx, id)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(r.FormValue("confirm")), user.Email) {
		return NewHTTPError(http.StatusBadRequest, "The email you typed doesn't match, so nothing was deleted.")
	}

	if err := p.auth.Service.Hook.BeforeUserDeleted(ctx, user); err != nil {
		slog.WarnContext(ctx, "admin: BeforeUserDeleted hook rejected the delete", "user_id", id, "err", err)
		return NewHTTPError(http.StatusConflict, "An app rule blocked this deletion.")
	}
	if err := p.auth.Service.UserDelete(ctx, id); err != nil {
		return fmt.Errorf("delete user %s: %w", id, err)
	}
	// ezauth's audit table cascades on user delete (and its after-hook tries to
	// record "user.deleted" once the row is gone), so this event can't live in
	// the database. A structured log line is the record of who did it.
	actor, _ := ezauth.GetUserID(ctx)
	slog.InfoContext(ctx, "admin: user deleted", "user_id", id, "deleted_by", actor)
	if err := p.auth.Service.Hook.AfterUserDeleted(ctx, user); err != nil {
		slog.ErrorContext(ctx, "admin: AfterUserDeleted hook failed", "user_id", id, "err", err)
	}
	http.Redirect(w, r, p.basePath+"/users?notice=deleted", http.StatusSeeOther)
	return nil
}
