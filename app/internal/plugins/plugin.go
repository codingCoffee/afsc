// Package plugins is the compile-time plugin system (build plan §12): the Plugin interface, the Host plugins
// register on, typed hooks and the Runtime that runs them with panic recovery, timeouts and enabled checks.
package plugins

import (
	"context"
	"regexp"
)

// Plugin is a compiled-in extension. Init only registers hooks, funcs, routes, settings, jobs and migrations; it
// must not do I/O, because it also runs in `afsc theme check`, `afsc import` and `afsc migrate`.
type Plugin interface {
	ID() string
	Init(ctx context.Context, h *Host) error
}

// Info describes a plugin in the admin. Plugins that don't implement Describer get their ID as name, enabled.
type Info struct {
	Name           string
	Description    string
	DefaultEnabled bool
	AlwaysOn       bool // can't be switched off (spam)
}

type Describer interface {
	Info() Info
}

// IDPattern is the shape of a plugin ID. No underscores, so plg_<id>_ prefixes are unambiguous.
var IDPattern = regexp.MustCompile(`^[a-z][a-z0-9]{1,31}$`)

func info(p Plugin) Info {
	if d, ok := p.(Describer); ok {
		i := d.Info()
		if i.Name == "" {
			i.Name = p.ID()
		}
		return i
	}
	return Info{Name: p.ID(), DefaultEnabled: true}
}
