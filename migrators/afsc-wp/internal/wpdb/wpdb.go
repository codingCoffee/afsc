// Package wpdb reads a local WordPress restore into an extract.Dataset. It runs one read-only transaction,
// uses parameterized queries only, and interpolates nothing but the validated table prefix.
package wpdb

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"

	"afsc/migrators/afsc-wp/internal/config"
	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/phpser"
	"afsc/migrators/afsc-wp/internal/plugins"
)

// CoreOptions are loaded by value (migration plan §3.1). Other options are recorded by name only.
var CoreOptions = []string{
	"siteurl", "home", "blogname", "blogdescription", "permalink_structure", "category_base", "tag_base",
	"posts_per_page", "posts_per_rss", "rss_use_excerpt", "show_on_front", "page_on_front", "page_for_posts",
	"sticky_posts", "timezone_string", "gmt_offset", "date_format", "time_format", "blog_charset", "use_smilies",
	"template", "stylesheet", "active_plugins", "default_comment_status", "close_comments_for_old_posts",
	"close_comments_days_old", "thread_comments", "thread_comments_depth", "page_comments", "comments_per_page",
	"comment_registration", "WPLANG", "uploads_use_yearmonth_folders", "upload_path", "upload_url_path",
	"thumbnail_size_w", "thumbnail_size_h", "thumbnail_crop", "medium_size_w", "medium_size_h",
	"medium_large_size_w", "medium_large_size_h", "large_size_w", "large_size_h", "sidebars_widgets", "db_version",
}

// SecretOptionRe flags option names that commonly hold credentials. Only the names are ever reported.
var SecretOptionRe = regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret|token|passw|smtp|private[_-]?key|licen[cs]e|auth|salt|nonce|credential|akismet_key|wordpress_api_key|jetpack_private_options)`)

type DB struct {
	tx     *sql.Tx
	db     *sql.DB
	p      string
	source string
	latin  map[string]bool
	fixes  []extract.Fix
}

// Open connects with utf8mb4 and starts a read-only, repeatable-read transaction.
func Open(ctx context.Context, dsn, prefix, source string) (*DB, error) {
	if !config.PrefixRe.MatchString(prefix) {
		return nil, fmt.Errorf("invalid table prefix %q", prefix)
	}
	mc, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("mysql dsn: %w", err)
	}
	mc.Collation = "utf8mb4_unicode_ci"
	mc.ParseTime = false
	mc.InterpolateParams = false
	mc.MultiStatements = false
	conn, err := mysql.NewConnector(mc)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(conn)
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &DB{tx: tx, db: db, p: prefix, source: source, latin: map[string]bool{}}, nil
}

func (d *DB) Close() error {
	d.tx.Rollback()
	return d.db.Close()
}

func (d *DB) t(name string) string { return "`" + d.p + name + "`" }

func (d *DB) key(kind string, id int64) string {
	return d.source + ":" + kind + ":" + strconv.FormatInt(id, 10)
}

// fix repairs double-encoded text in fields read from latin1 tables and logs it by legacy key.
func (d *DB) fix(table, key, field string, s *string) {
	if !d.latin[table] {
		return
	}
	if out, ok := extract.FixMojibake(*s); ok {
		*s = out
		d.fixes = append(d.fixes, extract.Fix{LegacyKey: key, Field: field})
	}
}

// Load reads everything later phases need. Plugin adapters are resolved midway, so their keys drive what loads.
func Load(ctx context.Context, cfg *config.Config) (*extract.Dataset, *plugins.Set, error) {
	dsn, err := cfg.DSN()
	if err != nil {
		return nil, nil, err
	}
	d, err := Open(ctx, dsn, cfg.MySQL.TablePrefix, cfg.Source)
	if err != nil {
		return nil, nil, err
	}
	defer d.Close()

	ds := &extract.Dataset{Options: map[string]any{}, Prefix: cfg.MySQL.TablePrefix}
	if ds.Tables, err = d.tables(ctx); err != nil {
		return nil, nil, err
	}
	for _, t := range ds.Tables {
		d.latin[strings.TrimPrefix(t.Name, d.p)] = strings.HasPrefix(strings.ToLower(t.Collation), "latin1")
	}
	if err := d.stats(ctx, &ds.Stats); err != nil {
		return nil, nil, err
	}
	active, err := d.options(ctx, []string{"active_plugins"}, false)
	if err != nil {
		return nil, nil, err
	}
	var activeList []string
	if l, ok := active["active_plugins"].([]any); ok {
		for _, v := range l {
			if s, ok := v.(string); ok {
				activeList = append(activeList, s)
			}
		}
	}
	set := plugins.Resolve(cfg, activeList, append(append([]extract.Count{}, ds.Stats.PostMetaKeys...), ds.Stats.UserMetaKeys...))

	names := append(append([]string{}, CoreOptions...), plugins.OptionNames(set)...)
	if ds.Options, err = d.options(ctx, names, true); err != nil {
		return nil, nil, err
	}
	if ds.Posts, err = d.posts(ctx); err != nil {
		return nil, nil, err
	}
	if ds.Terms, err = d.terms(ctx); err != nil {
		return nil, nil, err
	}
	if err := d.relationships(ctx, ds); err != nil {
		return nil, nil, err
	}
	keep := plugins.UserMetaKeys(set, d.p)
	if ds.Users, err = d.users(ctx, keep); err != nil {
		return nil, nil, err
	}
	for _, c := range ds.Stats.UserMetaKeys {
		if !slices.Contains(keep, c.Key) {
			ds.Stats.SkippedUserMeta = append(ds.Stats.SkippedUserMeta, c.Key)
		}
	}
	slices.Sort(ds.Stats.SkippedUserMeta)
	if ds.Comments, err = d.comments(ctx); err != nil {
		return nil, nil, err
	}
	ds.Fixes = d.fixes
	ds.Sort()
	return ds, set, nil
}

func (d *DB) tables(ctx context.Context) ([]extract.Table, error) {
	like := strings.ReplaceAll(d.p, "_", `\_`) + "%"
	rows, err := d.tx.QueryContext(ctx, `SELECT table_name, table_collation FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name LIKE ? ORDER BY table_name`, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []extract.Table
	for rows.Next() {
		var name string
		var coll sql.NullString
		if err := rows.Scan(&name, &coll); err != nil {
			return nil, err
		}
		out = append(out, extract.Table{Name: name, Collation: coll.String})
	}
	return out, rows.Err()
}

func (d *DB) counts(ctx context.Context, q string, args ...any) ([]extract.Count, error) {
	rows, err := d.tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []extract.Count
	for rows.Next() {
		var c extract.Count
		var k, sub sql.NullString
		if len(cols) == 3 {
			err = rows.Scan(&k, &sub, &c.Count)
		} else {
			err = rows.Scan(&k, &c.Count)
		}
		if err != nil {
			return nil, err
		}
		c.Key, c.Sub = k.String, sub.String
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) stats(ctx context.Context, s *extract.Stats) error {
	var err error
	if s.PostTypeStatus, err = d.counts(ctx, `SELECT post_type, post_status, COUNT(*) FROM `+d.t("posts")+` GROUP BY post_type, post_status ORDER BY post_type, post_status`); err != nil {
		return err
	}
	if s.PostMetaKeys, err = d.counts(ctx, `SELECT meta_key, COUNT(*) FROM `+d.t("postmeta")+` GROUP BY meta_key ORDER BY meta_key`); err != nil {
		return err
	}
	if s.UserMetaKeys, err = d.counts(ctx, `SELECT meta_key, COUNT(*) FROM `+d.t("usermeta")+` GROUP BY meta_key ORDER BY meta_key`); err != nil {
		return err
	}
	if s.TermMetaKeys, err = d.counts(ctx, `SELECT meta_key, COUNT(*) FROM `+d.t("termmeta")+` GROUP BY meta_key ORDER BY meta_key`); err != nil {
		return err
	}
	if s.Taxonomies, err = d.counts(ctx, `SELECT taxonomy, COUNT(*) FROM `+d.t("term_taxonomy")+` GROUP BY taxonomy ORDER BY taxonomy`); err != nil {
		return err
	}
	if s.CommentTypes, err = d.counts(ctx, `SELECT comment_type, comment_approved, COUNT(*) FROM `+d.t("comments")+` GROUP BY comment_type, comment_approved ORDER BY comment_type, comment_approved`); err != nil {
		return err
	}
	if err := d.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+d.t("users")).Scan(&s.TotalUsers); err != nil {
		return err
	}
	rows, err := d.tx.QueryContext(ctx, `SELECT option_name FROM `+d.t("options")+` ORDER BY option_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		s.OptionNames = append(s.OptionNames, n)
		if SecretOptionRe.MatchString(n) {
			s.SecretOptions = append(s.SecretOptions, n)
		}
	}
	return rows.Err()
}

// options loads named options plus, when themed, every theme_mods_* and widget_* option.
func (d *DB) options(ctx context.Context, names []string, themed bool) (map[string]any, error) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	q := `SELECT option_name, option_value FROM ` + d.t("options") + ` WHERE option_name IN (` + ph + `)`
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	if themed {
		q += ` OR option_name LIKE ? OR option_name LIKE ?`
		args = append(args, `theme\_mods\_%`, `widget\_%`)
	}
	rows, err := d.tx.QueryContext(ctx, q+` ORDER BY option_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var name string
		var val sql.NullString
		if err := rows.Scan(&name, &val); err != nil {
			return nil, err
		}
		s := val.String
		d.fix("options", "option:"+name, "option_value", &s)
		v, err := phpser.Decode(s)
		if err != nil {
			return nil, fmt.Errorf("option %s: %w", name, err)
		}
		out[name] = v
	}
	return out, rows.Err()
}

func (d *DB) posts(ctx context.Context) ([]*extract.Post, error) {
	rows, err := d.tx.QueryContext(ctx, `SELECT ID, post_author, post_date, post_date_gmt, post_content, post_title,
		post_excerpt, post_status, comment_status, post_password, post_name, post_modified, post_modified_gmt,
		post_parent, guid, menu_order, post_type, post_mime_type FROM `+d.t("posts")+`
		WHERE post_type <> ? AND post_status <> ? ORDER BY ID`, "revision", "auto-draft")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*extract.Post
	byID := map[int64]*extract.Post{}
	for rows.Next() {
		p := &extract.Post{}
		var date, dateGMT, mod, modGMT, content, title, excerpt, pass, guid sql.NullString
		if err := rows.Scan(&p.ID, &p.Author, &date, &dateGMT, &content, &title, &excerpt, &p.Status, &p.CommentStatus,
			&pass, &p.Name, &mod, &modGMT, &p.Parent, &guid, &p.MenuOrder, &p.Type, &p.MimeType); err != nil {
			return nil, err
		}
		p.Date, p.DateGMT, p.Modified, p.ModifiedGMT = date.String, dateGMT.String, mod.String, modGMT.String
		p.Content, p.Title, p.Excerpt, p.Password, p.GUID = content.String, title.String, excerpt.String, pass.String, guid.String
		k := d.key("post", p.ID)
		d.fix("posts", k, "post_title", &p.Title)
		d.fix("posts", k, "post_content", &p.Content)
		d.fix("posts", k, "post_excerpt", &p.Excerpt)
		p.Slug = extract.DecodeSlug(p.Name)
		out = append(out, p)
		byID[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, d.meta(ctx, "postmeta", "post_id", "meta_id", func(id int64, k, v string) {
		if p := byID[id]; p != nil {
			d.fix("postmeta", d.key("post", id), "meta:"+k, &v)
			if p.Meta == nil {
				p.Meta = extract.Meta{}
			}
			p.Meta[k] = append(p.Meta[k], v)
		}
	}, nil)
}

func (d *DB) meta(ctx context.Context, table, idCol, orderCol string, fn func(id int64, k, v string), only []string) error {
	q := `SELECT ` + idCol + `, meta_key, meta_value FROM ` + d.t(table)
	var args []any
	if only != nil {
		if len(only) == 0 {
			return nil
		}
		q += ` WHERE meta_key IN (` + strings.TrimSuffix(strings.Repeat("?,", len(only)), ",") + `)`
		for _, k := range only {
			args = append(args, k)
		}
	}
	rows, err := d.tx.QueryContext(ctx, q+` ORDER BY `+orderCol, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var k, v sql.NullString
		if err := rows.Scan(&id, &k, &v); err != nil {
			return err
		}
		fn(id, k.String, v.String)
	}
	return rows.Err()
}

func (d *DB) terms(ctx context.Context) ([]*extract.Term, error) {
	rows, err := d.tx.QueryContext(ctx, `SELECT tt.term_taxonomy_id, t.term_id, t.name, t.slug, tt.taxonomy, tt.description,
		tt.parent, tt.count FROM `+d.t("term_taxonomy")+` tt JOIN `+d.t("terms")+` t ON t.term_id = tt.term_id
		ORDER BY tt.term_taxonomy_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*extract.Term
	byTerm := map[int64][]*extract.Term{}
	for rows.Next() {
		t := &extract.Term{}
		var desc sql.NullString
		if err := rows.Scan(&t.TermTaxID, &t.TermID, &t.Name, &t.RawSlug, &t.Taxonomy, &desc, &t.Parent, &t.Count); err != nil {
			return nil, err
		}
		t.Description = desc.String
		k := d.key("term", t.TermID)
		d.fix("terms", k, "name", &t.Name)
		d.fix("term_taxonomy", k, "description", &t.Description)
		t.Slug = extract.DecodeSlug(t.RawSlug)
		out = append(out, t)
		byTerm[t.TermID] = append(byTerm[t.TermID], t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, d.meta(ctx, "termmeta", "term_id", "meta_id", func(id int64, k, v string) {
		for _, t := range byTerm[id] {
			if t.Meta == nil {
				t.Meta = extract.Meta{}
			}
			t.Meta[k] = append(t.Meta[k], v)
		}
	}, nil)
}

func (d *DB) relationships(ctx context.Context, ds *extract.Dataset) error {
	rows, err := d.tx.QueryContext(ctx, `SELECT object_id, term_taxonomy_id FROM `+d.t("term_relationships")+` ORDER BY object_id, term_taxonomy_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var obj, tt int64
		if err := rows.Scan(&obj, &tt); err != nil {
			return err
		}
		if p := ds.Post(obj); p != nil {
			p.TermTaxIDs = append(p.TermTaxIDs, tt)
		}
	}
	return rows.Err()
}

func (d *DB) users(ctx context.Context, keep []string) ([]*extract.User, error) {
	rows, err := d.tx.QueryContext(ctx, `SELECT ID, user_login, user_nicename, user_email, user_url, user_registered,
		display_name FROM `+d.t("users")+` ORDER BY ID`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*extract.User
	byID := map[int64]*extract.User{}
	for rows.Next() {
		u := &extract.User{}
		var reg sql.NullString
		if err := rows.Scan(&u.ID, &u.Login, &u.Nicename, &u.Email, &u.URL, &reg, &u.DisplayName); err != nil {
			return nil, err
		}
		u.Registered = reg.String
		d.fix("users", d.key("user", u.ID), "display_name", &u.DisplayName)
		out = append(out, u)
		byID[u.ID] = u
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, d.meta(ctx, "usermeta", "user_id", "umeta_id", func(id int64, k, v string) {
		if u := byID[id]; u != nil {
			d.fix("usermeta", d.key("user", id), "meta:"+k, &v)
			if u.Meta == nil {
				u.Meta = extract.Meta{}
			}
			u.Meta[k] = append(u.Meta[k], v)
		}
	}, keep)
}

func (d *DB) comments(ctx context.Context) ([]*extract.Comment, error) {
	rows, err := d.tx.QueryContext(ctx, `SELECT comment_ID, comment_post_ID, comment_author, comment_author_email,
		comment_author_url, comment_author_IP, comment_date, comment_date_gmt, comment_content, comment_approved,
		comment_agent, comment_type, comment_parent, user_id FROM `+d.t("comments")+` ORDER BY comment_ID`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*extract.Comment
	for rows.Next() {
		c := &extract.Comment{}
		var author, email, url, ip, date, dateGMT, content, agent, typ sql.NullString
		if err := rows.Scan(&c.ID, &c.PostID, &author, &email, &url, &ip, &date, &dateGMT, &content, &c.Approved,
			&agent, &typ, &c.Parent, &c.UserID); err != nil {
			return nil, err
		}
		c.Author, c.AuthorEmail, c.AuthorURL, c.AuthorIP = author.String, email.String, url.String, ip.String
		c.Date, c.DateGMT, c.Content, c.Agent, c.Type = date.String, dateGMT.String, content.String, agent.String, typ.String
		k := d.key("comment", c.ID)
		d.fix("comments", k, "comment_author", &c.Author)
		d.fix("comments", k, "comment_content", &c.Content)
		out = append(out, c)
	}
	return out, rows.Err()
}
