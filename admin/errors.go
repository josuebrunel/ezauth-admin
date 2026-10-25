package admin

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/josuebrunel/ezauth-admin/admin/views"
)

// HTTPError is an error that carries the status code and the message that is
// safe to show to the admin. Anything else a handler returns is logged and
// shown as a generic 500, so internals never leak into the page.
type HTTPError struct {
	Code    int
	Message string
}

func (e *HTTPError) Error() string { return e.Message }

// NewHTTPError returns an HTTPError with the given status and public message.
func NewHTTPError(code int, message string) *HTTPError {
	return &HTTPError{Code: code, Message: message}
}

// handlerFunc is an http.HandlerFunc that returns an error. Handlers
// propagate errors, and handle() renders them in one place.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// handle adapts a handlerFunc to an http.Handler with centralized error
// rendering.
func (p *Panel) handle(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			p.renderError(w, r, err)
		}
	})
}

func (p *Panel) renderError(w http.ResponseWriter, r *http.Request, err error) {
	code, message := http.StatusInternalServerError, "Something went wrong on our side."
	var he *HTTPError
	if errors.As(err, &he) {
		code, message = he.Code, he.Message
	}
	if code >= http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "admin request failed",
			"method", r.Method, "path", r.URL.Path, "status", code, "err", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Add("Vary", "HX-Request, HX-Boosted, HX-History-Restore-Request")

	// An htmx action answers with a toast and leaves the page as it is.
	if isHTMX(r) {
		w.Header().Set("HX-Reswap", "none")
		w.WriteHeader(code)
		if rerr := views.Toast("error", message).Render(r.Context(), w); rerr != nil {
			slog.ErrorContext(r.Context(), "admin error toast render failed", "err", rerr)
		}
		return
	}
	w.WriteHeader(code)
	pg := p.page(r, "", views.Crumb{Label: "Error"})
	if rerr := views.ErrorPage(pg, code, message).Render(r.Context(), w); rerr != nil {
		slog.ErrorContext(r.Context(), "admin error page render failed", "err", rerr)
	}
}
