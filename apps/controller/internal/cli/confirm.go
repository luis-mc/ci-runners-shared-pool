package cli

import (
	"fmt"
	"strings"
)

// confirm lists what goes, then asks "Type yes to go on:" (R5.14, R5.15).
// With yes it still prints the list. Without a terminal and without yes it
// changes nothing and exits 5.
func (c *call) confirm(title string, items []string, yes bool) error {
	w := c.env.Stderr
	_, _ = fmt.Fprintln(w, title)
	for _, it := range items {
		_, _ = fmt.Fprintf(w, "  - %s\n", it)
	}
	if yes {
		return nil
	}
	if !c.env.IsTerminal() {
		return fail(ExitNotConfirmed, "Nothing changed. Run it again with --yes to go on without a question.")
	}
	_, _ = fmt.Fprint(w, "Type yes to go on: ")
	line, _ := c.stdin().ReadString('\n')
	if strings.EqualFold(strings.TrimSpace(line), "yes") {
		return nil
	}
	return fail(ExitNotConfirmed, "Nothing changed.")
}
