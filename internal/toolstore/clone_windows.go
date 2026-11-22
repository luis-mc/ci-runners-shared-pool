//go:build windows

package toolstore

import (
	"log"
)

// DefaultCloner clones a cache into a job home with NTFS hard links, file by file, and copies a file whose
// link fails (another volume, a file system that is not NTFS). The fallback is logged once per cache (owner
// decision 22 Nov; it replaces the ReFS block clone). Folders are always new.
func DefaultCloner() Cloner {
	return NewLinkCloner(nil, func(cache string, err error) {
		log.Printf("toolstore: cache %s: hard link failed (%v); copying its files instead", cache, err)
	})
}
