package transform

import (
	"fmt"
	"sort"
	"strconv"

	"afsc/migrators/afsc-wp/internal/mediaidx"
	"afsc/migrators/afsc-wp/internal/report"
)

type FlagStat struct {
	Count   int            `json:"count"`
	Posts   []string       `json:"posts"`
	Details map[string]int `json:"details"`
}

type CountRow struct {
	Entity  string         `json:"entity"`
	Source  int            `json:"source"`
	Skipped map[string]int `json:"skipped"`
	Bundle  int            `json:"bundle"`
}

type Report struct {
	Counts        []CountRow                     `json:"counts"`
	Skips         map[string]map[string][]string `json:"skips"`
	Flags         map[string]*FlagStat           `json:"flags"`
	Fidelity      []PostFidelity                 `json:"fidelity"`
	Media         *mediaidx.Report               `json:"media"`
	Redirects     RedirectReport                 `json:"redirects"`
	Notes         []string                       `json:"notes"`
	Issues        []string                       `json:"afsc_gaps"`
	SettingsNotes []string                       `json:"settings_notes"`
	Secrets       []string                       `json:"secret_option_names"`
	Fixes         int                            `json:"charset_fixes"`
}

func newReport() *Report {
	return &Report{Skips: map[string]map[string][]string{}, Flags: map[string]*FlagStat{}}
}

func (r *Report) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

func (r *Report) issue(s string) { r.Issues = append(r.Issues, s) }

func (r *Report) skip(entity, reason, key string) {
	if r.Skips[entity] == nil {
		r.Skips[entity] = map[string][]string{}
	}
	r.Skips[entity][reason] = append(r.Skips[entity][reason], key)
}

func (r *Report) flag(key, kind, detail string) {
	f := r.Flags[kind]
	if f == nil {
		f = &FlagStat{Details: map[string]int{}}
		r.Flags[kind] = f
	}
	f.Count++
	if len(f.Posts) == 0 || f.Posts[len(f.Posts)-1] != key {
		f.Posts = append(f.Posts, key)
	}
	if detail != "" {
		f.Details[detail]++
	}
}

// counts reconciles source rows, skips and bundle records per entity (§8.4).
func (s *state) counts(o *Output) []CountRow {
	var users, posts, comments, terms, attachments, files, menus, items int
	for _, p := range s.ds.Posts {
		switch p.Type {
		case "attachment":
			attachments++
		case "nav_menu_item":
			if p.Status == "publish" {
				items++
			}
		case "revision":
		default:
			posts++
		}
	}
	for _, f := range s.ds.Files {
		if f.Media {
			files++
		}
	}
	for _, t := range s.ds.Terms {
		if t.Taxonomy == "nav_menu" {
			menus++
		} else {
			terms++
		}
	}
	users, comments = len(s.ds.Users), len(s.ds.Comments)
	nItems := 0
	for _, m := range o.Menus {
		nItems += len(m.Items)
	}
	skips := func(e string) map[string]int {
		out := map[string]int{}
		for reason, keys := range s.rep.Skips[e] {
			out[reason] = len(keys)
		}
		return out
	}
	mediaSkips := map[string]int{
		"duplicate file (alias)":    len(s.mrep.Aliases),
		"file missing":              len(s.mrep.Missing),
		"type AFSC does not accept": len(s.mrep.Unsupported),
	}
	return []CountRow{
		{"users", users, skips("users"), len(o.Users)},
		{"authors", len(s.authorKey), nil, len(o.Authors)},
		{"terms", terms, skips("terms"), len(o.Terms)},
		{"posts (non-attachment, non-menu, excluding revisions and auto-drafts)", posts, skips("posts"), len(o.Posts)},
		{"media (attachments + loose media files, deduplicated by sha256)", attachments + files, mediaSkips, len(o.Media)},
		{"comments", comments, skips("comments"), len(o.Comments)},
		{"menus", menus, nil, len(o.Menus)},
		{"menu items (published)", items, skips("menu_items"), nItems},
		{"redirects", 0, nil, len(o.Redirects)},
		{"legacy records", len(o.Posts), nil, len(o.Legacy)},
	}
}

// Markdown renders reports/transform.md.
func (r *Report) Markdown() *report.Doc {
	d := report.New("Transform report")
	d.H2("Counts")
	var rows [][]string
	for _, c := range r.Counts {
		var sk []string
		for _, kv := range report.SortedCounts(c.Skipped) {
			sk = append(sk, kv[0]+": "+kv[1])
		}
		rows = append(rows, []string{c.Entity, strconv.Itoa(c.Source), report.Samples(sk, 10), strconv.Itoa(c.Bundle)})
	}
	d.Table([]string{"Entity", "Source", "Skipped", "Bundle"}, rows)
	d.H2("Skipped records")
	rows = nil
	for _, e := range sortedKeys(r.Skips) {
		for _, reason := range sortedKeys(r.Skips[e]) {
			keys := r.Skips[e][reason]
			rows = append(rows, []string{e, reason, strconv.Itoa(len(keys)), report.Samples(keys, 5)})
		}
	}
	d.Table([]string{"Entity", "Reason", "Count", "Samples"}, rows)

	d.H2("Content conversion flags")
	rows = nil
	for _, k := range sortedKeys(r.Flags) {
		f := r.Flags[k]
		var det []string
		for _, kv := range report.SortedCounts(f.Details) {
			det = append(det, kv[0]+" ×"+kv[1])
		}
		rows = append(rows, []string{k, strconv.Itoa(f.Count), report.Samples(f.Posts, 5), report.Samples(det, 6)})
	}
	d.Table([]string{"Flag", "Count", "Posts", "Details"}, rows)

	d.H2("Media")
	if m := r.Media; m != nil {
		var missing []string
		for _, x := range m.Missing {
			missing = append(missing, x.Key+" "+x.Path)
		}
		d.H3("Missing attachment files (cutover blockers until decided)").List(missing)
		d.H3("Duplicate attachments merged").P("%d aliases.", len(m.Aliases))
		d.H3("Paths AFSC can't serve (space, ? or #)").List(m.Unservable)
		d.H3("Attachments of a type AFSC doesn't accept").List(m.Unsupported)
		d.H3("Dimension mismatches (metadata vs file)").List(m.DimMismatch)
		d.H3("Path conflicts").List(m.PathConflicts)
		d.P("%d loose media files imported as hidden media (legacy paths only).", m.Orphans)
		var ni []string
		for _, f := range m.NotImported {
			ni = append(ni, fmt.Sprintf("`%s` (%d bytes): %s", f.Path, f.Size, f.Reason))
		}
		d.H3("Files under uploads/ that were NOT imported").
			P("These may be publicly reachable on production today and can contain secrets. Review every one.").List(ni)
	}

	d.H2("Redirects")
	rows = nil
	for _, kv := range report.SortedCounts(r.Redirects.ByNote) {
		rows = append(rows, []string{kv[0], kv[1]})
	}
	d.Table([]string{"Source", "Count"}, rows)
	var conf []string
	for _, c := range r.Redirects.Conflicts {
		conf = append(conf, fmt.Sprintf("`%s`: kept %s, dropped %s", c.Source, c.Kept, c.Lost))
	}
	d.H3("Conflicts").List(conf)
	d.H3("Dropped because a live page has the same path").List(r.Redirects.Shadowed)
	d.H3("Self-redirects dropped").List(r.Redirects.SelfLoops)
	d.H3("Yoast regexes RE2 can't express (decide redirect_regex)").List(r.Redirects.Unconvertible)
	if len(r.Redirects.OldDomains) > 0 {
		d.H3("Old domains (handle at the proxy, not in AFSC)").List(r.Redirects.OldDomains)
	}

	d.H2("Settings notes").List(r.SettingsNotes)
	d.H2("Gaps in AFSC found during this run").List(r.Issues)
	d.H2("Notes").List(r.Notes)
	d.H2("Credentials to rotate").P("Option names that look like credentials. Values were never copied.").List(r.Secrets)
	d.P("%d fields were repaired from double-encoded latin1 (see work/extract/fixes.ndjson).", r.Fixes)
	return d
}

// FidelityMarkdown renders the pre-import content check against the WordPress-filtered HTML.
func (r *Report) FidelityMarkdown() *report.Doc {
	d := report.New("Content fidelity (pre-import, against WordPress-filtered HTML)")
	byClass := map[string]int{}
	for _, f := range r.Fidelity {
		byClass[f.Class]++
	}
	var rows [][]string
	for _, kv := range report.SortedCounts(byClass) {
		rows = append(rows, []string{kv[0], kv[1]})
	}
	d.Table([]string{"Class", "Posts"}, rows)
	d.P("`verify --checks content` repeats this against the live AFSC render and the baseline crawl.")
	fid := append([]PostFidelity{}, r.Fidelity...)
	sort.Slice(fid, func(i, j int) bool { return keyLess(fid[i].Key, fid[j].Key) })
	rows = nil
	for _, f := range fid {
		if f.Class == "lossy" || f.Class == "fallback" {
			rows = append(rows, []string{f.Key, f.Class, report.Samples(f.Diffs, 3)})
		}
	}
	d.H2("Lossy and fallback posts").Table([]string{"Post", "Class", "Differences"}, rows)
	return d
}
