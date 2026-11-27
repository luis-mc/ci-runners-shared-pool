package power

import "strings"

// ParsePmset reads the output of `pmset -g batt`. The first line names the
// source: "Now drawing from 'Battery Power'" or "Now drawing from 'AC Power'".
// Anything else counts as on mains.
func ParsePmset(out string) State {
	for _, line := range strings.Split(out, "\n") {
		l := strings.ToLower(line)
		if !strings.Contains(l, "now drawing from") {
			continue
		}
		switch {
		case strings.Contains(l, "battery power"):
			return State{OnBattery: true, Known: true}
		case strings.Contains(l, "ac power"), strings.Contains(l, "ups power"):
			return State{Known: true}
		}
	}
	return Mains
}
