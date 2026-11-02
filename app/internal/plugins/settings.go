package plugins

import (
	"fmt"
	"maps"
	"slices"

	"afsc/internal/settings"
)

// Listing is one plugin as the admin's Plugins screen shows it.
type Listing struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	AlwaysOn    bool      `json:"always_on"`
	Default     bool      `json:"default_enabled"`
	Settings    []Setting `json:"settings"`
}

// List describes every plugin in registry order.
func (rt *Runtime) List() []Listing {
	if rt == nil {
		return []Listing{}
	}
	out := make([]Listing, 0, len(rt.entries))
	for _, e := range rt.entries {
		decls := e.host.decls
		if decls == nil {
			decls = []Setting{}
		}
		out = append(out, Listing{ID: e.plugin.ID(), Name: e.info.Name, Description: e.info.Description,
			AlwaysOn: e.info.AlwaysOn, Default: e.info.DefaultEnabled || e.info.AlwaysOn, Settings: decls})
	}
	return out
}

// ValidateSettings checks the plugins settings group against the registry. Values saved for plugins that are no
// longer compiled in are kept as they are. It returns the group-relative field of the first problem.
func (rt *Runtime) ValidateSettings(p settings.Plugins) (field, msg string) {
	for _, id := range slices.Sorted(maps.Keys(p.Enabled)) {
		if e := rt.entry(id); e != nil && e.info.AlwaysOn && !p.Enabled[id] {
			return "enabled." + id, "this plugin can't be switched off"
		}
	}
	for _, id := range slices.Sorted(maps.Keys(p.Settings)) {
		e := rt.entry(id)
		if e == nil {
			continue
		}
		vals := p.Settings[id]
		for _, k := range slices.Sorted(maps.Keys(vals)) {
			i := slices.IndexFunc(e.host.decls, func(d Setting) bool { return d.Key == k })
			if i < 0 {
				return "settings." + id + "." + k, "not a setting of this plugin"
			}
			if err := e.host.decls[i].Check(vals[k]); err != nil {
				return "settings." + id + "." + k, err.Error()
			}
		}
	}
	return "", ""
}

func (rt *Runtime) entry(id string) *entry {
	if rt == nil {
		return nil
	}
	return rt.byID[id]
}

// Host returns plugin id's host, for tests and the admin mount.
func (rt *Runtime) Host(id string) (*Host, error) {
	e := rt.entry(id)
	if e == nil {
		return nil, fmt.Errorf("no plugin %q", id)
	}
	return e.host, nil
}
