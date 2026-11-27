package agentinstallers

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Handler serves GET PathPrefix+<name>: an agent installer or SHA256SUMS. It is
// public like the GitHub release it comes from; a file is served only after
// its SHA-256 matched SHA256SUMS.
func (s *Store) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, PathPrefix)
		if name == SumsName {
			b, _, err := s.Sums(r.Context())
			if err != nil {
				fail(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
		if _, ok := ParseName(name); !ok {
			http.NotFound(w, r)
			return
		}
		f, size, err := s.Open(r.Context(), name)
		if err != nil {
			fail(w, err)
			return
		}
		defer func() { _ = f.Close() }()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		http.ServeContent(w, r, name, time.Time{}, io.NewSectionReader(f, 0, size))
	})
}

func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotListed):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrMismatch):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	default:
		http.Error(w, "the installer could not be read", http.StatusInternalServerError)
	}
}
