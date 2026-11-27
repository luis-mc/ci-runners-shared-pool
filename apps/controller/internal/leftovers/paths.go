package leftovers

import "strings"

// style holds the path rules of one OS.
type style struct{ win bool }

func styleOf(goos string) style { return style{win: goos == "windows"} }

// key is the form two names are compared in.
func (s style) key(v string) string {
	if s.win {
		return strings.ToLower(v)
	}
	return v
}

// split cleans a path into segments. ok is false for a path with a `..`
// segment. A Unix absolute path starts with an empty segment.
func (s style) split(p string) (segs []string, ok bool) {
	sep := "/"
	if s.win {
		p = strings.ReplaceAll(p, "/", `\`)
		sep = `\`
	}
	for i, part := range strings.Split(p, sep) {
		switch {
		case part == "..":
			return nil, false
		case part == "." || (part == "" && i != 0):
			continue
		}
		if s.win {
			part = strings.TrimRight(part, " ")
		}
		segs = append(segs, part)
	}
	return segs, true
}

func (s style) join(segs []string) string {
	if s.win {
		return strings.Join(segs, `\`)
	}
	if len(segs) == 1 && segs[0] == "" {
		return "/"
	}
	return strings.Join(segs, "/")
}

// under reports whether p lies strictly inside root (never root itself) and
// returns the segments of p below root.
func (s style) under(root, p string) ([]string, bool) {
	rs, ok1 := s.split(root)
	ps, ok2 := s.split(p)
	if !ok1 || !ok2 || len(ps) <= len(rs) {
		return nil, false
	}
	for i := range rs {
		if s.key(rs[i]) != s.key(ps[i]) {
			return nil, false
		}
	}
	return ps[len(rs):], true
}

// same reports whether two paths are the same place.
func (s style) same(a, b string) bool {
	as, ok1 := s.split(a)
	bs, ok2 := s.split(b)
	if !ok1 || !ok2 || len(as) != len(bs) {
		return false
	}
	for i := range as {
		if s.key(as[i]) != s.key(bs[i]) {
			return false
		}
	}
	return true
}

// binaryPath takes the program path out of a service command line:
// `"C:\x\RunnerService.exe" --arg` or `C:\x\RunnerService.exe -arg`.
func binaryPath(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if i := strings.Index(cmd[1:], `"`); i >= 0 {
			return cmd[1 : 1+i]
		}
		return cmd[1:]
	}
	if i := strings.Index(strings.ToLower(cmd), ".exe"); i >= 0 {
		return cmd[:i+4]
	}
	return cmd
}

// accountBase drops a `.\` or `MACHINE\` prefix from a service account.
func accountBase(a string) string {
	if i := strings.LastIndex(a, `\`); i >= 0 {
		return a[i+1:]
	}
	return a
}
