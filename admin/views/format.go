package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/josuebrunel/ezauth/pkg/db/models"
)

// Crumb is one breadcrumb. An empty Href marks the current page.
type Crumb struct {
	Label string
	Href  string
}

// userInitials returns up to two capital letters to stand in for an avatar.
func userInitials(u *models.User) string {
	var out []rune
	for _, s := range []string{u.FirstName, u.LastName} {
		if r := []rune(strings.TrimSpace(s)); len(r) > 0 {
			out = append(out, r[0])
		}
	}
	if len(out) == 0 {
		src := u.Username
		if src == "" {
			src = u.Email
		}
		if r := []rune(strings.TrimSpace(src)); len(r) > 0 {
			out = append(out, r[0])
		}
	}
	return strings.ToUpper(string(out))
}

// UserStatus is how an account's state reads in the UI.
type UserStatus struct {
	Label string
	Class string // a chip-* class
}

// statusOf derives the status the same way ezauth does: active, a temporary
// brute-force lockout, or an administrative suspension.
func statusOf(u *models.User, now time.Time) UserStatus {
	switch {
	case u.IsActive:
		return UserStatus{"Active", "chip-ok"}
	case u.LockedUntil != nil && u.LockedUntil.After(now):
		return UserStatus{"Locked", "chip-warn"}
	default:
		return UserStatus{"Suspended", "chip-bad"}
	}
}

func isSuspended(u *models.User, now time.Time) bool {
	return !u.IsActive && (u.LockedUntil == nil || !u.LockedUntil.After(now))
}

// relTime renders t as "5 min ago", "yesterday", and so on.
func relTime(t *time.Time, now time.Time) string {
	if t == nil || t.IsZero() {
		return "Never"
	}
	d := now.Sub(*t)
	switch {
	case d < time.Minute:
		return "Just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "Yesterday"
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	default:
		return t.Format("2 Jan 2006")
	}
}

func fmtDate(t time.Time) string { return t.Format("2 Jan 2006") }

func fmtDateTime(t time.Time) string { return t.Format("2 Jan 2006, 15:04") }

func fmtTimePtr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "Never"
	}
	return fmtDateTime(*t)
}

func orNotSet(s string) string {
	if strings.TrimSpace(s) == "" {
		return "Not set"
	}
	return s
}

// displayName prefers the full name and falls back to username, then email.
func displayName(u *models.User) string {
	return u.DisplayName()
}
