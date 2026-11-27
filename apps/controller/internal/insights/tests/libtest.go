package tests

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type libtestParser struct{}

func (libtestParser) Format() Format { return Libtest }

func (libtestParser) Detect(head []byte) bool {
	return firstByte(head) == '{' && hasAny(head, `"type"`) && hasAny(head, `"suite"`, `"test"`) && !hasAny(head, `"Action"`)
}

type ltEvent struct {
	Type     string  `json:"type"`
	Event    string  `json:"event"`
	Name     string  `json:"name"`
	ExecTime float64 `json:"exec_time"`
}

// Parse reads cargo libtest JSON lines. A name "module::test" gives suite "module". Libtest does not report
// assertions: -1.
func (libtestParser) Parse(r io.Reader, emit func(Case) error) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 && line[0] == '{' {
			var e ltEvent
			if json.Unmarshal(line, &e) == nil && e.Type == "test" && e.Name != "" {
				c := Case{Name: e.Name, Assertions: -1, Duration: secs(e.ExecTime)}
				if i := strings.LastIndex(e.Name, "::"); i > 0 {
					c.Suite, c.Name = e.Name[:i], e.Name[i+2:]
				}
				ok := true
				switch e.Event {
				case "ok":
					c.Result = Passed
				case "failed":
					c.Result = Failed
				case "ignored":
					c.Result = Skipped
				default:
					ok = false
				}
				if ok {
					if err := emit(c); err != nil {
						return err
					}
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
