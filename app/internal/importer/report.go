package importer

import (
	"fmt"
	"sort"

	"afsc/internal/sanitize"
)

type Counts struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Skipped   int `json:"skipped"`
	Deleted   int `json:"deleted,omitempty"`
}

type Issue struct {
	LegacyKey string `json:"legacy_key,omitempty"`
	Field     string `json:"field,omitempty"`
	Message   string `json:"message"`
}

type SanitizerRemoval struct {
	LegacyKey string `json:"legacy_key"`
	Path      string `json:"path"`
	sanitize.Removal
}

type Invite struct {
	LegacyKey string `json:"legacy_key"`
	Email     string `json:"email"`
	Command   string `json:"command"`
}

type PruneItem struct {
	Table  string   `json:"table"`
	Count  int      `json:"count"`
	Sample []string `json:"sample"`
}

type Report struct {
	Source   string             `json:"source"`
	DryRun   bool               `json:"dry_run"`
	Counts   map[string]*Counts `json:"counts"`
	Errors   []Issue            `json:"errors"`
	Warnings []Issue            `json:"warnings"`
	Removals []SanitizerRemoval `json:"sanitizer_removals"`
	Invites  []Invite           `json:"invites"`
	Prune    []PruneItem        `json:"prune,omitempty"`
}

func newReport(dryRun bool) *Report {
	return &Report{
		DryRun: dryRun, Counts: map[string]*Counts{},
		Errors: []Issue{}, Warnings: []Issue{}, Removals: []SanitizerRemoval{}, Invites: []Invite{},
	}
}

func (r *Report) errorf(key, field, format string, args ...any) {
	r.Errors = append(r.Errors, Issue{LegacyKey: key, Field: field, Message: fmt.Sprintf(format, args...)})
}

func (r *Report) warnf(key, field, format string, args ...any) {
	r.Warnings = append(r.Warnings, Issue{LegacyKey: key, Field: field, Message: fmt.Sprintf(format, args...)})
}

func (r *Report) count(entity string) *Counts {
	c, ok := r.Counts[entity]
	if !ok {
		c = &Counts{}
		r.Counts[entity] = c
	}
	return c
}

// Summary lines for the CLI, sorted by entity name.
func (r *Report) Summary() []string {
	names := make([]string, 0, len(r.Counts))
	for n := range r.Counts {
		names = append(names, n)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, n := range names {
		c := r.Counts[n]
		lines = append(lines, fmt.Sprintf("%-20s created %d, updated %d, unchanged %d, skipped %d, deleted %d",
			n, c.Created, c.Updated, c.Unchanged, c.Skipped, c.Deleted))
	}
	return lines
}
