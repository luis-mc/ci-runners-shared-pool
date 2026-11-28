package depstore

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Handler serves GET PathPrefix+<id>: a stored, checked file. It is mounted on
// the agent link, whose pinned certificate the agent checks; the agent checks
// the SHA-256 again against its own pinned list.
func (s *Store) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, PathPrefix)
		f, size, err := s.Open(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrUnknown) || errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "the file could not be read", http.StatusInternalServerError)
			return
		}
		defer func() { _ = f.Close() }()
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, id, time.Time{}, io.NewSectionReader(f, 0, size))
	})
}
