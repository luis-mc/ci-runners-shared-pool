//go:build !windows

package runnerid

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
)

// HostFiles reads /etc/passwd and /etc/group (or the given files) for ids in
// use. A missing file counts as empty.
func HostFiles(passwd, group string) Host {
	return func(_ context.Context, id int) (bool, error) {
		for _, f := range []string{passwd, group} {
			ok, err := fileHasID(f, id)
			if err != nil || ok {
				return ok, err
			}
		}
		return false, nil
	}
}

// HostDefault reads the system files.
func HostDefault() Host { return HostFiles("/etc/passwd", "/etc/group") }

func fileHasID(path string, id int) (bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	want := strconv.Itoa(id)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		// passwd: name:x:uid:gid:...  group: name:x:gid:members
		if len(p) > 2 && p[2] == want {
			return true, nil
		}
		if len(p) > 3 && p[3] == want && len(p) > 5 {
			return true, nil
		}
	}
	return false, sc.Err()
}
