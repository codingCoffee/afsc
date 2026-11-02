package extract

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// cp1252 maps the runes MySQL's "latin1" (really Windows-1252) decodes bytes 0x80–0x9F to.
var cp1252 = map[rune]byte{
	'€': 0x80, '‚': 0x82, 'ƒ': 0x83, '„': 0x84, '…': 0x85, '†': 0x86, '‡': 0x87, 'ˆ': 0x88, '‰': 0x89,
	'Š': 0x8A, '‹': 0x8B, 'Œ': 0x8C, 'Ž': 0x8E, '‘': 0x91, '’': 0x92, '“': 0x93, '”': 0x94, '•': 0x95,
	'–': 0x96, '—': 0x97, '˜': 0x98, '™': 0x99, 'š': 0x9A, '›': 0x9B, 'œ': 0x9C, 'ž': 0x9E, 'Ÿ': 0x9F,
	'\u0081': 0x81, '\u008D': 0x8D, '\u008F': 0x8F, '\u0090': 0x90, '\u009D': 0x9D,
}

func toCP1252(r rune) (byte, bool) {
	if r < 0x80 || (r >= 0xA0 && r <= 0xFF) {
		return byte(r), true
	}
	b, ok := cp1252[r]
	return b, ok
}

// mojibakeScore counts UTF-8 lead/continuation byte pairs that appear as separate characters.
func mojibakeScore(s string) int {
	n := 0
	var prev rune = -1
	for _, r := range s {
		if prev >= 0xC2 && prev <= 0xF4 {
			if b, ok := toCP1252(r); ok && b >= 0x80 && b <= 0xBF {
				n++
			}
		}
		prev = r
	}
	return n
}

// FixMojibake repairs UTF-8 that was stored as latin1 and decoded twice. It only changes s when the
// re-decoded bytes are valid UTF-8 and the mojibake signature count drops.
func FixMojibake(s string) (string, bool) {
	before := mojibakeScore(s)
	if before == 0 {
		return s, false
	}
	b := make([]byte, 0, len(s))
	for _, r := range s {
		c, ok := toCP1252(r)
		if !ok {
			return s, false
		}
		b = append(b, c)
	}
	if !utf8.Valid(b) {
		return s, false
	}
	out := string(b)
	if mojibakeScore(out) >= before {
		return s, false
	}
	return out, true
}

// DecodeSlug percent-decodes a WordPress slug. It returns the input when it isn't valid encoding.
func DecodeSlug(raw string) string {
	if !strings.Contains(raw, "%") {
		return raw
	}
	d, err := url.PathUnescape(raw)
	if err != nil || !utf8.ValidString(d) {
		return raw
	}
	return strings.ToLower(d)
}

const mysqlLayout = "2006-01-02 15:04:05"

// ParseDate reads a MySQL DATETIME in loc. All-zero dates return the zero time.
func ParseDate(s string, loc *time.Location) (time.Time, error) {
	if s == "" || strings.HasPrefix(s, "0000-00-00") {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation(mysqlLayout, s, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad date %q", s)
	}
	return t, nil
}

// UTC returns gmt when set, otherwise local converted from the site timezone.
func UTC(gmt, local string, loc *time.Location) (time.Time, error) {
	t, err := ParseDate(gmt, time.UTC)
	if err != nil || !t.IsZero() {
		return t.UTC(), err
	}
	t, err = ParseDate(local, loc)
	return t.UTC(), err
}

// SiteLocation returns the WordPress site timezone: timezone_string, else a fixed zone from gmt_offset.
// fixed reports that gmt_offset was used, which loses DST rules.
func SiteLocation(timezoneString, gmtOffset string) (loc *time.Location, name string, fixed bool, err error) {
	if timezoneString != "" && !strings.HasPrefix(timezoneString, "UTC") {
		l, err := time.LoadLocation(timezoneString)
		if err != nil {
			return nil, "", false, err
		}
		return l, timezoneString, false, nil
	}
	off := gmtOffset
	if timezoneString != "" {
		off = strings.TrimPrefix(timezoneString, "UTC")
	}
	var hours float64
	if off != "" {
		if _, err := fmt.Sscanf(off, "%g", &hours); err != nil {
			return nil, "", false, fmt.Errorf("bad gmt_offset %q", off)
		}
	}
	if hours == 0 {
		return time.UTC, "UTC", off != "", nil
	}
	secs := int(hours * 3600)
	if hours == float64(int(hours)) {
		// IANA Etc/GMT zones have inverted signs.
		name = fmt.Sprintf("Etc/GMT%+d", -int(hours))
		if l, err := time.LoadLocation(name); err == nil {
			return l, name, true, nil
		}
	}
	return time.FixedZone(fmt.Sprintf("UTC%+g", hours), secs), "", true, nil
}
