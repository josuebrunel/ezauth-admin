# EzAuth Admin

**A ready-made admin panel for [EzAuth](https://github.com/josuebrunel/ezauth). Mount it with one line.**

Users, roles, permissions, an audit log and a dashboard, running on your existing EzAuth instance and database. No extra service to deploy, no migrations of its own.

**Live demo: <https://ezauth-admin.onrender.com/admin/>**, sign in as `admin@example.com` with `demo-admin-password`.

[Quick start](#quick-start) · [Try the demo](#try-the-demo) · [What you get](#what-you-get) · [Options](#options) · [Security](#security) · [Mounting guide](docs/mounting.md)

> **Status:** v0.1.0. Early, but complete: everything below works. Needs EzAuth v0.31.0 or newer.

If you use EzAuth as a library, you'll build an admin panel at some point. Everyone does. It's never the interesting part of your app, and it always takes longer than you think. So I built it once.

## Quick start

You need Go 1.25.4 or newer, and EzAuth v0.31.0 or newer.

### 1. Install

```sh
go get github.com/josuebrunel/ezauth-admin
```

### 2. Mount it

Here's a complete `main.go`. If you already use EzAuth, you only need the lines marked `// new`.

```go
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/josuebrunel/ezauth"
	"github.com/josuebrunel/ezauth/pkg/config"

	"github.com/josuebrunel/ezauth-admin/admin" // new
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	auth, err := ezauth.New(&cfg, "") // "" because Mount adds the /auth prefix
	if err != nil {
		log.Fatal(err)
	}
	if err := auth.Migrate(); err != nil {
		log.Fatal(err)
	}

	panel, err := admin.New(auth, admin.WithAppName("Acme")) // new
	if err != nil {
		log.Fatal(err)
	}

	r := chi.NewRouter()
	r.Use(auth.SessionMiddleware) // the panel needs this to know who's signed in
	r.Mount("/auth", auth.Handler)
	r.Mount("/admin", panel.Handler()) // new

	// A signed-out visitor is sent here. You probably have a login page already.
	r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<form method="post" action="/auth/login">
			<input type="email" name="email" placeholder="Email" required>
			<input type="password" name="password" placeholder="Password" required>
			<button>Log in</button></form>`)
	})

	log.Fatal(http.ListenAndServe(":8080", r))
}
```

Then run it with the settings EzAuth needs:

```sh
export EZAUTH_API_KEY=change-me
export EZAUTH_JWT_SECRET=change-me-to-something-at-least-32-characters
export EZAUTH_DB_DIALECT=sqlite
export EZAUTH_DB_DSN=file:app.db
export EZAUTH_LOGIN_PAGE_URL=/login
export EZAUTH_REDIRECT_AFTER_LOGIN=/admin/

go run .
```

### 3. Create your first admin

The panel is for users who hold the admin role, so someone has to be the first. With the app running, use EzAuth's CLI:

```sh
go run github.com/josuebrunel/ezauth/cmd/ezauthapi@latest create-admin \
  -email=you@example.com -password='a-strong-password'
```

(Run it with the same `EZAUTH_*` settings. The `ezauthapi` binary is also on [EzAuth's releases page](https://github.com/josuebrunel/ezauth/releases).)

### 4. Open it

Go to <http://localhost:8080/admin/>, sign in, and you're on the dashboard.

That's it. Something not working? The [mounting guide](docs/mounting.md) has a troubleshooting list for the usual suspects.

## Try the demo

**Online:** <https://ezauth-admin.onrender.com/admin/>

Sign in with one of these:

| Who | Email | Password |
|---|---|---|
| Admin | `admin@example.com` | `demo-admin-password` |
| Demo users | `alice@`, `bob@`, `carol@`, `dave@example.com` | `demo-user-password` |

Sign in as alice and open `/admin/` to see what a non-admin gets: a 403.

It's a shared demo on a free server, so the first load can be slow if it's asleep, and everything resets whenever it restarts. Please be kind to it.

**On your machine:** Docker runs the same example app, with the panel mounted, on SQLite, and the same admin and four demo users already created:

```sh
docker compose up --build
```

Then open <http://localhost:8080> and use the same logins. No Docker? `make run-example` does the same with `go run`, and logs random passwords once. The example lives in [`examples/chi`](examples/chi).

These passwords are for the demo only. Use your own, and your own `EZAUTH_*` secrets, for anything real.

## What you get

| Page | What you can do |
|---|---|
| **Dashboard** | See user counts, a 14-day sign-ups chart and the latest activity. |
| **Users** | Search, filter by status, and page through everyone. Open a user to edit the profile, suspend or reactivate, grant and revoke roles, keep admin-only notes, impersonate, or delete (you type the email to confirm). |
| **Roles** | Create and delete roles, and choose the permissions each one holds. |
| **Permissions** | Create and delete the permissions your app checks. |
| **Audit log** | Read sign-ins, role changes, impersonation and deletions across all users, filtered by event. |

Light and dark themes, works on a phone, and nothing to build: it's server-rendered, with a little HTMX, and every asset is embedded in the package.

## Options

```go
admin.New(auth,
	admin.WithAppName("Acme"),                // name in the sidebar and page titles
	admin.WithBasePath("/admin"),             // must match where you mount it
	admin.WithRole("admin"),                  // the role that opens the panel
	admin.WithDashboardCache(30*time.Second), // how long dashboard counts are reused
)
```

All four are optional. The defaults are shown, except the app name (`EzAuth`). More in the [mounting guide](docs/mounting.md#options).

## Security

- **Closed by default.** Every route needs a signed-in user holding the admin role. Signed-out visitors go to your login page, everyone else gets a 403.
- **It does its own checking.** EzAuth's `service.Auth` methods don't check who's calling, so the panel checks on every route.
- **CSRF protected.** State-changing requests must be same-origin. There's no token to add.
- **You can't lock yourself out.** An admin can't suspend, delete or impersonate their own account, or revoke their own admin role. The role that opens the panel can't be deleted.
- **Hooks and audit.** Deleting a user runs your `BeforeUserDeleted` and `AfterUserDeleted` hooks, and the deletion lands in the audit log with the user's email.

It's still an admin panel. Put it behind HTTPS, and think twice before exposing it to the whole internet.

## Develop

```sh
make test          # unit and integration tests
make run-example   # chi + SQLite on http://localhost:8080
make docker-up     # the same, in Docker
make css           # only if you change the styles
```

Built with Go, [Templ](https://templ.guide), [HTMX](https://htmx.org) and [DaisyUI](https://daisyui.com). You don't need any of them to use the panel, and `make css` downloads Tailwind's standalone binary, so there's no Node either.

## License

MIT, same as EzAuth.
