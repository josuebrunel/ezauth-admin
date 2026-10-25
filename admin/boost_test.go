package admin_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func boostedGet(t *testing.T, c *http.Client, url string, extra map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Boosted", "true")
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

func TestBoostedNavigation(t *testing.T) {
	_, srv, c := adminSession(t)

	t.Run("answers with content only, shell kept out", func(t *testing.T) {
		code, body := boostedGet(t, c, srv.URL+"/admin/roles", nil)
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
		mustContain(t, body, `<title>`, `id="breadcrumbs"`, `id="main-nav"`, `hx-swap-oob="true"`, `id="roles-region"`)
		mustNotContain(t, body, "<!doctype", "<!DOCTYPE", "<html", `id="toasts"`, `class="drawer`)
	})

	t.Run("history restore gets the whole document", func(t *testing.T) {
		_, body := boostedGet(t, c, srv.URL+"/admin/roles", map[string]string{"HX-History-Restore-Request": "true"})
		if !strings.Contains(strings.ToLower(body), "<!doctype html>") {
			t.Errorf("history restore must return a full page, got:\n%.200s", body)
		}
	})

	t.Run("full page boosts the body into #main", func(t *testing.T) {
		_, body, _ := do(t, c, http.MethodGet, srv.URL+"/admin/roles", nil, false)
		mustContain(t, body, `hx-boost="true"`, `hx-target="#main"`)
	})
}
