//go:build dev

package agentmirror

import "io/fs"

// Frontend leaves the development API independent of dist. Vite serves the UI
// and proxies API requests when started with npm run dev.
func Frontend() fs.FS { return nil }
