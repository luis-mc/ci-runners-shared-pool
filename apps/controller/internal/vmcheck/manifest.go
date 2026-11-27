package vmcheck

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestSuffix ends the manifest file of a prepared base disk.
const ManifestSuffix = ".packages"

// ManifestFile is the manifest of a base disk: bases/<distro>-<key>.packages
// next to bases/<distro>-<key>.qcow2. It lists the packages the base disk was
// built with, one per line, sorted (caches R5.46). `crsp vm doctor` compares it
// with the packages the runners need.
func ManifestFile(base string) string {
	return strings.TrimSuffix(base, ".qcow2") + ManifestSuffix
}

// CleanPackages sorts a package list and drops empty names and repeats.
func CleanPackages(pkgs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range pkgs {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// WriteManifest writes the manifest of a base disk through a temporary name
// and a rename, so a reader never sees half a list.
func WriteManifest(base string, pkgs []string) error {
	name := ManifestFile(base)
	tmp, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename
	w := bufio.NewWriter(tmp)
	for _, p := range CleanPackages(pkgs) {
		if _, err := w.WriteString(p + "\n"); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	//nolint:gosec // shared cache file, group readable on purpose
	if err := os.Chmod(tmp.Name(), 0o664); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}

// ReadManifest reads the package list of a base disk. A base with no manifest
// was built before manifests existed, with no package, so it reads as empty.
func ReadManifest(base string) ([]string, error) {
	data, err := os.ReadFile(ManifestFile(base)) //nolint:gosec // a path inside the cache
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return CleanPackages(strings.Split(string(data), "\n")), nil
}

// MissingPackages lists the needed packages the manifest does not have, sorted.
func MissingPackages(have, need []string) []string {
	in := map[string]bool{}
	for _, p := range have {
		in[p] = true
	}
	var out []string
	for _, p := range CleanPackages(need) {
		if !in[p] {
			out = append(out, p)
		}
	}
	return out
}

// Installer puts packages into a prepared base disk. Nothing in crsp installs
// them yet (caches R5.46, open question 19): it needs a guest boot on the base.
type Installer func(ctx context.Context, base string, pkgs []string) error

// ErrNoInstaller is returned when a base disk with packages is asked for and
// no installer is given, so a manifest never lists what is not in the disk.
var ErrNoInstaller = errors.New("packages cannot be put into a base disk yet: nothing installs them")
