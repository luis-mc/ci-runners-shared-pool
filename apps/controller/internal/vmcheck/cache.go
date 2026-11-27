package vmcheck

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The shared VM cache (caches R5.47): one folder per machine that every
// repository's pool reads and writes, so one download serves all of them.
//
//	<cache>/images/<distro>-<16 hex of image digest>.qcow2   downloaded, sha-checked
//	<cache>/bases/<distro>-<base key>.qcow2                  prepared base disk
//	<cache>/snaps/<distro>-<snap key>.qcow2                  reserved (--resume, not built)
//	<cache>/use/<pid>-<nonce>.lease                          disks a running VM uses
//	<cache>/.lock-<name>                                     flock lock files
//
// Every write goes to a temporary name (".tmp-") under a lock and is renamed.

// Defaults of the cache.
const (
	// DefaultCacheCapGiB is the size cap of the cache; CRSP_VM_CACHE_GIB changes it.
	DefaultCacheCapGiB = 60
	// DefaultKeep is how many images, bases and snapshots of a distro prune keeps.
	DefaultKeep = 1
)

var cacheKinds = []string{"images", "bases", "snaps"}

// EnsureCacheDir makes the cache folder and its kind folders with mode 2775
// (setgid, group write), the shared-cache mode of caches R5.18. The mode is set
// only where this process owns the folder; the owner and the `kvm` group of a
// machine-wide folder come from the installer, not from here.
func EnsureCacheDir(dir string) error {
	for _, d := range append([]string{dir}, kindDirs(dir)...) {
		if err := os.MkdirAll(d, 0o775); err != nil { //nolint:gosec // shared cache folder, group write on purpose
			return err
		}
		setShared(d)
	}
	return nil
}

func kindDirs(dir string) []string {
	out := []string{filepath.Join(dir, "use")}
	for _, k := range cacheKinds {
		out = append(out, filepath.Join(dir, k))
	}
	return out
}

// BaseFile is the cache path of a prepared base disk.
func BaseFile(cacheDir, distro, key string) string {
	return filepath.Join(cacheDir, "bases", distro+"-"+key+".qcow2")
}

// BaseArgs is the `qemu-img` call that makes the prepared base disk: a thin
// qcow2 on the downloaded image, so the image file is never written and a run's
// overlay (OverlayArgs) sits on the base. The image path is made absolute
// because qcow2 stores the backing path as given.
func BaseArgs(image, base string) []string {
	if abs, err := filepath.Abs(image); err == nil {
		image = abs
	}
	return OverlayArgs(image, base)
}

// EnsureBase returns the prepared base disk of an image with no package, making
// it when it is not in the cache.
func EnsureBase(ctx context.Context, deps Deps, cacheDir, distro, image, digest string) (string, error) {
	return EnsureBasePackages(ctx, deps, cacheDir, distro, image, digest, nil, nil)
}

// EnsureBasePackages returns the prepared base disk of an image and a package
// list, making it when it is not in the cache. The key is BaseKey of the image
// digest and the list. The disk has a manifest of its packages next to it
// (ManifestFile); the doctor reads it. Packages need an installer, which runs on
// the new disk before it is renamed into place; without one a non-empty list is
// refused (ErrNoInstaller).
func EnsureBasePackages(ctx context.Context, deps Deps, cacheDir, distro, image, digest string, pkgs []string, install Installer) (string, error) {
	pkgs = CleanPackages(pkgs)
	if len(pkgs) > 0 && install == nil {
		return "", ErrNoInstaller
	}
	base := BaseFile(cacheDir, distro, BaseKey(digest, pkgs))
	unlock, err := Lock(ctx, cacheDir, "base-"+distro)
	if err != nil {
		return "", err
	}
	defer unlock()
	if st, err := os.Stat(base); err == nil && st.Mode().IsRegular() {
		if _, merr := os.Stat(ManifestFile(base)); errors.Is(merr, os.ErrNotExist) {
			// Made before manifests: the key says what it holds.
			if err := WriteManifest(base, pkgs); err != nil {
				return "", err
			}
		}
		return base, nil
	}
	if err := EnsureCacheDir(cacheDir); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(base), filepath.Base(base)+".tmp-*")
	if err != nil {
		return "", err
	}
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename
	if err := deps.Run(ctx, "qemu-img", BaseArgs(image, tmp.Name())...); err != nil {
		return "", fmt.Errorf("qemu-img could not make the base disk: %w", err)
	}
	if len(pkgs) > 0 {
		if err := install(ctx, tmp.Name(), pkgs); err != nil {
			return "", fmt.Errorf("packages could not be put into the base disk: %w", err)
		}
	}
	// The manifest first: a base without one reads as empty, so a crash between
	// the two writes must leave the manifest, never the disk alone.
	if err := WriteManifest(base, pkgs); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), base); err != nil {
		_ = os.Remove(ManifestFile(base))
		return "", err
	}
	return base, nil
}

// Lease says that this process uses disks of the cache, so prune and the size
// cap leave them alone. It returns the function that ends the lease.
func Lease(cacheDir string, paths ...string) (func(), error) {
	if err := EnsureCacheDir(cacheDir); err != nil {
		return nil, err
	}
	nonce := make([]byte, 6)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	name := filepath.Join(cacheDir, "use", strconv.Itoa(os.Getpid())+"-"+hex.EncodeToString(nonce)+".lease")
	if err := os.WriteFile(name, []byte(strings.Join(paths, "\n")+"\n"), 0o644); err != nil { //nolint:gosec // paths of public images
		return nil, err
	}
	return func() { _ = os.Remove(name) }, nil
}

// leaseFile is one lease on disk.
type leaseFile struct {
	file  string
	pid   int
	paths []string
}

func readLeases(cacheDir string) []leaseFile {
	entries, _ := os.ReadDir(filepath.Join(cacheDir, "use"))
	var out []leaseFile
	for _, e := range entries {
		pidText, _, ok := strings.Cut(e.Name(), "-")
		pid, err := strconv.Atoi(pidText)
		if !ok || err != nil || !strings.HasSuffix(e.Name(), ".lease") {
			continue
		}
		file := filepath.Join(cacheDir, "use", e.Name())
		b, err := os.ReadFile(file) //nolint:gosec // the cache's own folder
		if err != nil {
			continue
		}
		lf := leaseFile{file: file, pid: pid}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lf.paths = append(lf.paths, l)
			}
		}
		out = append(out, lf)
	}
	return out
}

// InUse is the set of cache paths named by leases of live processes; a lease
// whose process is gone is deleted. busy says whether any lease is live.
func InUse(cacheDir string, alive func(pid int) bool) (paths map[string]bool, busy bool) {
	paths = map[string]bool{}
	for _, lf := range readLeases(cacheDir) {
		if !alive(lf.pid) {
			_ = os.Remove(lf.file)
			continue
		}
		busy = true
		for _, p := range lf.paths {
			paths[p] = true
		}
	}
	return paths, busy
}

// CacheFile is a File with its size.
type CacheFile struct {
	File
	Size int64
}

// Scan lists the cache by kind ("images", "bases", "snaps", and "tmp" for the
// half-written files of any kind folder).
func Scan(cacheDir string) (map[string][]CacheFile, error) {
	out := map[string][]CacheFile{}
	for _, kind := range cacheKinds {
		entries, err := os.ReadDir(filepath.Join(cacheDir, kind))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if strings.HasSuffix(e.Name(), ManifestSuffix) {
				continue // follows its base disk, see Prune
			}
			cf := CacheFile{File{Path: filepath.Join(cacheDir, kind, e.Name()), ModUnix: info.ModTime().Unix()}, info.Size()}
			k := kind
			if strings.Contains(e.Name(), ".tmp-") {
				k = "tmp"
			}
			out[k] = append(out[k], cf)
		}
	}
	return out, nil
}

// PruneSpec is one prune.
type PruneSpec struct {
	Keep     int   // per distro and kind; 0 takes DefaultKeep
	CapBytes int64 // size cap; 0 means no cap pass
	Now      time.Time
	DryRun   bool
}

// PruneResult says what a prune did.
type PruneResult struct {
	Removed []string
	Freed   int64
	Kept    int
	InUse   []string // planned for removal but a running VM uses them
	Busy    bool     // a VM job runs, so the size cap did nothing
	Over    int64    // bytes still over the cap after the pass, 0 when it fits
}

// PlanPrune picks the files to remove (caches R5.46, R5.47):
//   - files of a distro the catalogue does not list;
//   - all but the newest keep of each distro in each kind folder, and
//     temporary files older than a day (PrunePlan);
//   - when capBytes is set and no VM job runs, the oldest remaining files until
//     the total fits, never the newest image of a distro (a run needs it).
//
// Files named in inUse are never planned; they are returned as skipped.
func PlanPrune(files map[string][]CacheFile, cat Catalogue, keep int, capBytes int64, now time.Time, inUse map[string]bool, busy bool) (remove, skipped []string, over int64) {
	if keep <= 0 {
		keep = DefaultKeep
	}
	size := map[string]int64{}
	byPath := map[string]File{}
	listed := map[string][]File{"tmp": nil}
	var all []string
	planned := map[string]bool{}
	add := func(p string) {
		if planned[p] {
			return
		}
		planned[p] = true
		if inUse[p] {
			skipped = append(skipped, p)
			return
		}
		remove = append(remove, p)
	}
	for kind, fs := range files {
		for _, cf := range fs {
			size[cf.Path] = cf.Size
			byPath[cf.Path] = cf.File
			if kind == "tmp" {
				listed["tmp"] = append(listed["tmp"], cf.File)
				continue
			}
			all = append(all, cf.Path)
			if m := pruneRe.FindStringSubmatch(filepath.Base(cf.Path)); m != nil {
				if _, ok := cat[m[1]]; !ok {
					add(cf.Path) // beyond what vm-images.json lists
					continue
				}
			}
			listed[kind] = append(listed[kind], cf.File)
		}
	}
	for _, p := range PrunePlan(listed, keep, now.Unix()) {
		add(p)
	}
	if capBytes > 0 {
		var total int64
		for _, p := range all {
			if !planned[p] || inUse[p] {
				total += size[p]
			}
		}
		if !busy && total > capBytes {
			newest := newestImages(files["images"])
			rest := make([]string, 0, len(all))
			for _, p := range all {
				if !planned[p] && !inUse[p] && !newest[p] {
					rest = append(rest, p)
				}
			}
			sort.Slice(rest, func(i, j int) bool {
				a, b := byPath[rest[i]], byPath[rest[j]]
				if a.ModUnix != b.ModUnix {
					return a.ModUnix < b.ModUnix
				}
				return a.Path < b.Path
			})
			for _, p := range rest {
				if total <= capBytes {
					break
				}
				add(p)
				total -= size[p]
			}
		}
		if total > capBytes {
			over = total - capBytes
		}
	}
	sort.Strings(remove)
	sort.Strings(skipped)
	return remove, skipped, over
}

func newestImages(images []CacheFile) map[string]bool {
	best := map[string]CacheFile{}
	for _, cf := range images {
		m := pruneRe.FindStringSubmatch(filepath.Base(cf.Path))
		if m == nil {
			continue
		}
		if b, ok := best[m[1]]; !ok || cf.ModUnix > b.ModUnix || (cf.ModUnix == b.ModUnix && cf.Path > b.Path) {
			best[m[1]] = cf
		}
	}
	out := map[string]bool{}
	for _, cf := range best {
		out[cf.Path] = true
	}
	return out
}

// Prune removes what PlanPrune picks, under the cache's prune lock so it does
// not run beside a download. alive says whether a process id still runs.
func Prune(ctx context.Context, cacheDir string, cat Catalogue, spec PruneSpec, alive func(pid int) bool) (PruneResult, error) {
	unlock, err := Lock(ctx, cacheDir, "prune")
	if err != nil {
		return PruneResult{}, err
	}
	defer unlock()
	files, err := Scan(cacheDir)
	if err != nil {
		return PruneResult{}, err
	}
	inUse, busy := InUse(cacheDir, alive)
	now := spec.Now
	if now.IsZero() {
		now = time.Now()
	}
	remove, skipped, over := PlanPrune(files, cat, spec.Keep, spec.CapBytes, now, inUse, busy)
	res := PruneResult{InUse: skipped, Busy: busy && spec.CapBytes > 0, Over: over}
	sizes := map[string]int64{}
	total := 0
	for _, fs := range files {
		for _, cf := range fs {
			sizes[cf.Path] = cf.Size
			total++
		}
	}
	for _, p := range remove {
		if !spec.DryRun {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return res, fmt.Errorf("remove %s: %w", p, err)
			}
		}
		if !spec.DryRun && strings.HasSuffix(p, ".qcow2") && filepath.Base(filepath.Dir(p)) == "bases" {
			if err := os.Remove(ManifestFile(p)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return res, fmt.Errorf("remove %s: %w", ManifestFile(p), err)
			}
		}
		res.Removed = append(res.Removed, p)
		res.Freed += sizes[p]
	}
	res.Kept = total - len(remove)
	return res, nil
}
