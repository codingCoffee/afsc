// Package plugintest builds a plugin runtime for a plugin's own tests.
package plugintest

import (
	"context"
	"testing"

	"afsc/internal/plugins"
	"afsc/internal/settings"
)

// Runtime builds ps with no database and binds it to s (defaults when nil). Change *s to change the settings the
// plugins see, for example s.Plugins.Enabled["id"] = true.
func Runtime(t testing.TB, s *settings.All, ps ...plugins.Plugin) *plugins.Runtime {
	t.Helper()
	rt, err := plugins.Build(context.Background(), plugins.Config{BaseURL: "https://blog.example.com", Logf: t.Logf}, ps)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		d := settings.Defaults()
		s = &d
	}
	rt.Bind(func() *settings.All { return s })
	return rt
}
