package admin

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/josuebrunel/ezauth/pkg/db/models"
	"github.com/josuebrunel/ezauth/pkg/service"

	"github.com/josuebrunel/ezauth-admin/admin/views"
)

const (
	signupDays     = 14
	activityWindow = 7 * 24 * time.Hour
	recentEvents   = 8
)

// dashboardCounts are the numbers behind the dashboard cards and chart.
type dashboardCounts struct {
	stats   models.UserStats
	signups []views.DayCount
}

// countsCache keeps the dashboard numbers for a short while. Counting users
// reads the whole users table, and every admin who opens the dashboard would
// repeat it. A few seconds of staleness is fine for a "this week" overview.
type countsCache struct {
	mu     sync.Mutex
	ttl    time.Duration
	loaded time.Time
	value  dashboardCounts
}

func (c *countsCache) get(now time.Time, load func() (dashboardCounts, error)) (dashboardCounts, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ttl > 0 && !c.loaded.IsZero() && now.Sub(c.loaded) < c.ttl {
		return c.value, nil
	}
	v, err := load()
	if err != nil {
		return dashboardCounts{}, err
	}
	c.value, c.loaded = v, now
	return v, nil
}

func (p *Panel) loadCounts(ctx context.Context, now time.Time) (dashboardCounts, error) {
	stats, err := p.auth.Service.UserStats(ctx, now.Add(-activityWindow))
	if err != nil {
		return dashboardCounts{}, fmt.Errorf("count users: %w", err)
	}
	today := now.UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(signupDays - 1))
	rows, err := p.auth.Service.UserSignups(ctx, start)
	if err != nil {
		return dashboardCounts{}, fmt.Errorf("count sign-ups: %w", err)
	}
	return dashboardCounts{stats: *stats, signups: fillDays(rows, start, signupDays)}, nil
}

// fillDays turns the days that had sign-ups into one entry per day, so quiet
// days show as zero on the chart.
func fillDays(rows []service.DayCount, start time.Time, days int) []views.DayCount {
	byDay := make(map[string]int, len(rows))
	for _, r := range rows {
		byDay[r.Day.Format("2006-01-02")] = r.Count
	}
	out := make([]views.DayCount, 0, days)
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i)
		out = append(out, views.DayCount{Label: d.Format("2 Jan"), Count: byDay[d.Format("2006-01-02")]})
	}
	return out
}

func (p *Panel) dashboard(w http.ResponseWriter, r *http.Request) error {
	now := time.Now()
	counts, err := p.counts.get(now, func() (dashboardCounts, error) { return p.loadCounts(r.Context(), now) })
	if err != nil {
		return err
	}
	recent, _, err := p.recentEvents(r, service.ListAuditLogsOptions{Limit: recentEvents})
	if err != nil {
		return err
	}

	data := views.DashboardData{
		Total:      counts.stats.Total,
		NewWeek:    counts.stats.CreatedSince,
		ActiveWeek: counts.stats.ActiveSince,
		Inactive:   counts.stats.Inactive,
		Signups:    counts.signups,
		Recent:     recent,
		Now:        now,
	}
	if user, _ := currentUser(r); user != nil {
		data.FirstName = user.FirstName
	}
	pg := p.page(r, "dashboard", views.Crumb{Label: "Dashboard"})
	pg.Scripts = []string{"/static/chart.umd.min.js", "/static/admin-chart.js"}
	return render(w, r, http.StatusOK, views.Dashboard(pg, data))
}
