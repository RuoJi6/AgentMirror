// Package evaluated contains the exact, attachment-free evaluation materials
// shipped with the application. JSON files remain the single source of truth.
package evaluated

import "embed"

//go:embed prompts/*.json workspaces/*/workspace.json
var Files embed.FS
