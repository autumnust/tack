package discuss

import (
	"embed"
	"io/fs"
)

//go:embed web/*
var webFS embed.FS

// WebFS returns a sub-filesystem rooted at the embedded web directory.
// Used by the server's static file handler.
func WebFS() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	return sub
}
