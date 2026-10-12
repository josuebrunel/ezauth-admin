package admin_test

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/josuebrunel/ezauth-admin/admin"
)

// statValue pulls the number shown under a dashboard card label.
func statValue(t *testing.T, body, label string) int {
	t.Helper()
	re := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(label) + `</h2>\s*<p class="stat-value[^"]*">(\d+)</p>`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %q card in the dashboard", label)
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func TestDashboard(t *testing.T) {
	auth := newAuth(t)
	newUser(t, auth, "admin@example.com", "admin")
	for _, e := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		newUser(t, auth, e, "")
	}
	if _, err := auth.Service.UserSuspend(context.Background(), userID(t, auth, "c@example.com")); err != nil {
		t.Fatal(err)
	}
	// Seen just now, so counted as active this week.
	if _, err := auth.Service.TokenCreate(context.Background(), getUser(t, auth, userID(t, auth, "a@example.com"))); err != nil {
		t.Fatal(err)
	}

	srv := newServer(t, auth, admin.WithDashboardCache(0))
	c := client(t)
	login(t, c, srv, "admin@example.com")

	code, body, _ := do(t, c, http.MethodGet, srv.URL+"/admin/", nil, false)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	mustContain(t, body, "Total users", "New sign-ups", "Recent activity", `data-chart="signups"`, `/admin/static/chart.umd.min.js`, `/admin/static/admin-chart.js`)

	if got := statValue(t, body, "Total users"); got != 4 {
		t.Errorf("total users = %d, want 4", got)
	}
	if got := statValue(t, body, "New this week"); got != 4 {
		t.Errorf("new this week = %d, want 4", got)
	}
	if got := statValue(t, body, "Can&#39;t sign in"); got != 1 {
		t.Errorf("can't sign in = %d, want 1", got)
	}
	if got := statValue(t, body, "Active this week"); got < 1 {
		t.Errorf("active this week = %d, want at least the user who just got tokens", got)
	}

	t.Run("the chart has one bar per day, with today's sign-ups last", func(t *testing.T) {
		m := regexp.MustCompile(`data-values="([^"]*)"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatal("no chart values")
		}
		vals := strings.Split(strings.Trim(strings.ReplaceAll(m[1], "&#34;", ""), "[]"), ",")
		if len(vals) != 14 {
			t.Fatalf("expected 14 days, got %d: %v", len(vals), vals)
		}
		if vals[13] != "4" {
			t.Errorf("today = %s, want 4 (all four users joined today)", vals[13])
		}
		for _, v := range vals[:13] {
			if v != "0" {
				t.Errorf("a quiet day shows %s, want 0: %v", v, vals)
				break
			}
		}
	})

	t.Run("shows the latest activity", func(t *testing.T) {
		mustContain(t, body, "See the audit log", `href="/admin/audit"`)
	})
}

// Counting users reads the whole table, so the numbers are cached briefly.
func TestDashboard_CachesCounts(t *testing.T) {
	auth := newAuth(t)
	newUser(t, auth, "admin@example.com", "admin")
	srv := newServer(t, auth, admin.WithDashboardCache(time.Hour))
	c := client(t)
	login(t, c, srv, "admin@example.com")

	_, first, _ := do(t, c, http.MethodGet, srv.URL+"/admin/", nil, false)
	before := statValue(t, first, "Total users")

	newUser(t, auth, "late@example.com", "")
	_, second, _ := do(t, c, http.MethodGet, srv.URL+"/admin/", nil, false)
	if got := statValue(t, second, "Total users"); got != before {
		t.Errorf("total changed from %d to %d inside the cache window", before, got)
	}

	// Without the cache the same request sees the new user.
	srv2 := newServer(t, auth, admin.WithDashboardCache(0))
	c2 := client(t)
	login(t, c2, srv2, "admin@example.com")
	_, fresh, _ := do(t, c2, http.MethodGet, srv2.URL+"/admin/", nil, false)
	if got := statValue(t, fresh, "Total users"); got != before+1 {
		t.Errorf("uncached total = %d, want %d", got, before+1)
	}
}
