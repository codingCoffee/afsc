package spam

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/netip"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Kinds of spam_blocklist entries, in display order.
var Kinds = []string{"keyword", "domain", "ip", "cidr", "email"}

const (
	MaxBlocklistEntries = 5000
	maxEntryBytes       = 200
)

type Entry struct{ Kind, Value string }

var domainRe = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9-]{2,63}$`)

// NormalizeEntry validates an entry and returns its stored form.
func NormalizeEntry(kind, value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", errors.New("is empty")
	}
	if len(v) > maxEntryBytes {
		return "", fmt.Errorf("is longer than %d bytes", maxEntryBytes)
	}
	switch kind {
	case "keyword":
		return strings.ToLower(v), nil
	case "domain":
		v = strings.TrimSuffix(strings.ToLower(v), ".")
		if !domainRe.MatchString(v) {
			return "", errors.New("is not a domain name")
		}
		return v, nil
	case "ip":
		a, err := netip.ParseAddr(v)
		if err != nil {
			return "", errors.New("is not an IP address")
		}
		return a.Unmap().String(), nil
	case "cidr":
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return "", errors.New("is not a CIDR range")
		}
		return p.Masked().String(), nil
	case "email":
		a, err := mail.ParseAddress(v)
		if err != nil || a.Address != v {
			return "", errors.New("is not an email address")
		}
		return strings.ToLower(v), nil
	}
	return "", fmt.Errorf("unknown kind %q", kind)
}

func LoadBlocklist(ctx context.Context, db Querier) ([]Entry, error) {
	rows, err := db.Query(ctx, `SELECT kind, value FROM spam_blocklist ORDER BY kind, value`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Entry, error) {
		var e Entry
		err := r.Scan(&e.Kind, &e.Value)
		return e, err
	})
}

func domainMatch(host, d string) bool {
	return host == d || strings.HasSuffix(host, "."+d)
}

// MatchBlocklist returns a +5 reason for every entry the comment matches (§11 check 4).
func MatchBlocklist(list []Entry, in Input) []Reason {
	if len(list) == 0 {
		return nil
	}
	text := strings.ToLower(in.Body + "\n" + in.Name + "\n" + in.URL)
	var hosts []string
	for _, l := range Links(in.Body) {
		hosts = append(hosts, Host(l))
	}
	if in.URL != "" {
		hosts = append(hosts, Host(in.URL))
	}
	if d := EmailDomain(in.Email); d != "" {
		hosts = append(hosts, d)
	}
	ip := in.IP.Unmap()
	var out []Reason
	for _, e := range list {
		hit := false
		switch e.Kind {
		case "keyword":
			hit = strings.Contains(text, e.Value)
		case "domain":
			for _, h := range hosts {
				if h != "" && domainMatch(h, e.Value) {
					hit = true
					break
				}
			}
		case "ip":
			a, err := netip.ParseAddr(e.Value)
			hit = err == nil && ip.IsValid() && a.Unmap() == ip
		case "cidr":
			p, err := netip.ParsePrefix(e.Value)
			hit = err == nil && ip.IsValid() && p.Contains(ip)
		case "email":
			hit = in.Email != "" && strings.EqualFold(in.Email, e.Value)
		}
		if hit {
			out = append(out, Reason{Check: "blocklist", Delta: 5, Detail: e.Kind + " " + e.Value})
		}
	}
	return out
}

// ReplaceBlocklist replaces the whole list inside tx and returns the added and removed entries.
func ReplaceBlocklist(ctx context.Context, tx pgx.Tx, entries []Entry) (added, removed []Entry, err error) {
	old, err := LoadBlocklist(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	want := map[Entry]bool{}
	for _, e := range entries {
		want[e] = true
	}
	have := map[Entry]bool{}
	for _, e := range old {
		have[e] = true
		if !want[e] {
			removed = append(removed, e)
		}
	}
	for _, e := range entries {
		if !have[e] {
			added = append(added, e)
			have[e] = true
		}
	}
	for _, e := range removed {
		if _, err := tx.Exec(ctx, `DELETE FROM spam_blocklist WHERE kind = $1 AND value = $2`, e.Kind, e.Value); err != nil {
			return nil, nil, err
		}
	}
	for _, e := range added {
		if _, err := tx.Exec(ctx, `INSERT INTO spam_blocklist (kind, value) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			e.Kind, e.Value); err != nil {
			return nil, nil, err
		}
	}
	return added, removed, nil
}
