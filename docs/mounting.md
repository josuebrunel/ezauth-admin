# Mounting the panel

The panel is an `http.Handler`. You create it from your EzAuth instance and
mount it on your router, like any other handler.

## Requirements

- Go 1.25.4 or newer.
- EzAuth v0.31.0 or newer (the panel uses `AuditLogsAll`, `UserStats`,
  `UserSignups` and `UserProfileUpdate`, which arrived in v0.31.0).
- An EzAuth user who holds the admin role. See [the first admin](#the-first-admin).

```sh
go get github.com/josuebrunel/ezauth-admin
```

## What your router needs

Three things, in this order:

1. `auth.SessionMiddleware` on the router. EzAuth needs it for login, and the
   panel uses it to know who is signed in.
2. EzAuth itself, mounted for login, logout and the rest of `/auth/*`.
3. The panel.

### chi

```go
cfg, _ := config.LoadConfig()
auth, _ := ezauth.New(&cfg, "") // "" because Mount adds the /auth prefix
_ = auth.Migrate()

panel, _ := admin.New(auth, admin.WithAppName("Acme"))

r := chi.NewRouter()
r.Use(auth.SessionMiddleware)
r.Mount("/auth", auth.Handler)
r.Mount("/admin", panel.Handler())
```

### net/http

```go
auth, _ := ezauth.New(&cfg, "") // "" because StripPrefix removes /auth
panel, _ := admin.New(auth)

mux := http.NewServeMux()
mux.Handle("/auth/", http.StripPrefix("/auth", auth.Handler))
mux.Handle("/admin/", panel.Handler())
mux.Handle("/admin", panel.Handler())

http.ListenAndServe(":8080", auth.SessionMiddleware(mux))
```

Register both `/admin/` and `/admin`, so a request without the trailing slash
reaches the panel too.

### Any other router

Anything that can mount an `http.Handler` works. The panel removes its own base
path from the request, so it doesn't matter whether your router strips the
prefix first (chi's `Mount` doesn't change the request path, `http.StripPrefix`
does). Both are covered by tests.

## Options

| Option | Default | What it does |
|---|---|---|
| `WithBasePath(path)` | `/admin` | The URL prefix the panel is mounted at. It must match your router, because the panel writes its links and asset URLs from it. |
| `WithRole(role)` | EzAuth's admin role (`EZAUTH_ADMIN_ROLE`, `admin`) | The RBAC role that opens the panel. |
| `WithAppName(name)` | `EzAuth` | The name in the sidebar and page titles. |
| `WithDashboardCache(ttl)` | `30s` | How long the dashboard reuses its user counts. Use `0` to count on every load. |

Mounting somewhere else is two changes that have to agree:

```go
panel, _ := admin.New(auth, admin.WithBasePath("/internal/ops"))
r.Mount("/internal/ops", panel.Handler())
```

## The first admin

The panel is for users who hold the admin role, so someone has to be the first.
EzAuth's CLI does it:

```sh
ezauthapi create-admin -email=you@example.com -password=<a-strong-password>
```

Or in code: create the role with `auth.RoleCreate`, then `auth.UserRoleGrant`.
If you use `WithRole`, grant that role instead. From then on, admins can give
the role to others from a user's page.

## How access works

- Every route is closed by default. Someone who isn't signed in is redirected to
  your login page (`EZAUTH_LOGIN_PAGE_URL`).
- A signed-in user without the role gets a 403.
- State-changing requests are checked for same-origin (CSRF) using the browser's
  Fetch Metadata headers. There is no token to add to forms.
- An admin can't suspend, delete or impersonate their own account, or revoke their
  own admin role, so there is always an admin left.
- Impersonating redirects to `EZAUTH_REDIRECT_AFTER_LOGIN`. While impersonating,
  the panel is closed to the session, because the user being impersonated isn't an
  admin. Visiting `/admin/` shows that user's own profile with a banner and a
  button to stop impersonating, and any other page sends you back there. Nothing
  can be changed from an impersonated session.

## Behind a proxy

If TLS ends at a reverse proxy, set `EZAUTH_FORCE_SECURE_COOKIES=true`. EzAuth
warns about this at startup when `EZAUTH_BASE_URL` isn't `https://`.

## Troubleshooting

**Signing in returns a 404.** EzAuth was created with the path `"auth"` but
mounted with a router that already strips `/auth`. Pass `""` to `ezauth.New`
when you mount at `/auth`, or `"auth"` when your router leaves the prefix on.

**Every panel page returns a 500, and the log says the request panicked.** The
router the panel is mounted on doesn't run `auth.SessionMiddleware`, so there is
no session to read. Add it. The panel stays closed in the meantime.

**Signing in loops back to the login page.** The login page must live outside
`/auth/*` and `/admin/*`, and must not sit behind `LoginRequiredMiddleware`.
Point `EZAUTH_LOGIN_PAGE_URL` at it.

**A signed-in user gets a 403.** They don't hold the role. Check `WithRole`
against the role you granted.

**Pages show unstyled, or `/admin/static/...` is a 404.** The panel's base path
and your mount path disagree. See [Options](#options).
