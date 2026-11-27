// Package output prints tables and JSON for the CLI (cli R5.10, R5.11, R5.13,
// R6.7). It never writes colour: no meaning rests on colour alone, so the
// no-colour rule holds on every terminal.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxWidth is the width default list output must fit (cli R6.7).
const MaxWidth = 80

// Table writes an aligned table with a header row. Cells that would push a row
// past width columns are cut with an ellipsis; the last column takes the cut.
func Table(w io.Writer, width int, headers []string, rows [][]string) {
	if width <= 0 {
		width = MaxWidth
	}
	cols := len(headers)
	widths := make([]int, cols)
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i := 0; i < cols && i < len(r); i++ {
			if n := utf8.RuneCountInString(r[i]); n > widths[i] {
				widths[i] = n
			}
		}
	}
	// Shrink the widest column until the row fits.
	for {
		total := 2 * (cols - 1)
		widest := 0
		for i, n := range widths {
			total += n
			if n > widths[widest] {
				widest = i
			}
		}
		if total <= width || widths[widest] <= 4 {
			break
		}
		widths[widest]--
	}
	line := func(cells []string) {
		var b strings.Builder
		for i := 0; i < cols; i++ {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			c = Cut(c, widths[i])
			b.WriteString(c)
			if i < cols-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c)+2))
			}
		}
		_, _ = fmt.Fprintln(w, b.String())
	}
	line(headers)
	for _, r := range rows {
		line(r)
	}
}

// Cut shortens s to n runes, ending in an ellipsis when it had to cut.
func Cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// JSON writes v as one indented JSON document (cli R5.11).
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Line writes v as one compact JSON line, for job watch --json.
func Line(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

// Time formats a time as 2026-11-02 14:30 in UTC, or "-" when absent.
func Time(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// Number formats n with a thin space between thousands: 12 345.
func Number(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, " ")
	if neg {
		out = "-" + out
	}
	return out
}

// Percent formats a load as 45%, or "-" when unknown.
func Percent(p *float32) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(int(*p+0.5)) + "%"
}

// Age formats how long ago t was, as 5 min, 3 h or 2 d.
func Age(t *time.Time, now time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	d := now.Sub(*t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%d d", int(d.Hours()/24))
	}
}

// Dash returns "-" for an empty string.
func Dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
