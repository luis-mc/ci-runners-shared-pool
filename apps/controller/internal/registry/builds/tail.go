package builds

import "strings"

const (
	tailLines = 80
	tailBytes = 8 << 10
)

// Tail keeps the last lines of a build log, redacted and capped (R5.69). The
// redactor runs on each line before it is kept, so a secret never sits in
// memory beyond the line that carried it.
type Tail struct {
	redact func(string) string
	lines  []string
}

// NewTail makes a tail; a nil redactor keeps lines as they are.
func NewTail(redact func(string) string) *Tail {
	if redact == nil {
		redact = func(s string) string { return s }
	}
	return &Tail{redact: redact}
}

// Add keeps one line.
func (t *Tail) Add(line string) {
	line = strings.TrimRight(t.redact(line), " \t")
	if line == "" {
		return
	}
	if len(line) > 400 {
		line = line[:400]
	}
	t.lines = append(t.lines, line)
	if len(t.lines) > tailLines {
		t.lines = t.lines[len(t.lines)-tailLines:]
	}
}

// String is the kept text, newest last, at most 8 KiB (the oldest lines go first).
func (t *Tail) String() string {
	s := strings.Join(t.lines, "\n")
	if len(s) > tailBytes {
		s = s[len(s)-tailBytes:]
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return s
}

// Failure classes of the image_builds table.
const (
	ClassNetwork  = "network"
	ClassChecksum = "checksum"
	ClassDisk     = "disk"
	ClassApt      = "apt"
	ClassTimeout  = "timeout"
	ClassOther    = "other"
)

var classWords = []struct {
	class string
	words []string
	why   string
}{
	{ClassDisk, []string{"no space left on device"}, "The disk is full."},
	{ClassChecksum, []string{"hash sum mismatch", "checksum", "sha256sum", "digest mismatch"}, "A download did not match its checksum."},
	{ClassNetwork, []string{"could not resolve", "temporary failure in name resolution", "connection refused", "connection reset", "network is unreachable", "tls handshake", "no route to host", "i/o timeout"}, "A download could not reach its server."},
	{ClassTimeout, []string{"context deadline exceeded", "timed out", "timeout"}, "A step took too long."},
	{ClassApt, []string{"e: unable to locate package", "e: failed to fetch", "dpkg: error"}, "A package could not be installed."},
}

// Classify says which failure class a log tail shows and a short reason in
// plain words. Disk and checksum win over network, since a full disk also
// shows as a failed download.
func Classify(log string) (class, why string) {
	low := strings.ToLower(log)
	for _, c := range classWords {
		for _, w := range c.words {
			if strings.Contains(low, w) {
				return c.class, c.why
			}
		}
	}
	return ClassOther, "The build stopped; the log tail shows where."
}
