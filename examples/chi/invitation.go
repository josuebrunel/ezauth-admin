package main

import (
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/josuebrunel/ezauth"
)

// invitationCookie remembers the invitation token for an hour. EzAuth sends a
// failed submit (passwords that don't match, a weak password) back to this page
// without the token, so without this the invitee would have to open the emailed
// link again to retry.
const invitationCookie = "invitation_token"

// The accept page is app-owned, like the login page: the emailed link lands on
// the app, which shows a form that posts to EzAuth's POST /auth/invitation/accept.
var invitationTmpl = template.Must(template.New("invitation").Parse(`<!DOCTYPE html>
<html lang="en" data-theme="light">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Accept your invitation · Acme</title>
	<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/daisyui@5.7.47/daisyui.css">
	<style>body{min-height:100vh;display:grid;place-items:center;padding:1rem}.card{width:100%;max-width:26rem}.card-body{gap:.75rem}</style>
</head>
<body>
	{{if .Invalid}}
	<div class="card bg-base-200">
		<div class="card-body">
			<h1 class="card-title">This invitation can't be used</h1>
			{{if .Error}}<div role="alert" class="alert alert-error">{{.Error}}</div>{{end}}
			<p>The link is invalid, has expired, or was already used. Ask for a new one.</p>
			<a class="btn" href="/login">Go to the login page</a>
		</div>
	</div>
	{{else}}
	<form class="card bg-base-200" method="post" action="/auth/invitation/accept">
		<div class="card-body">
			<h1 class="card-title">Welcome to Acme</h1>
			<p>You're invited as <strong>{{.Email}}</strong>{{if .Roles}}, with the role {{.Roles}}{{end}}. Choose a password to finish.</p>
			{{if .Error}}<div role="alert" class="alert alert-error">{{.Error}}</div>{{end}}
			<input type="hidden" name="token" value="{{.Token}}">
			<div class="grid grid-cols-2 gap-2">
				<input class="input w-full" name="first_name" placeholder="First name" autocomplete="given-name">
				<input class="input w-full" name="last_name" placeholder="Last name" autocomplete="family-name">
			</div>
			<input class="input w-full" type="password" name="password" placeholder="Password (8+ characters)" required minlength="8" autocomplete="new-password">
			<input class="input w-full" type="password" name="password_confirm" placeholder="Repeat the password" required minlength="8" autocomplete="new-password">
			<button class="btn btn-primary" type="submit">Create my account</button>
		</div>
	</form>
	{{end}}
</body>
</html>`))

func invitationPage(auth *ezauth.EzAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		data := struct {
			Invalid bool
			Error   string
			Token   string
			Email   string
			Roles   string
		}{Error: auth.GetErrorMessage(ctx)}

		token := r.URL.Query().Get("token")
		if token != "" {
			http.SetCookie(w, &http.Cookie{
				Name: invitationCookie, Value: token, Path: "/invitation", MaxAge: 3600,
				HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			})
		} else if c, err := r.Cookie(invitationCookie); err == nil {
			token = c.Value
		}

		if token == "" {
			data.Invalid = true
		} else if info, err := auth.Service.InvitationPreview(ctx, token); err != nil {
			data.Invalid = true
		} else {
			data.Token, data.Email = token, info.Email
			data.Roles = strings.ReplaceAll(info.Roles, ",", ", ")
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer") // the token is in the URL
		if err := invitationTmpl.Execute(w, data); err != nil {
			slog.ErrorContext(ctx, "render invitation page", "err", err)
		}
	}
}
