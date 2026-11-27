package windows

import (
	"html"
	"regexp"
	"strings"
)

// errMarker starts the whole error text a wrapped script writes to stderr.
const errMarker = "CRSP-ERROR: "

// wrapScript runs a script inside try/catch. A failure writes one plain line
// on stderr with the message of the exception and of every inner exception,
// so PowerShell never sends its CLIXML error stream and the reason that sits
// in an inner exception (for example the logon failure behind "cannot be
// started") is not lost.
//
// The script goes to `powershell -Command -` on stdin. There PowerShell reads
// line by line and runs a multi-line statement only when a blank line follows
// it, so the text must end with a blank line (OPS92) and must not hold one
// inside: a blank line inside the open try block would end it early.
func wrapScript(script string) string {
	return "try {\n" + dropBlankLines(script) + "\n} catch {\n" +
		"    $m = @(); $e = $_.Exception\n" +
		"    while ($e) { $t = ($e.Message -replace '\\s+', ' ').Trim(); if ($t -and $m -notcontains $t) { $m += $t }; $e = $e.InnerException }\n" +
		"    [Console]::Error.WriteLine('" + errMarker + "' + ($m -join ' | '))\n" +
		"    exit 1\n}\n\n"
}

// dropBlankLines removes the empty and whitespace-only lines of a script.
func dropBlankLines(script string) string {
	lines := strings.Split(script, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

var (
	clixmlString = regexp.MustCompile(`(?s)<S S="[^"]*">(.*?)</S>`)
	spaces       = regexp.MustCompile(`\s+`)
)

// plainError turns what a failed PowerShell or tool run printed into one whole
// line of plain text: the wrapped script's own message when there is one, else
// the text with any CLIXML decoded. Nothing is cut short.
func plainError(stderr, stdout string) string {
	for _, s := range []string{stderr, stdout} {
		if i := strings.LastIndex(s, errMarker); i >= 0 {
			return collapse(s[i+len(errMarker):])
		}
	}
	var parts []string
	for _, s := range []string{stderr, stdout} {
		if strings.Contains(s, "#< CLIXML") {
			for _, m := range clixmlString.FindAllStringSubmatch(s, -1) {
				t := html.UnescapeString(m[1])
				t = strings.NewReplacer("_x000D__x000A_", " ", "_x000D_", " ", "_x000A_", " ").Replace(t)
				parts = append(parts, t)
			}
			continue
		}
		parts = append(parts, s)
	}
	return collapse(strings.Join(parts, " "))
}

func collapse(s string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
}
