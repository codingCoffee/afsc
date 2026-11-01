//go:build prod

package main

import (
	"embed"
	"io/fs"
)

//go:embed all:client/.output/public
var clientDist embed.FS

func clientFS() (fs.FS, error) {
	return fs.Sub(clientDist, "client/.output/public")
}
