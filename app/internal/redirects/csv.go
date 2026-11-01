package redirects

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Columns is the CSV header, in export order. Imports accept the columns in any order.
var Columns = []string{"source", "match", "target", "status", "note"}

const (
	MaxCSVBytes = 5 << 20
	MaxCSVRows  = 10000
)

// LineError is a problem with one CSV line (1 = the header).
type LineError struct {
	Line    int    `json:"line"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// Line is one parsed CSV row.
type Line struct {
	N     int
	Input Input
}

// spreadsheet apps run cells that start with these as formulas
func risky(s string) bool { return s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) }

func defang(s string) string {
	if risky(s) {
		return "'" + s
	}
	return s
}

func undefang(s string) string {
	if len(s) > 1 && s[0] == '\'' && risky(s[1:]) {
		return s[1:]
	}
	return s
}

// WriteCSV writes rules with the Columns header. Cells that a spreadsheet would run as a formula get a leading '.
func WriteCSV(w io.Writer, rules []*Rule) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(Columns); err != nil {
		return err
	}
	for _, r := range rules {
		if err := cw.Write([]string{defang(r.Source), r.Match, defang(r.Target), strconv.Itoa(r.Status), defang(r.Note)}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ReadCSV parses an import file. It needs the source column; match defaults to exact, status to 301, and a blank
// target means none. Row problems are returned as LineErrors; a malformed file is an error.
func ReadCSV(r io.Reader) ([]Line, []LineError, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	head, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil, errors.New("the file is empty")
	}
	if err != nil {
		return nil, nil, err
	}
	col := map[string]int{}
	for i, h := range head {
		h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		if !slices.Contains(Columns, h) {
			return nil, nil, fmt.Errorf("unknown column %q; the columns are %s", h, strings.Join(Columns, ", "))
		}
		if _, dup := col[h]; dup {
			return nil, nil, fmt.Errorf("column %q appears twice", h)
		}
		col[h] = i
	}
	if _, ok := col["source"]; !ok {
		return nil, nil, errors.New("the source column is required")
	}

	var lines []Line
	var errs []LineError
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		line, _ := cr.FieldPos(0)
		if len(lines) >= MaxCSVRows {
			return nil, nil, fmt.Errorf("more than %d rows", MaxCSVRows)
		}
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(rec) {
				return undefang(strings.TrimSpace(rec[i]))
			}
			return ""
		}
		if strings.Join(rec, "") == "" {
			continue
		}
		in := Input{Source: get("source"), Match: get("match"), Note: get("note")}
		if t := get("target"); t != "" {
			in.Target = &t
		}
		if s := get("status"); s != "" {
			if in.Status, err = strconv.Atoi(s); err != nil {
				errs = append(errs, LineError{Line: line, Field: "status", Message: "must be a number"})
				continue
			}
		}
		lines = append(lines, Line{N: line, Input: in})
	}
	return lines, errs, nil
}
