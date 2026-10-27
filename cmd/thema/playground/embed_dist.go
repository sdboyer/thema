//go:build playgroundui

package playground

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

func bundle() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
