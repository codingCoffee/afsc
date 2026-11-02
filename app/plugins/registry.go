// Package registry lists every compiled-in plugin. To add one, write its package under plugins/, add it below and
// rebuild; see docs/plugins.md.
package registry

import (
	"afsc/internal/plugins"
	"afsc/plugins/progress"
	"afsc/plugins/spam"
	"afsc/plugins/webhooks"
)

// All returns the plugins in registry order, which is also the tie-break order for hooks of equal priority.
func All() []plugins.Plugin {
	return []plugins.Plugin{
		spam.New(),
		webhooks.New(),
		progress.New(),
	}
}
