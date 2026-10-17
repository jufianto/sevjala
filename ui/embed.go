// Package ui is the dashboard's Svelte build (ui/dist), embedded in the binary.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS holds the built dashboard; a build without 'make ui' holds only a placeholder.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
