//go:build !darwin

package macvm

import "errors"

// CloneFile implements FS; only a Mac clones.
func (OSFS) CloneFile(_, _ string) error {
	return errors.New("copy-on-write clones need APFS on a Mac")
}
