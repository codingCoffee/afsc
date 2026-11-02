// Package mediaidx turns attachments and loose uploads into media records with legacy paths (migration plan
// §5.2, §7.1), and indexes every URL form of every file so content references resolve (§6.5).
package mediaidx

import (
	"fmt"
	"math"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/phpser"
	"afsc/migrators/afsc-wp/internal/records"
)

// Ref is what an uploads path resolves to.
type Ref struct {
	Key    string
	Size   string // WordPress size name; "" for the original
	Width  int
	Height int
}

type Options struct {
	Source      string
	UploadsPath string   // "/wp-content/uploads"
	HomePath    string   // site path stripped from served legacy paths ("/varsity"); content URLs keep it
	SiteHosts   []string // every host that serves this site
	CDNHosts    []string // Jetpack Photon hosts
	Loc         *time.Location
	Keys        struct{ AttachedFile, Metadata, Alt string }
}

type Missing struct {
	Key  string `json:"legacy_key"`
	Path string `json:"path"`
}

type Report struct {
	Missing       []Missing         `json:"missing"`
	Aliases       map[string]string `json:"aliases"`
	Unservable    []string          `json:"unservable_paths"`
	Unsupported   []string          `json:"unsupported_attachments"`
	DimMismatch   []string          `json:"dimension_mismatches"`
	PathConflicts []string          `json:"path_conflicts"`
	Orphans       int               `json:"orphan_files"`
	NotImported   []*extract.File   `json:"not_imported"`
	RenamedTypes  []string          `json:"sniffed_type_differs"`
}

type Index struct {
	opts       Options
	hosts      map[string]bool
	cdn        map[string]bool
	byRel      map[string]Ref
	attachment map[int64]string
	attached   map[int64][]int64
	byKey      map[string]*records.Media
	files      map[string]*extract.File
}

var badPathChars = " ?#\t\n"

// Build creates media records for every attachment and every loose media file under uploads.
func Build(ds *extract.Dataset, o Options) (*Index, []*records.Media, *Report) {
	ix := &Index{opts: o, hosts: set(o.SiteHosts), cdn: set(o.CDNHosts), byRel: map[string]Ref{},
		attachment: map[int64]string{}, attached: map[int64][]int64{}, byKey: map[string]*records.Media{}, files: map[string]*extract.File{}}
	rep := &Report{Aliases: map[string]string{}}
	loc := o.Loc
	if loc == nil {
		loc = time.UTC
	}
	for _, f := range ds.Files {
		ix.files[f.Path] = f
		if !f.Media {
			rep.NotImported = append(rep.NotImported, f)
		}
		if f.Reason != "" && f.Media {
			rep.RenamedTypes = append(rep.RenamedTypes, f.Path)
		}
	}
	var out []*records.Media
	bySHA := map[string]*records.Media{}
	claimed := map[string]string{}
	seenPath := map[string]bool{}

	addPath := func(m *records.Media, rel string, lp records.LegacyPath, ref Ref) {
		if owner, ok := claimed[rel]; ok && owner != m.LegacyKey {
			rep.PathConflicts = append(rep.PathConflicts, fmt.Sprintf("%s claimed by %s and %s", rel, owner, m.LegacyKey))
			return
		}
		claimed[rel] = m.LegacyKey
		if _, ok := ix.byRel[rel]; !ok {
			ix.byRel[rel] = ref
		}
		lp.Path = strings.TrimPrefix(o.UploadsPath, o.HomePath) + "/" + rel
		if strings.ContainsAny(lp.Path, badPathChars) || strings.Contains(lp.Path, "..") {
			rep.Unservable = append(rep.Unservable, lp.Path)
			return
		}
		if seenPath[lp.Path] {
			return
		}
		seenPath[lp.Path] = true
		m.LegacyPaths = append(m.LegacyPaths, lp)
	}

	for _, p := range ds.Posts {
		if p.Type != "attachment" {
			continue
		}
		key := o.Source + ":post:" + strconv.FormatInt(p.ID, 10)
		attached := normalizeRel(p.Meta.First(o.Keys.AttachedFile))
		meta, _ := phpser.Decode(p.Meta.First(o.Keys.Metadata))
		md, _ := meta.(map[string]any)
		origRel, scaledRel := attached, ""
		if orig, _ := md["original_image"].(string); orig != "" && attached != "" {
			origRel, scaledRel = path.Join(path.Dir(attached), orig), attached
		}
		f := ix.files[origRel]
		if (f == nil || !f.Media) && scaledRel != "" {
			if sf := ix.files[scaledRel]; sf != nil && sf.Media {
				f, origRel, scaledRel = sf, scaledRel, ""
			}
		}
		switch {
		case attached == "":
			rep.Missing = append(rep.Missing, Missing{Key: key, Path: "(no " + o.Keys.AttachedFile + ")"})
			continue
		case f == nil:
			rep.Missing = append(rep.Missing, Missing{Key: key, Path: o.UploadsPath + "/" + origRel})
			ix.byRel[origRel] = Ref{Key: ""}
			continue
		case !f.Media:
			rep.Unsupported = append(rep.Unsupported, key)
			continue
		}
		if p.Parent > 0 && strings.HasPrefix(p.MimeType, "image/") {
			ix.attached[p.Parent] = append(ix.attached[p.Parent], p.ID)
		}

		m, dup := bySHA[f.SHA256]
		if dup {
			rep.Aliases[key] = m.LegacyKey
			ix.attachment[p.ID] = m.LegacyKey
		} else {
			created, _ := extract.UTC(p.DateGMT, p.Date, loc)
			m = &records.Media{
				LegacyKey: key, SHA256: f.SHA256, Filename: path.Base(origRel), Mime: f.Mime, Title: p.Title,
				Alt: p.Meta.First(o.Keys.Alt), Caption: p.Excerpt, Description: p.Content, SourcePath: origRel,
				LibraryVisible: records.Ptr(true),
			}
			if !created.IsZero() {
				m.CreatedAt = &created
			}
			if f.Width > 0 {
				m.Width, m.Height = records.Ptr(f.Width), records.Ptr(f.Height)
			}
			if mw, mh := num(md["width"]), num(md["height"]); scaledRel == "" && mw > 0 && f.Width > 0 && (mw != f.Width || mh != f.Height) {
				rep.DimMismatch = append(rep.DimMismatch, key)
			}
			bySHA[f.SHA256] = m
			ix.byKey[key] = m
			ix.attachment[p.ID] = key
			out = append(out, m)
		}
		addPath(m, origRel, records.LegacyPath{Variant: "orig"}, Ref{Key: m.LegacyKey, Width: f.Width, Height: f.Height})
		ow, oh := f.Width, f.Height
		if scaledRel != "" {
			w, h := num(md["width"]), num(md["height"])
			if w > 0 && h > 0 {
				addPath(m, scaledRel, resize(w, h, ow, oh), Ref{Key: m.LegacyKey, Size: "full", Width: w, Height: h})
			}
		}
		dir := path.Dir(attached)
		sizes, _ := md["sizes"].(map[string]any)
		names := make([]string, 0, len(sizes))
		for n := range sizes {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			s, _ := sizes[n].(map[string]any)
			file, _ := s["file"].(string)
			w, h := num(s["width"]), num(s["height"])
			if file == "" || w <= 0 || h <= 0 {
				continue
			}
			addPath(m, path.Join(dir, file), resize(w, h, ow, oh), Ref{Key: m.LegacyKey, Size: n, Width: w, Height: h})
		}
	}

	for _, f := range ds.Files {
		if !f.Media || claimed[f.Path] != "" {
			continue
		}
		m, dup := bySHA[f.SHA256]
		if !dup {
			if strings.ContainsAny(f.Path, badPathChars) {
				rep.Unservable = append(rep.Unservable, o.UploadsPath+"/"+f.Path)
				continue
			}
			rep.Orphans++
			m = &records.Media{
				LegacyKey: o.Source + ":file:" + f.SHA256[:16], SHA256: f.SHA256, Filename: path.Base(f.Path),
				Mime: f.Mime, LibraryVisible: records.Ptr(false), SourcePath: f.Path,
			}
			if f.Width > 0 {
				m.Width, m.Height = records.Ptr(f.Width), records.Ptr(f.Height)
			}
			bySHA[f.SHA256] = m
			ix.byKey[m.LegacyKey] = m
			out = append(out, m)
		}
		addPath(m, f.Path, records.LegacyPath{Variant: "orig"}, Ref{Key: m.LegacyKey, Width: f.Width, Height: f.Height})
	}
	for _, ids := range ix.attached {
		sort.Slice(ids, func(i, j int) bool {
			a, b := ds.Post(ids[i]), ds.Post(ids[j])
			if a.MenuOrder != b.MenuOrder {
				return a.MenuOrder < b.MenuOrder
			}
			return a.ID < b.ID
		})
	}
	sort.Slice(out, func(i, j int) bool { return keyLess(out[i].LegacyKey, out[j].LegacyKey) })
	sort.Strings(rep.Unservable)
	return ix, out, rep
}

func resize(w, h, ow, oh int) records.LegacyPath {
	lp := records.LegacyPath{Variant: "resize", Width: records.Ptr(w), Height: records.Ptr(h)}
	if ow > 0 && oh > 0 {
		a, b := float64(w)/float64(h), float64(ow)/float64(oh)
		lp.Crop = math.Abs(a-b)/b > 0.01
	}
	return lp
}

func num(v any) int {
	switch x := v.(type) {
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

func set(l []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range l {
		m[strings.ToLower(s)] = true
	}
	return m
}

// normalizeRel makes _wp_attached_file relative: very old sites stored absolute filesystem paths.
func normalizeRel(p string) string {
	if i := strings.Index(p, "/uploads/"); i >= 0 && strings.HasPrefix(p, "/") {
		p = p[i+len("/uploads/"):]
	}
	return strings.TrimPrefix(p, "/")
}

// KeyLess orders legacy keys by kind, then numerically by id where ids are numbers.
func keyLess(a, b string) bool {
	ka, ia := splitKey(a)
	kb, ib := splitKey(b)
	if ka != kb {
		return ka < kb
	}
	na, ea := strconv.ParseInt(ia, 10, 64)
	nb, eb := strconv.ParseInt(ib, 10, 64)
	if ea == nil && eb == nil {
		return na < nb
	}
	return ia < ib
}

// KeyLess is exported for other packages that sort records.
func KeyLess(a, b string) bool { return keyLess(a, b) }

func splitKey(k string) (string, string) {
	i := strings.LastIndexByte(k, ':')
	if i < 0 {
		return k, ""
	}
	return k[:i], k[i+1:]
}

var sizeSuffixRe = regexp.MustCompile(`^(.*)-(\d+)x(\d+)(\.[A-Za-z0-9]+)$`)

// RelPath maps a URL in any known form to an uploads-relative, decoded path.
func (ix *Index) RelPath(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	p := u.Path
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "":
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
	case ix.cdn[host]:
		// Photon: https://i0.wp.com/{site host}/{path}
		rest := strings.TrimPrefix(p, "/")
		h, after, ok := strings.Cut(rest, "/")
		if !ok || !ix.hosts[strings.ToLower(h)] {
			return "", false
		}
		p = "/" + after
	case ix.hosts[host]:
	default:
		return "", false
	}
	rel, ok := strings.CutPrefix(p, ix.opts.UploadsPath+"/")
	if !ok {
		return "", false
	}
	return rel, true
}

// Resolve finds the media behind a URL. Unknown size suffixes resolve to their base image.
func (ix *Index) Resolve(raw string) (Ref, string, bool) {
	rel, ok := ix.RelPath(raw)
	if !ok {
		return Ref{}, "", false
	}
	if r, ok := ix.byRel[rel]; ok {
		return r, rel, r.Key != ""
	}
	if m := sizeSuffixRe.FindStringSubmatch(rel); m != nil {
		base := m[1] + m[4]
		for _, cand := range []string{base, m[1] + "-scaled" + m[4]} {
			if r, ok := ix.byRel[cand]; ok && r.Key != "" {
				w, _ := strconv.Atoi(m[2])
				h, _ := strconv.Atoi(m[3])
				return Ref{Key: r.Key, Size: "", Width: w, Height: h}, rel, true
			}
		}
	}
	return Ref{}, rel, false
}

// IsUpload reports whether a URL points into uploads on a site host, whether or not it resolves.
func (ix *Index) IsUpload(raw string) bool {
	_, ok := ix.RelPath(raw)
	return ok
}

// Attachment returns the canonical media key for an attachment id.
func (ix *Index) Attachment(id int64) (string, bool) {
	k, ok := ix.attachment[id]
	return k, ok
}

// AttachedImages lists image attachments of a post in WordPress gallery order (menu_order, ID).
func (ix *Index) AttachedImages(parent int64) []int64 { return ix.attached[parent] }

// Media returns the record for a canonical key.
func (ix *Index) Media(key string) *records.Media { return ix.byKey[key] }

// OrigPath is the public legacy path of a media item's original file.
func (ix *Index) OrigPath(key string) string {
	if m := ix.byKey[key]; m != nil {
		for _, lp := range m.LegacyPaths {
			if lp.Variant == "orig" {
				return lp.Path
			}
		}
	}
	return ""
}

// Known reports whether rel (uploads-relative) was seen at all, even as a missing attachment file.
func (ix *Index) Known(rel string) bool {
	_, ok := ix.byRel[rel]
	return ok
}

// FileExists reports whether an uploads-relative file exists in the scanned copy.
func (ix *Index) FileExists(rel string) bool { return ix.files[rel] != nil }
