package main

import (
	"html/template"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/josuebrunel/ezauth"
	"github.com/josuebrunel/ezauth/pkg/db/models"
	"github.com/josuebrunel/ezauth/pkg/service"
)

// The user-facing pages are app-owned, like the login page: ezauth's /auth
// routes only handle forms and JSON, they never render HTML. Register, reset
// and API key forms post straight to them. Profile and password changes have
// no HTTP route, so those two call the Go API.
//
// The pages share one layout. DaisyUI and HTMX come from pinned CDN builds
// since these pages belong to the example app, not to the panel.
var pages = template.Must(template.New("layout").Parse(`{{define "head"}}<!DOCTYPE html>
<html lang="en" data-theme="light">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>{{.Title}} · Acme</title>
	<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/daisyui@5.7.47/daisyui.css">
	<script src="https://cdn.jsdelivr.net/npm/htmx.org@2.0.4/dist/htmx.min.js"></script>
	<style>body{padding:1rem}.wrap{max-width:44rem;margin:0 auto}.narrow{max-width:24rem;margin:10vh auto}.card-body{gap:.75rem}</style>
</head>
<body>{{end}}

{{define "alerts"}}{{if .Error}}<div role="alert" class="alert alert-error">{{.Error}}</div>{{end}}{{if .Success}}<div role="alert" class="alert alert-success">{{.Success}}</div>{{end}}{{end}}

{{define "nav"}}<div class="navbar bg-base-200 rounded-box mb-4">
	<div class="flex-1"><a class="btn btn-ghost" href="/account">Acme</a></div>
	<ul class="menu menu-horizontal">
		<li><a href="/account">Profile</a></li>
		<li><a href="/account/api-keys">API keys</a></li>
		<li><a href="/account/sessions">Sessions</a></li>
		{{if .IsAdmin}}<li><a href="/admin/">Admin</a></li>{{end}}
		<li><form method="post" action="/auth/logout"><button class="btn btn-ghost btn-sm" type="submit">Log out</button></form></li>
	</ul>
</div>{{end}}

{{define "register"}}{{template "head" .}}
	<form class="card bg-base-200 narrow" method="post" action="/auth/register">
		<div class="card-body">
			<h1 class="card-title">Create your account</h1>
			{{template "alerts" .}}
			<input class="input w-full" type="text" name="first_name" placeholder="First name">
			<input class="input w-full" type="text" name="last_name" placeholder="Last name">
			<input class="input w-full" type="email" name="email" placeholder="Email" required autofocus>
			<input class="input w-full" type="password" name="password" placeholder="Password (8+ characters)" minlength="8" required>
			<input class="input w-full" type="password" name="password_confirm" placeholder="Confirm password" minlength="8" required>
			<button class="btn btn-primary" type="submit">Register</button>
			<a class="link text-sm" href="/login">Already have an account? Log in</a>
		</div>
	</form>
</body></html>{{end}}

{{define "forgot"}}{{template "head" .}}
	<form class="card bg-base-200 narrow" method="post" action="/auth/password-reset/request">
		<div class="card-body">
			<h1 class="card-title">Reset your password</h1>
			{{template "alerts" .}}
			<p class="text-sm">We'll email you a link to choose a new one.</p>
			<input class="input w-full" type="email" name="email" placeholder="Email" required autofocus>
			<button class="btn btn-primary" type="submit">Send reset link</button>
			<a class="link text-sm" href="/login">Back to log in</a>
		</div>
	</form>
</body></html>{{end}}

{{define "reset"}}{{template "head" .}}
	<form class="card bg-base-200 narrow" method="post" action="/auth/password-reset/confirm">
		<div class="card-body">
			<h1 class="card-title">Choose a new password</h1>
			{{template "alerts" .}}
			<input type="hidden" name="token" value="{{.Token}}">
			<input class="input w-full" type="password" name="password" placeholder="New password (8+ characters)" minlength="8" required autofocus>
			<button class="btn btn-primary" type="submit">Update password</button>
		</div>
	</form>
</body></html>{{end}}

{{define "profile"}}{{template "head" .}}
<div class="wrap">
	{{template "nav" .}}
	{{template "alerts" .}}
	<form class="card bg-base-200 mb-4" method="post" action="/account/profile">
		<div class="card-body">
			<h2 class="card-title">Profile</h2>
			<p class="text-sm">Signed in as {{.User.Email}}</p>
			<input class="input w-full" type="text" name="first_name" placeholder="First name" value="{{.User.FirstName}}">
			<input class="input w-full" type="text" name="last_name" placeholder="Last name" value="{{.User.LastName}}">
			<input class="input w-full" type="text" name="username" placeholder="Username" value="{{.User.Username}}">
			<input class="input w-full" type="url" name="avatar_url" placeholder="Avatar URL" value="{{.User.AvatarURL}}">
			<button class="btn btn-primary" type="submit">Save profile</button>
		</div>
	</form>
	<form class="card bg-base-200" method="post" action="/account/password">
		<div class="card-body">
			<h2 class="card-title">Change password</h2>
			<input class="input w-full" type="password" name="password" placeholder="New password" minlength="8" required>
			<input class="input w-full" type="password" name="password_confirm" placeholder="Confirm new password" minlength="8" required>
			<button class="btn" type="submit">Change password</button>
		</div>
	</form>
</div>
</body></html>{{end}}

{{define "apikeys"}}{{template "head" .}}
<div class="wrap">
	{{template "nav" .}}
	<div class="card bg-base-200 mb-4"><div class="card-body">
		<h2 class="card-title">API keys</h2>
		<p class="text-sm">Scoped keys authenticate requests with the <code>X-API-Key</code> header. The raw key is shown once, right after creation.</p>
		<form hx-post="/auth/api-keys" hx-swap="none" hx-on::after-request="onKeyCreated(event)" class="flex flex-col gap-2">
			<select class="select w-full" name="scopes" multiple size="3">
				<option value="profile:read">profile:read</option>
				<option value="profile:write">profile:write</option>
				<option value="admin:users">admin:users</option>
			</select>
			<span class="text-xs">Select none for an unscoped, full-access key.</span>
			<button class="btn btn-primary" type="submit">Create API key</button>
		</form>
	</div></div>
	<dialog id="new-key" class="modal"><div class="modal-box">
		<h3 class="font-bold">Your new API key</h3>
		<p class="text-sm">Copy it now, it won't be shown again.</p>
		<pre class="bg-base-200 p-2 rounded overflow-x-auto"><code id="new-key-value"></code></pre>
		<form method="dialog" class="modal-action"><button class="btn">Done</button></form>
	</div></dialog>
	<table class="table bg-base-200">
		<thead><tr><th>ID</th><th>Scopes</th><th>Expires</th><th></th></tr></thead>
		<tbody>
		{{range .Keys}}<tr>
			<td><code>{{.ID}}</code></td>
			<td>{{if .Scopes}}{{range .Scopes}}<span class="badge badge-info">{{.}}</span> {{end}}{{else}}<span class="badge">unscoped</span>{{end}}</td>
			<td>{{.ExpiresAt.Format "2006-01-02"}}</td>
			<td><button class="btn btn-sm" hx-delete="/auth/api-keys/{{.ID}}" hx-target="closest tr" hx-swap="delete" hx-confirm="Revoke this API key?">Revoke</button></td>
		</tr>{{else}}<tr><td colspan="4">No API keys yet.</td></tr>{{end}}
		</tbody>
	</table>
</div>
<script>
	function onKeyCreated(evt) {
		if (!evt.detail.successful) return;
		document.getElementById('new-key-value').textContent = JSON.parse(evt.detail.xhr.response).data.token;
		var d = document.getElementById('new-key');
		d.addEventListener('close', function () { location.reload(); }, { once: true });
		d.showModal();
	}
</script>
</body></html>{{end}}

{{define "sessions"}}{{template "head" .}}
<div class="wrap">
	{{template "nav" .}}
	<div class="card bg-base-200 mb-4"><div class="card-body">
		<h2 class="card-title">Sessions</h2>
		<p class="text-sm">Each row is a device currently logged in. Revoking one logs it out immediately.</p>
		<button class="btn btn-sm self-start" hx-delete="/auth/sessions" hx-confirm="Revoke every session, including this one?" hx-on::after-request="if (event.detail.successful) location.href = '/login'">Revoke all sessions</button>
	</div></div>
	<table class="table bg-base-200">
		<thead><tr><th>Session</th><th>Created</th><th>Expires</th><th></th></tr></thead>
		<tbody>
		{{range .Sessions}}<tr>
			<td><code>{{.ID}}</code></td>
			<td>{{.CreatedAt.Format "2006-01-02 15:04"}}</td>
			<td>{{.ExpiresAt.Format "2006-01-02"}}</td>
			<td><button class="btn btn-sm" hx-delete="/auth/sessions/{{.ID}}" hx-target="closest tr" hx-swap="delete" hx-confirm="Revoke this session?">Revoke</button></td>
		</tr>{{else}}<tr><td colspan="4">No active sessions.</td></tr>{{end}}
		</tbody>
	</table>
</div>
</body></html>{{end}}
`))

// pageData is what every page template reads. Fields a page doesn't use stay
// zero.
type pageData struct {
	Title, Error, Success, Token string
	IsAdmin                      bool
	User                         userView
	Keys                         []service.APIKeyInfo
	Sessions                     []service.SessionInfo
}

type userView struct{ Email, FirstName, LastName, Username, AvatarURL string }

func render(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ExecuteTemplate(w, name, data); err != nil {
		slog.ErrorContext(r.Context(), "render page", "page", name, "err", err)
	}
}

func registerPage(auth *ezauth.EzAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ezauth.IsAuthenticated(r.Context()) {
			http.Redirect(w, r, "/account", http.StatusFound)
			return
		}
		render(w, r, "register", pageData{Title: "Register", Error: auth.GetErrorMessage(r.Context())})
	}
}

func forgotPage(auth *ezauth.EzAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "forgot", pageData{Title: "Reset password", Error: auth.GetErrorMessage(r.Context())})
	}
}

// resetPage is where the emailed reset link ends up. EzAuth links to
// GET /auth/password-reset/confirm?token=..., but that route only accepts a
// POST, so main.go redirects the GET here.
func resetPage(auth *ezauth.EzAuth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		render(w, r, "reset", pageData{Title: "New password", Token: r.URL.Query().Get("token"), Error: auth.GetErrorMessage(r.Context())})
	}
}

func resetRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/reset-password?token="+url.QueryEscape(r.URL.Query().Get("token")), http.StatusFound)
}

// account holds the handlers behind the logged-in pages.
type account struct{ auth *ezauth.EzAuth }

// user returns the signed-in user, or redirects to the login page.
func (a account) user(w http.ResponseWriter, r *http.Request) (data pageData, u *models.User, ok bool) {
	u, err := a.auth.GetSessionUser(r.Context())
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return data, nil, false
	}
	admin, _ := a.auth.UserHasRole(r.Context(), u.ID, "admin")
	data.IsAdmin = admin
	data.User = userView{u.Email, u.FirstName, u.LastName, u.Username, u.AvatarURL}
	return data, u, true
}

func (a account) profilePage(w http.ResponseWriter, r *http.Request) {
	data, _, ok := a.user(w, r)
	if !ok {
		return
	}
	data.Title = "Profile"
	data.Error, data.Success = r.URL.Query().Get("error"), r.URL.Query().Get("success")
	render(w, r, "profile", data)
}

func (a account) profileUpdate(w http.ResponseWriter, r *http.Request) {
	_, u, ok := a.user(w, r)
	if !ok {
		return
	}
	f := func(k string) *string { v := r.FormValue(k); return &v }
	_, err := a.auth.Service.UserProfileUpdate(r.Context(), u.ID, service.ProfileUpdate{
		FirstName: f("first_name"), LastName: f("last_name"), Username: f("username"), AvatarURL: f("avatar_url"),
	})
	back(w, r, err, "Profile updated")
}

func (a account) passwordUpdate(w http.ResponseWriter, r *http.Request) {
	_, u, ok := a.user(w, r)
	if !ok {
		return
	}
	pw := r.FormValue("password")
	if pw != r.FormValue("password_confirm") {
		http.Redirect(w, r, "/account?error="+url.QueryEscape("passwords do not match"), http.StatusFound)
		return
	}
	_, err := a.auth.Service.UserUpdatePassword(r.Context(), u, pw)
	back(w, r, err, "Password updated")
}

// back sends the user to /account with the outcome in the query string.
func back(w http.ResponseWriter, r *http.Request, err error, success string) {
	if err != nil {
		slog.ErrorContext(r.Context(), "account update failed", "err", err)
		http.Redirect(w, r, "/account?error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/account?success="+url.QueryEscape(success), http.StatusFound)
}

func (a account) apiKeysPage(w http.ResponseWriter, r *http.Request) {
	data, u, ok := a.user(w, r)
	if !ok {
		return
	}
	keys, err := a.auth.APIKeysList(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "could not list API keys", http.StatusInternalServerError)
		slog.ErrorContext(r.Context(), "list api keys", "err", err)
		return
	}
	data.Title, data.Keys = "API keys", keys
	render(w, r, "apikeys", data)
}

func (a account) sessionsPage(w http.ResponseWriter, r *http.Request) {
	data, u, ok := a.user(w, r)
	if !ok {
		return
	}
	sessions, err := a.auth.Sessions(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "could not list sessions", http.StatusInternalServerError)
		slog.ErrorContext(r.Context(), "list sessions", "err", err)
		return
	}
	data.Title, data.Sessions = "Sessions", sessions
	render(w, r, "sessions", data)
}
