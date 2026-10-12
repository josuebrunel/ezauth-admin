package admin

import (
	"embed"
	"io/fs"
)

// staticFS holds the built CSS and pinned JS the panel serves itself, so a
// host app needs no CDN access, no Node, and no extra files to deploy.
//
//go:embed static
var staticFS embed.FS

func staticSub() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// static is embedded at compile time, so this can't fail at runtime.
		panic(err)
	}
	return sub
}
