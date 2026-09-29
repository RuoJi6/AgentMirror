//go:build !dev

// Package agentmirror contains the compiled administration frontend.
package agentmirror

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var frontend embed.FS

func Frontend() fs.FS {
	root, err := fs.Sub(frontend, "dist")
	if err != nil {
		panic(err)
	}
	return root
}
