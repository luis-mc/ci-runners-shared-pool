package collect

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Redactor is the part of internal/redact the vault needs: it masks the secret values the controller holds
// and the shapes of tokens, keys and credential headers (R5.2, R5.4, R6.5).
type Redactor interface {
	Redact(s string) string
}

// ErrRedactor is returned when the redactor failed; nothing is stored then (spec section 8: closed).
var ErrRedactor = errors.New("the redactor failed, nothing was stored")

// Mask replaces a value the vault will not keep.
const Mask = "***"

// maxChunk is the longest piece the redactor sees at once. A longer line (a one-line JSON report) is cut at a
// separator, so a secret is not split in two. A variable so a test can make it small.
var maxChunk = 1 << 20

// maxBlock bounds a multi-line private key block held for the redactor.
const maxBlock = 64 << 10

var (
	// A GitHub log line starts with a timestamp: 2026-11-20T10:00:00.1234567Z.
	timestamp = regexp.MustCompile(`^\d{4}-\d\d-\d\dT[\d:.]+Z `)
	envEntry  = regexp.MustCompile(`^([ \t]+)([A-Za-z_][A-Za-z0-9_.\-]*):(?: .*)?$`)
	email     = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
)

// scrubOptions says what scrub does on top of redaction.
type scrubOptions struct {
	maxRaw int64
	// envBlocks drops the values of an `env:` block of a job log, keeping names only (R5.2).
	envBlocks bool
}

// scrubbed is what scrub counted.
type scrubbed struct {
	raw   int64
	lines int
	sum   string // sha256 of the stored (redacted, uncompressed) text
}

// scrub copies src to dst line by line with every line redacted, emails masked and, for a log, the values of
// env: blocks dropped. It stops with ErrTooLarge when src is longer than opt.maxRaw. A panic or error of the
// redactor is ErrRedactor.
func scrub(dst io.Writer, src io.Reader, rd Redactor, opt scrubOptions) (out scrubbed, err error) {
	if rd == nil {
		return scrubbed{}, ErrRedactor
	}
	defer func() {
		if recover() != nil {
			out, err = scrubbed{}, ErrRedactor
		}
	}()
	h := sha256.New()
	w := io.MultiWriter(dst, h)
	sp := &splitter{br: bufio.NewReaderSize(src, 64<<10), max: maxChunk}
	emit := func(s string, newline bool) error {
		s = email.ReplaceAllString(rd.Redact(s), Mask)
		if newline {
			s += "\n"
		}
		_, err := io.WriteString(w, s)
		return err
	}
	var (
		inEnv     bool
		envIndent string
		block     []string
		blockSize int
		open      bool // the last piece had no newline
	)
	for {
		line, newline, err := sp.next()
		if err != nil && !errors.Is(err, io.EOF) {
			return scrubbed{}, err
		}
		if errors.Is(err, io.EOF) {
			if len(block) > 0 { // an unfinished key block: keep nothing of it
				if err := emit(Mask, true); err != nil {
					return scrubbed{}, err
				}
			}
			if open {
				out.lines++
			}
			out.sum = hex.EncodeToString(h.Sum(nil))
			return out, nil
		}
		out.raw += int64(len(line))
		open = !newline
		if newline {
			out.raw++
			out.lines++
		}
		if opt.maxRaw > 0 && out.raw > opt.maxRaw {
			return scrubbed{}, ErrTooLarge
		}
		// A private key spans lines; the redactor needs the whole block.
		if len(block) > 0 || (strings.Contains(line, "-----BEGIN ") && !strings.Contains(line, "-----END ")) {
			block = append(block, line)
			blockSize += len(line)
			if strings.Contains(line, "-----END ") || len(block) > 200 || blockSize > maxBlock {
				joined := strings.Join(block, "\n")
				if !strings.Contains(line, "-----END ") {
					joined = Mask
				}
				block, blockSize = nil, 0
				if err := emit(joined, newline); err != nil {
					return scrubbed{}, err
				}
			}
			continue
		}
		if opt.envBlocks {
			body := timestamp.ReplaceAllString(line, "")
			prefix := line[:len(line)-len(body)]
			switch {
			case inEnv && body != "" && (body[0] == ' ' || body[0] == '\t'):
				m := envEntry.FindStringSubmatch(body)
				if envIndent == "" && m != nil {
					envIndent = m[1]
				}
				if m == nil || m[1] != envIndent {
					continue // a continuation of a value: dropped
				}
				if err := emit(prefix+m[1]+m[2]+": "+Mask, newline); err != nil {
					return scrubbed{}, err
				}
				continue
			case strings.TrimSpace(body) == "env:":
				inEnv, envIndent = true, ""
			default:
				inEnv = false
			}
		}
		if err := emit(line, newline); err != nil {
			return scrubbed{}, err
		}
	}
}

// splitter cuts a stream into lines, and cuts a line over max at a separator.
type splitter struct {
	br      *bufio.Reader
	max     int
	pending []byte
	buf     []byte
	eof     bool
}

// next returns the next line without its newline; newline says whether the line ended with one. At the end
// it returns io.EOF.
func (s *splitter) next() (string, bool, error) {
	for {
		if i := bytes.IndexByte(s.pending, '\n'); i >= 0 && i <= s.max {
			line := strings.TrimSuffix(string(s.pending[:i]), "\r")
			s.pending = s.pending[i+1:]
			return line, true, nil
		}
		if len(s.pending) > s.max {
			cut := s.max
			if i := bytes.LastIndexAny(s.pending[:s.max], " \t,;:\"'<>{}[]()"); i > 0 {
				cut = i + 1
			}
			line := string(s.pending[:cut])
			s.pending = s.pending[cut:]
			return line, false, nil
		}
		if s.eof {
			if len(s.pending) == 0 {
				return "", false, io.EOF
			}
			line := strings.TrimSuffix(string(s.pending), "\r")
			s.pending = nil
			return line, false, nil
		}
		if s.buf == nil {
			s.buf = make([]byte, 32<<10)
		}
		n, err := s.br.Read(s.buf)
		s.pending = append(s.pending, s.buf[:n]...)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return "", false, fmt.Errorf("read the file: %w", err)
			}
			s.eof = true
		}
	}
}
