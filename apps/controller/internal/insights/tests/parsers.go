package tests

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"time"
)

// Parsers returns every parser, most specific first (detection by content, R5.33).
func Parsers() []Parser {
	return []Parser{trxParser{}, junitParser{}, playwrightParser{}, vitestParser{}, libtestParser{}, goTestParser{}, ciRunTimesParser{}, ciTimingsParser{}}
}

// Sniff is how many leading bytes Detect looks at.
const Sniff = 4096

// Detect returns the parser whose Detect accepts head, or nil.
func Detect(head []byte) Parser {
	for _, p := range Parsers() {
		if p.Detect(head) {
			return p
		}
	}
	return nil
}

// ParseAny reads the first Sniff bytes, picks the parser by content and streams the file through it. It
// returns the format found. An unknown content is an error, never a guess.
func ParseAny(r io.Reader, emit func(Format, Case) error) (Format, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	head, _ := br.Peek(Sniff)
	p := Detect(head)
	if p == nil {
		return "", fmt.Errorf("not a known test report format")
	}
	f := p.Format()
	return f, p.Parse(br, func(c Case) error { return emit(f, c) })
}

func hasAny(head []byte, subs ...string) bool {
	for _, s := range subs {
		if bytes.Contains(head, []byte(s)) {
			return true
		}
	}
	return false
}

func firstByte(head []byte) byte {
	h := bytes.TrimLeft(head, "\xef\xbb\xbf \t\r\n")
	if len(h) == 0 {
		return 0
	}
	return h[0]
}

func secs(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }
