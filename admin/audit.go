package admin

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/josuebrunel/ezauth/pkg/db/models"
	"github.com/josuebrunel/ezauth/pkg/service"

	"github.com/josuebrunel/ezauth-admin/admin/views"
)

const auditPageSize = 25

// auditEventTypes are the events ezauth records, for the filter. A request
// can only filter by one of these, so the query string can't probe the table.
var auditEventTypes = func() []string {
	t := []string{
		models.AuditEventUserCreated, models.AuditEventUserDeleted,
		models.AuditEventLoginSucceeded, models.AuditEventLoginFailed, models.AuditEventLogoutSucceeded,
		models.AuditEventPasswordResetRequested, models.AuditEventPasswordResetConfirmed,
		models.AuditEventOAuth2SignedIn, models.AuditEventOAuth2Created,
		models.AuditEventImpersonationStarted, models.AuditEventImpersonationEnded,
		models.AuditEventMFAEnabled, models.AuditEventMFADisabled,
		models.AuditEventAccountLocked,
		models.AuditEventRoleGranted, models.AuditEventRoleRevoked,
	}
	slices.Sort(t)
	return t
}()

// pageParam reads the 1-based page from the query string or a posted form.
func pageParam(r *http.Request) (int, error) {
	raw := r.FormValue("page")
	if raw == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxPage {
		return 0, NewHTTPError(http.StatusBadRequest, "Unknown page.")
	}
	return n, nil
}

// toAuditEntry flattens an ezauth audit row for the views.
func toAuditEntry(e *models.AuditLogEntry) views.AuditEntry {
	out := views.AuditEntry{
		ID:        e.ID,
		EventType: e.EventType,
		Details:   views.SortedDetails(e.Metadata),
		At:        e.CreatedAt,
	}
	if e.UserID != nil {
		out.UserID = *e.UserID
	}
	if e.UserEmail != nil {
		out.UserEmail = *e.UserEmail
	}
	return out
}

func (p *Panel) recentEvents(r *http.Request, opts service.ListAuditLogsOptions) ([]views.AuditEntry, bool, error) {
	res, err := p.auth.Service.AuditLogsAll(r.Context(), opts)
	if err != nil {
		return nil, false, fmt.Errorf("list audit log: %w", err)
	}
	entries := make([]views.AuditEntry, 0, len(res.Events))
	for _, e := range res.Events {
		entries = append(entries, toAuditEntry(e))
	}
	return entries, res.HasMore, nil
}

func (p *Panel) auditPage(w http.ResponseWriter, r *http.Request) error {
	event := r.URL.Query().Get("event")
	if event != "" && !slices.Contains(auditEventTypes, event) {
		return NewHTTPError(http.StatusBadRequest, "Unknown event type.")
	}
	page, err := pageParam(r)
	if err != nil {
		return err
	}
	entries, hasMore, err := p.recentEvents(r, service.ListAuditLogsOptions{
		EventType: event,
		Limit:     auditPageSize,
		Offset:    (page - 1) * auditPageSize,
	})
	if err != nil {
		return err
	}
	data := views.AuditData{
		Entries: entries, EventType: event, Types: auditEventTypes,
		Page: page, PageSize: auditPageSize, HasMore: hasMore, Now: time.Now(),
	}
	pg := p.page(r, "audit", views.Crumb{Label: "Dashboard", Href: "/"}, views.Crumb{Label: "Audit log"})
	if isHTMX(r) {
		return render(w, r, http.StatusOK, views.AuditResults(pg, data))
	}
	return render(w, r, http.StatusOK, views.AuditPage(pg, data))
}
