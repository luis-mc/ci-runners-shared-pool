package doctor

import "regexp"

// secretShapes are the strings that look like a secret (doctor R6.3,
// observability R6.5). The central redactor is the first line; this one keeps
// the doctor safe on its own because a check may quote a command line or an error.
var secretShapes = []*regexp.Regexp{
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`(?i)(authorization|cookie):\s*[^\r\n]+`),
	regexp.MustCompile(`(?i)(--token|--pat|--unattended\s+--token)\s+\S+`),
}

// Redact replaces secret-looking text with [redacted].
func Redact(s string) string {
	for _, re := range secretShapes {
		s = re.ReplaceAllString(s, "[redacted]")
	}
	return s
}

func redactFinding(f Finding) Finding {
	f.Title = Redact(f.Title)
	f.Detail = Redact(f.Detail)
	f.Fix.Text = Redact(f.Fix.Text)
	f.Fix.Command = Redact(f.Fix.Command)
	f.Fix.Link = Redact(f.Fix.Link)
	f.Subject.Name = Redact(f.Subject.Name)
	return f
}
