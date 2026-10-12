package main

import (
	"html/template"
	"log/slog"
	"net/http"

	"github.com/josuebrunel/ezauth"
)

// The login page is app-owned: ezauth's POST /auth/login only handles the
// form, it never renders one. DaisyUI comes from a pinned CDN build since
// this page belongs to the example app, not to the panel.
var loginTmpl = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="en" data-theme="light">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Log in · Acme</title>
	<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/daisyui@5.7.47/daisyui.css">
	<style>body{min-height:100vh;display:grid;place-items:center;padding:1rem}.card{width:100%;max-width:24rem}.card-body{gap:.75rem}</style>
</head>
<body>
	<form class="card bg-base-200" method="post" action="/auth/login">
		<div class="card-body">
			<h1 class="card-title">Log in to Acme</h1>
			{{if .Error}}<div role="alert" class="alert alert-error">{{.Error}}</div>{{end}}
			<label class="floating-label">
				<span>Email</span>
				<input class="input w-full" type="email" name="email" placeholder="Email" required autofocus>
			</label>
			<label class="floating-label">
				<span>Password</span>
				<input class="input w-full" type="password" name="password" placeholder="Password" required>
			</label>
			<button class="btn btn-primary" type="submit">Log in</button>
		</div>
	</form>
</body>
</html>`))

func loginPage(auth *ezauth.EzAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ezauth.IsAuthenticated(r.Context()) {
			http.Redirect(w, r, "/admin/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := struct{ Error string }{Error: auth.GetErrorMessage(r.Context())}
		if err := loginTmpl.Execute(w, data); err != nil {
			slog.ErrorContext(r.Context(), "render login page", "err", err)
		}
	}
}
