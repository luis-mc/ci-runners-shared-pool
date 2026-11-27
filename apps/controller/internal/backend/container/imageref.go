package container

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrBadImageRef is returned when an image reference cannot be passed to the
// engine. The text names the value, so the owner sees what was wrong instead of
// the engine's "invalid reference format".
var ErrBadImageRef = errors.New("crsp: bad image reference")

var (
	refPathPart = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)
	refDomain   = regexp.MustCompile(`^(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])(?:\.(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))*(?::[0-9]{1,5})?$`)
	refTag      = regexp.MustCompile(`^[\w][\w.-]{0,127}$`)
	refDigest   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(?:[-_+.][A-Za-z][A-Za-z0-9]*)*:[0-9a-fA-F]{32,}$`)
)

// ValidateImageRef checks the grammar of a Docker image reference
// (`[host[:port]/]repo[/more][:tag][@algo:hex]`) before any engine call. It
// accepts `crsp-pool:<key>`, `host:port/crsp/std/name:tag` and `name@sha256:...`,
// and refuses an empty value, a scheme, spaces, a Windows path, upper case in the
// repository, a double colon and a malformed digest.
func ValidateImageRef(ref string) error {
	bad := func(why string) error { return fmt.Errorf("%w %q: %s", ErrBadImageRef, ref, why) }
	switch {
	case ref == "":
		return bad("it is empty")
	case strings.Contains(ref, "://"):
		return bad("it has a scheme such as https://")
	case strings.ContainsAny(ref, " \t\r\n\\"):
		return bad("it has a space or a backslash")
	}
	rest := ref
	if i := strings.Index(rest, "@"); i >= 0 {
		if !refDigest.MatchString(rest[i+1:]) {
			return bad("the digest after @ is not algorithm:hex")
		}
		rest = rest[:i]
	}
	last := rest
	if i := strings.LastIndex(rest, "/"); i >= 0 {
		last = rest[i+1:]
	}
	if i := strings.Index(last, ":"); i >= 0 {
		tag := last[i+1:]
		if !refTag.MatchString(tag) {
			return bad("the tag after : is empty or malformed")
		}
		rest = rest[:len(rest)-len(last)+i]
	}
	if rest == "" {
		return bad("the repository name is empty")
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 1 {
		if h := parts[0]; strings.ContainsAny(h, ".:") || h == "localhost" {
			if !refDomain.MatchString(h) {
				return bad("the registry host:port is malformed")
			}
			parts = parts[1:]
		}
	}
	for _, p := range parts {
		if !refPathPart.MatchString(p) {
			return bad("the repository name must be lower case letters, digits and . _ - separators")
		}
	}
	return nil
}
