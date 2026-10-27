//go:build !playgroundui

package playground

import "io/fs"

func bundle() (fs.FS, error) {
	return nil, ErrNotBuilt
}
