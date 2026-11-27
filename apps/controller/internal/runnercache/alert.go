package runnercache

// Oversize is one cache file or folder past the size alert threshold.
type Oversize struct {
	Path  string
	Bytes int64
	// Folder is true for a top-level entry, false for one file.
	Folder bool
}

// FindOversize lists the top-level entries of dir and the single files in it
// that are past threshold bytes, in one walk (caches R5.49). A threshold of 0
// or less finds nothing. A file inside a flagged folder is listed too, because
// the file is often the one to look at.
func FindOversize(fsys FS, dir string, threshold int64) ([]Oversize, error) {
	if threshold <= 0 {
		return nil, nil
	}
	var out []Oversize
	es, err := scanWith(fsys, dir, func(p string, n int64) {
		if n > threshold {
			out = append(out, Oversize{Path: p, Bytes: n})
		}
	})
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		if e.Size > threshold && !hasPath(out, e.Path) {
			out = append(out, Oversize{Path: e.Path, Bytes: e.Size, Folder: true})
		}
	}
	return out, nil
}

func hasPath(os []Oversize, p string) bool {
	for _, o := range os {
		if o.Path == p {
			return true
		}
	}
	return false
}
