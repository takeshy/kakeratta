// Package system provides the implementation specification shipped with the app.
package system

import "embed"

// Bundle is the read-only OKF v0.2 specification, shared by developers and support.
//
//go:embed *.md
var Bundle embed.FS
