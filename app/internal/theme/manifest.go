// Package theme loads themes. For now only theme.json; templates arrive with the public site.
package theme

import (
  "encoding/json"
  "fmt"
  "os"
  "path/filepath"
)

type Manifest struct {
  Name          string   `json:"name"`
  Version       string   `json:"version"`
  MenuLocations []string `json:"menu_locations"`
  Content       struct {
    AllowedClasses []string `json:"allowed_classes"`
  } `json:"content"`
}

func LoadManifest(dir string) (*Manifest, error) {
  b, err := os.ReadFile(filepath.Join(dir, "theme.json"))
  if err != nil {
    return nil, fmt.Errorf("read theme.json: %w", err)
  }
  var m Manifest
  if err := json.Unmarshal(b, &m); err != nil {
    return nil, fmt.Errorf("parse theme.json: %w", err)
  }
  if m.Name == "" {
    return nil, fmt.Errorf("theme.json: name is required")
  }
  return &m, nil
}

func (m *Manifest) AllowedClasses() map[string]bool {
  out := make(map[string]bool, len(m.Content.AllowedClasses))
  for _, c := range m.Content.AllowedClasses {
    out[c] = true
  }
  return out
}
