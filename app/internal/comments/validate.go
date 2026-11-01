package comments

import (
	"net/mail"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	maxName  = 100
	maxEmail = 254
	maxURL   = 500
	minBody  = 2
	maxBody  = 5000
)

// ValidationError lists bad form fields by name, with a message each.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "invalid comment" }

// normalize trims the input and validates it against §11: name required, email format only, URL http(s).
func normalize(in *Input) *ValidationError {
	bad := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.TrimSpace(in.Email)
	in.URL = strings.TrimSpace(in.URL)
	in.Body = strings.TrimSpace(strings.ReplaceAll(in.Body, "\r\n", "\n"))
	if n := utf8.RuneCountInString(in.Name); n == 0 || n > maxName {
		bad["author_name"] = "Name is required (up to 100 characters)."
	}
	if in.Email == "" || len(in.Email) > maxEmail {
		bad["author_email"] = "Email is required."
	} else if a, err := mail.ParseAddress(in.Email); err != nil || a.Address != in.Email {
		bad["author_email"] = "Email address is not valid."
	}
	if in.URL != "" {
		u, err := url.Parse(in.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(in.URL) > maxURL {
			bad["author_url"] = "Website must be an http(s) address."
		}
	}
	if n := utf8.RuneCountInString(in.Body); n < minBody || n > maxBody {
		bad["body"] = "Comment must be 2 to 5000 characters."
	}
	if !utf8.ValidString(in.Name + in.Email + in.URL + in.Body) {
		bad["body"] = "Comment contains invalid characters."
	}
	if len(bad) > 0 {
		return &ValidationError{Fields: bad}
	}
	return nil
}

// validBody checks a staff reply body.
func validBody(body string) (string, *ValidationError) {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if n := utf8.RuneCountInString(body); n < minBody || n > maxBody || !utf8.ValidString(body) {
		return "", &ValidationError{Fields: map[string]string{"body": "Reply must be 2 to 5000 characters."}}
	}
	return body, nil
}
