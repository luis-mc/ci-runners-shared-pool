package addresses_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// allowlist holds the literals that may stay outside addresses.go. Each entry
// names the file and the literal and says why it is not an address crsp
// chooses. Keep it tiny.
var allowlist = map[string]string{}

// literalAddress matches a string that starts with an absolute address and a host.
var literalAddress = regexp.MustCompile(`^\^?https?://[A-Za-z0-9]`)

// skipped are the places that are not hand-written application code.
func skipped(rel string) bool {
	switch {
	case rel == "apps/controller/internal/addresses/addresses.go":
		return true // the one file that holds the defaults
	case strings.HasPrefix(rel, "apps/controller/internal/api/gen/"), strings.HasPrefix(rel, "apps/controller/internal/api/client/"),
		strings.HasPrefix(rel, "apps/controller/internal/github/fake/"), strings.HasSuffix(rel, ".gen.go"):
		return true // generated code and the fake GitHub
	}
	return false
}

// catalogueHosts are the VM image hosts that internal/vmcheck/vm-images.json
// may name (catalogue data, moved by CRSP_VM_IMAGE_MIRROR, OPS37). A new host
// in the catalogue is added here on purpose, with the owner's say; a host in Go
// code never is.
var catalogueHosts = []string{"cloud-images.ubuntu.com", "cloud.debian.org", "dl.fedoraproject.org", "dl.rockylinux.org"}

// imageHostLiteral matches an image address written without a scheme: a host
// name and a path that ends in an image or checksum file
// ("dl.example.org/pub/disk.qcow2"). With a scheme, TestNoLiteralAddressesInGo
// catches it.
var imageHostLiteral = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+(:[0-9]+)?/\S*(\.qcow2|\.img|\.iso|SUMS|CHECKSUM)$`)

// walkGoStrings calls fn for every string literal of the hand-written, non-test
// Go files of the repository.
func walkGoStrings(t *testing.T, fn func(rel string, pos token.Position, val string)) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "vendor", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || skipped(rel) {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Errorf("%s: %v", rel, perr)
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if _, isImport := n.(*ast.ImportSpec); isImport {
				return false
			}
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if val, uerr := strconv.Unquote(lit.Value); uerr == nil {
					fn(rel, fset.Position(lit.Pos()), val)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNoLiteralVMImageHostsInGo: VM images come from the catalogue
// (vm-images.json), moved by CRSP_VM_IMAGE_MIRROR, never from a host written in
// Go code, with or without a scheme (operations OPS37). The catalogue itself may
// name only the hosts listed above.
func TestNoLiteralVMImageHostsInGo(t *testing.T) {
	walkGoStrings(t, func(rel string, pos token.Position, val string) {
		for _, h := range catalogueHosts {
			if strings.Contains(val, h) {
				t.Errorf("%s: literal VM image host %q; it belongs in internal/vmcheck/vm-images.json", pos, h)
			}
		}
		if imageHostLiteral.MatchString(val) {
			if _, ok := allowlist[rel+" "+val]; !ok {
				t.Errorf("%s: literal host address %q; read it from the settings (internal/addresses)", pos, val)
			}
		}
	})
}

// TestCatalogueNamesOnlyKnownVMImageHosts: a new host in vm-images.json is a
// decision, so it has to be added to catalogueHosts here.
func TestCatalogueNamesOnlyKnownVMImageHosts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "vmcheck", "vm-images.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Distros map[string]struct {
			BaseURL     string `json:"base_url"`
			ChecksumURL string `json:"checksum_url"`
		} `json:"distros"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, h := range catalogueHosts {
		known[h] = true
	}
	for name, d := range f.Distros {
		for _, raw := range []string{d.BaseURL, d.ChecksumURL} {
			u, err := url.Parse(raw)
			if err != nil || !known[u.Hostname()] {
				t.Errorf("%s: host of %q is not in catalogueHosts (guard_test.go)", name, raw)
			}
		}
	}
}

// TestNoLiteralAddressesInGo: every address crsp calls or links to comes from
// the settings (CRSP_GITHUB_URL and the others), never from a literal in code
// (operations OPS35). Comments are not string literals, so examples there pass.
func TestNoLiteralAddressesInGo(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "vendor", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || skipped(rel) {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Errorf("%s: %v", rel, perr)
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val, uerr := strconv.Unquote(lit.Value)
			if uerr != nil || !literalAddress.MatchString(val) {
				return true
			}
			if _, ok := allowlist[rel+" "+val]; ok {
				return true
			}
			t.Errorf("%s: literal address %q; read it from the settings (internal/addresses)", fset.Position(lit.Pos()), val)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
