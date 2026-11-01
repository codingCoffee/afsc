//go:build !prod

package main

import "io/fs"

func clientFS() (fs.FS, error) {
	return nil, nil
}
