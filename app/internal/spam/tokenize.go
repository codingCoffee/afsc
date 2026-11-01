package spam

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var linkRe = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"']+|\bwww\.[^\s<>"']+`)

// Links returns the URLs in text, as the sanitizer would autolink them plus bare www. hosts.
func Links(text string) []string {
	return linkRe.FindAllString(text, -1)
}

// Host is the lowercased host of a URL or bare www. link, "" when there is none.
func Host(raw string) string {
	raw = strings.TrimRight(raw, ".,;:!?)")
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

// EmailDomain is the lowercased domain of an email address.
func EmailDomain(email string) string {
	if i := strings.LastIndexByte(email, '@'); i >= 0 {
		return strings.ToLower(email[i+1:])
	}
	return ""
}

// Tokens is the classifier's feature set for a message: lowercased body words of 3–20 runes, domain:<host> per
// body link, and from:<host> for the author URL and email domain. Each token appears once.
func Tokens(body, authorURL, email string) []string {
	set := map[string]struct{}{}
	for _, l := range Links(body) {
		if h := Host(l); h != "" {
			set["domain:"+strings.TrimPrefix(h, "www.")] = struct{}{}
		}
	}
	words := strings.FieldsFunc(linkRe.ReplaceAllString(body, " "), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\''
	})
	for _, w := range words {
		w = strings.ToLower(strings.Trim(w, "'"))
		if n := utf8.RuneCountInString(w); n >= 3 && n <= 20 {
			set[w] = struct{}{}
		}
	}
	if authorURL != "" {
		if h := Host(authorURL); h != "" {
			set["from:"+strings.TrimPrefix(h, "www.")] = struct{}{}
		}
	}
	if d := EmailDomain(email); d != "" {
		set["from:"+d] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}
