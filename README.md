# EzAuth Admin

An admin panel for [EzAuth](https://github.com/josuebrunel/ezauth) that you mount with one line.

If you use EzAuth as a library, you'll build an admin panel at some point. Everyone does. A user list, a way to suspend someone, a place to grant roles, a look at the audit log. It's never the interesting part of your app, and it always takes longer than you think.

So I built it once. Mount it, log in as an admin, get on with your product.

> **Status:** early. The design is settled and the work is tracked in [the issues](https://github.com/josuebrunel/ezauth-admin/issues). Nothing below is released yet.

## Quick start

```go
auth, _ := ezauth.New(&cfg, "") // "" because Mount adds the /auth prefix

panel, _ := admin.New(auth, admin.WithAppName("Acme"))

r.Use(auth.SessionMiddleware)
r.Mount("/auth", auth.Handler)
r.Mount("/admin", panel.Handler())
```

That's it. The panel runs on your existing EzAuth instance and your existing database. No extra service, no extra deployment, no migrations of its own.

Your router needs `auth.SessionMiddleware` (you need it for login anyway). The panel adds its own guard on top.

It's a plain `http.Handler`, so it isn't tied to chi. But chi is what I build and test against, so that's what the docs show.

## What you get

- **Users:** search, filter, paginate, see a profile, suspend, reactivate, edit, delete, impersonate.
- **Roles and permissions:** create them, grant them, revoke them.
- **Audit log:** who did what, and when.
- **Dashboard:** user counts, recent sign-ups, recent activity.

Simple on purpose. Server-rendered pages, a little HTMX, no JavaScript build step, and assets embedded in the binary.

## Security

EzAuth's `service.Auth` methods don't check who's calling. The panel does. Every route is deny by default and sits behind your EzAuth session and the admin role (`admin` unless you change it with `admin.WithRole`). State-changing requests are protected against CSRF.

Still, it's an admin panel. Put it behind HTTPS, and think twice before exposing it to the whole internet.

## Stack

Go, [Templ](https://templ.guide), [HTMX](https://htmx.org), and [DaisyUI](https://daisyui.com) for styling. You don't need any of them installed to use the panel, the compiled templates and assets ship with the package. Changing the styles needs no Node either: `make css` downloads Tailwind's standalone binary.

## Try it

`make run-example` starts a small chi app with a seeded admin and a few demo users. Their passwords are logged once, on first start. The example lives in [`examples/chi`](examples/chi).

## Develop

```sh
make test
make run-example   # chi + SQLite, http://localhost:8080/admin/
make css           # only if you change the styles
```

## License

MIT, same as EzAuth.

**What works:** the plan, the design direction, and a working prototype of the user and role pages in [ezauth-example](https://github.com/josuebrunel/ezauth-example).

**What doesn't (yet):** everything else. Follow along in the issues.
