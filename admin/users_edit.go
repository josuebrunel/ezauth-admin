package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/josuebrunel/ezauth"
	"github.com/josuebrunel/ezauth/pkg/service"

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
// It goes through ezauth's UserProfileUpdate, which is built for this: every
// field in the form is written as typed, so a field left empty is cleared, and
// the account flags (suspended, verified, MFA) are never touched.
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

	// The before-hook sees what the user would look like after the change.
	candidate := *user
	candidate.FirstName, candidate.LastName, candidate.Username = form.FirstName, form.LastName, form.Username
	candidate.Phone, candidate.Locale, candidate.Timezone = form.Phone, form.Locale, form.Timezone
	if err := p.auth.Service.Hook.BeforeUserUpdated(ctx, &candidate); err != nil {
		slog.WarnContext(ctx, "admin: BeforeUserUpdated hook rejected the update", "user_id", id, "err", err)
		return NewHTTPError(http.StatusConflict, "An app rule blocked this change.")
	}

	updated, err := p.auth.Service.UserProfileUpdate(ctx, id, service.ProfileUpdate{
		FirstName: &form.FirstName,
		LastName:  &form.LastName,
		Username:  &form.Username,
		Phone:     &form.Phone,
		Locale:    &form.Locale,
		Timezone:  &form.Timezone,
	})
	if err != nil {
		if field, msg, ok := profileConflict(err); ok {
			return p.renderEdit(w, r, http.StatusConflict, id, user.DisplayName(), form, map[string]string{field: msg})
		}
		if isUniqueViolation(err) { // two admins racing for the same value
			return p.renderEdit(w, r, http.StatusConflict, id, user.DisplayName(), form,
				map[string]string{"form": "Another user already has that username or phone number."})
		}
		return fmt.Errorf("update user %s: %w", id, err)
	}
	if err := p.auth.Service.Hook.AfterUserUpdated(ctx, updated); err != nil {
		slog.ErrorContext(ctx, "admin: AfterUserUpdated hook failed", "user_id", id, "err", err)
	}
	http.Redirect(w, r, p.basePath+"/users/"+id+"?notice=saved", http.StatusSeeOther)
	return nil
}

// profileConflict maps ezauth's profile errors to the form field they belong to.
func profileConflict(err error) (field, message string, ok bool) {
	switch {
	case errors.Is(err, service.ErrUsernameInUse):
		return "username", "Another user already has that username.", true
	case errors.Is(err, service.ErrPhoneInUse):
		return "phone", "Another user already has that phone number.", true
	case errors.Is(err, service.ErrInvalidUsername):
		return "username", "Use 3 to 30 letters, numbers, underscores or hyphens.", true
	case errors.Is(err, service.ErrInvalidPhone):
		return "phone", "Use international format, like +33612345678.", true
	}
	return "", "", false
}

// userDelete deletes an account after the admin typed its email to confirm.
//
// ezauth's UserDelete is a bare database delete: it doesn't run the delete
// hooks (which also write the audit event), which its own HTTP route does
// around it. So the panel does the same, in the same order.
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
	// ezauth's after-hook stores the "user.deleted" audit event (with the user
	// id and email in its details), so only the actor is logged here.
	actor, _ := ezauth.GetUserID(ctx)
	slog.InfoContext(ctx, "admin: user deleted", "user_id", id, "deleted_by", actor)
	if err := p.auth.Service.Hook.AfterUserDeleted(ctx, user); err != nil {
		slog.ErrorContext(ctx, "admin: AfterUserDeleted hook failed", "user_id", id, "err", err)
	}
	http.Redirect(w, r, p.basePath+"/users?notice=deleted", http.StatusSeeOther)
	return nil
}
