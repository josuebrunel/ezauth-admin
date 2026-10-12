package admin

import (
	"net/http"
	"regexp"
	"strings"
)

// The rules mirror what ezauth enforces at registration, so an admin can't
// save a value a user couldn't have signed up with.
var (
	usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,30}$`)
	phoneRe    = regexp.MustCompile(`^\+?[1-9]\d{6,14}$`)
	localeRe   = regexp.MustCompile(`^[A-Za-z]{2,3}([-_][A-Za-z0-9]{2,8})*$`)
	timezoneRe = regexp.MustCompile(`^[A-Za-z0-9_+\-/]{1,64}$`)
)

const maxNameLength = 100

// profileForm is the part of a profile an admin can edit. Email is left out
// on purpose: changing it needs re-verification, which ezauth owns.
type profileForm struct {
	FirstName string
	LastName  string
	Username  string
	Phone     string
	Locale    string
	Timezone  string
}

func parseProfile(r *http.Request) profileForm {
	v := func(k string) string { return strings.TrimSpace(r.FormValue(k)) }
	return profileForm{
		FirstName: v("first_name"),
		LastName:  v("last_name"),
		Username:  v("username"),
		Phone:     v("phone"),
		Locale:    v("locale"),
		Timezone:  v("timezone"),
	}
}

// validate returns a message per invalid field, keyed by the form field name.
// Every field is optional, so an empty value is always valid.
func (f profileForm) validate() map[string]string {
	errs := map[string]string{}
	if len([]rune(f.FirstName)) > maxNameLength {
		errs["first_name"] = "Keep the first name under 100 characters."
	}
	if len([]rune(f.LastName)) > maxNameLength {
		errs["last_name"] = "Keep the last name under 100 characters."
	}
	if f.Username != "" && !usernameRe.MatchString(f.Username) {
		errs["username"] = "Use 3 to 30 letters, numbers, underscores or hyphens."
	}
	if f.Phone != "" && !phoneRe.MatchString(f.Phone) {
		errs["phone"] = "Use international format, like +33612345678."
	}
	if f.Locale != "" && !localeRe.MatchString(f.Locale) {
		errs["locale"] = "Use a language code like en or fr-CA."
	}
	if f.Timezone != "" && !timezoneRe.MatchString(f.Timezone) {
		errs["timezone"] = "Use a time zone name like Europe/Paris."
	}
	return errs
}
