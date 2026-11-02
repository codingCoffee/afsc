// Package wpsettings maps WordPress options to AFSC settings.json (migration plan §5.3).
package wpsettings

import (
	"fmt"
	"strings"
	"time"
)

var phpToGo = map[byte]string{
	'd': "02", 'D': "Mon", 'j': "2", 'l': "Monday", 'F': "January", 'm': "01", 'M': "Jan", 'n': "1",
	'Y': "2006", 'y': "06", 'a': "pm", 'A': "PM", 'g': "3", 'h': "03", 'H': "15", 'i': "04", 's': "05",
	'O': "-0700", 'P': "-07:00", 'T': "MST",
}

// DateLayout converts a PHP date() format to a Go layout. It verifies the result against a PHP emulation on
// known dates, because literal text in the format can collide with Go's reference tokens.
func DateLayout(php string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(php); i++ {
		c := php[i]
		if c == '\\' && i+1 < len(php) {
			i++
			b.WriteByte(php[i])
			continue
		}
		if g, ok := phpToGo[c]; ok {
			b.WriteString(g)
			continue
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return "", fmt.Errorf("PHP date character %q has no Go equivalent", c)
		}
		b.WriteByte(c)
	}
	layout := b.String()
	for _, t := range []time.Time{
		time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC),
		time.Date(1999, 12, 31, 23, 59, 58, 0, time.UTC),
		time.Date(2024, 11, 9, 12, 0, 1, 0, time.UTC),
	} {
		if got, want := t.Format(layout), phpDate(php, t); got != want {
			return "", fmt.Errorf("Go layout %q renders %q, PHP renders %q", layout, got, want)
		}
	}
	return layout, nil
}

// phpDate emulates PHP's date() for the characters DateLayout supports.
func phpDate(format string, t time.Time) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c == '\\' && i+1 < len(format) {
			i++
			b.WriteByte(format[i])
			continue
		}
		h12 := t.Hour() % 12
		if h12 == 0 {
			h12 = 12
		}
		switch c {
		case 'd':
			fmt.Fprintf(&b, "%02d", t.Day())
		case 'D':
			b.WriteString(t.Weekday().String()[:3])
		case 'j':
			fmt.Fprintf(&b, "%d", t.Day())
		case 'l':
			b.WriteString(t.Weekday().String())
		case 'F':
			b.WriteString(t.Month().String())
		case 'm':
			fmt.Fprintf(&b, "%02d", int(t.Month()))
		case 'M':
			b.WriteString(t.Month().String()[:3])
		case 'n':
			fmt.Fprintf(&b, "%d", int(t.Month()))
		case 'Y':
			fmt.Fprintf(&b, "%04d", t.Year())
		case 'y':
			fmt.Fprintf(&b, "%02d", t.Year()%100)
		case 'a', 'A':
			s := "am"
			if t.Hour() >= 12 {
				s = "pm"
			}
			if c == 'A' {
				s = strings.ToUpper(s)
			}
			b.WriteString(s)
		case 'g':
			fmt.Fprintf(&b, "%d", h12)
		case 'h':
			fmt.Fprintf(&b, "%02d", h12)
		case 'H':
			fmt.Fprintf(&b, "%02d", t.Hour())
		case 'i':
			fmt.Fprintf(&b, "%02d", t.Minute())
		case 's':
			fmt.Fprintf(&b, "%02d", t.Second())
		case 'O':
			b.WriteString(t.Format("-0700"))
		case 'P':
			b.WriteString(t.Format("-07:00"))
		case 'T':
			b.WriteString(t.Format("MST"))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
