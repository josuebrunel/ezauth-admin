package admin

import (
	"strings"
	"time"
)

// Option configures a Panel.
type Option func(*Panel)

// WithBasePath sets the URL prefix the panel is mounted at. It must match the
// prefix the host router mounts the handler on. Defaults to "/admin".
func WithBasePath(path string) Option {
	return func(p *Panel) { p.basePath = normalizeBasePath(path) }
}

// WithRole sets the RBAC role required to use the panel. Defaults to the
// ezauth admin role (EZAUTH_ADMIN_ROLE, "admin" unless changed).
func WithRole(role string) Option {
	return func(p *Panel) { p.role = role }
}

// WithAppName sets the name shown in the panel header and page titles.
// Defaults to "EzAuth".
func WithAppName(name string) Option {
	return func(p *Panel) { p.appName = name }
}

// WithDashboardCache sets how long the dashboard reuses its user counts.
// Counting reads the whole users table, so the numbers are cached briefly
// (30 seconds by default). Use 0 to count on every load.
func WithDashboardCache(ttl time.Duration) Option {
	return func(p *Panel) { p.counts.ttl = ttl }
}

// normalizeBasePath returns path with a leading slash and no trailing slash.
// An empty path or "/" means the panel is mounted at the root.
func normalizeBasePath(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}
