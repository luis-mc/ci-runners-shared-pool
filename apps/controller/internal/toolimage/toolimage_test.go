package toolimage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
)

func dig(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

type ent struct {
	name, body string
	typ        byte
	mode       int64
}

func layer(t *testing.T, gz bool, ents ...ent) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.Writer = &buf
	var gw *gzip.Writer
	if gz {
		gw = gzip.NewWriter(&buf)
		w = gw
	}
	tw := tar.NewWriter(w)
	for _, e := range ents {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: e.mode, Size: int64(len(e.body)), Linkname: "x"}
		if typ == tar.TypeDir {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if gw != nil {
		if err := gw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// fakeReg serves one image from memory.
type fakeReg struct {
	manifest []byte
	blobs    map[string][]byte
	blobGets int
}

func newReg(t *testing.T, layers ...[]byte) (*fakeReg, Ref) {
	t.Helper()
	m := Manifest{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json"}
	r := &fakeReg{blobs: map[string][]byte{}}
	for _, l := range layers {
		m.Layers = append(m.Layers, Descriptor{MediaType: "application/vnd.oci.image.layer.v1.tar", Digest: dig(l), Size: int64(len(l))})
		r.blobs[dig(l)] = l
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	r.manifest = raw
	return r, Ref{Name: "ghcr.io/o/tools:1", Digest: dig(raw)}
}

func (r *fakeReg) Manifest(context.Context, Ref) ([]byte, error) { return r.manifest, nil }
func (r *fakeReg) Blob(_ context.Context, _ Ref, d string) (io.ReadCloser, error) {
	r.blobGets++
	b, ok := r.blobs[d]
	if !ok {
		return nil, errors.New("no blob")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// memTree is the in-memory FS with an atomic rename.
type memTree struct{ *sysfake.FS }

func (m memTree) Rename(from, to string) error {
	for k, v := range m.Files {
		if strings.HasPrefix(k, from+string(filepath.Separator)) {
			nk := to + strings.TrimPrefix(k, from)
			m.Files[nk], m.Modes[nk] = v, m.Modes[k]
			delete(m.Files, k)
			delete(m.Modes, k)
		}
	}
	if mode, ok := m.Dirs[from]; ok {
		m.Dirs[to] = mode
		delete(m.Dirs, from)
	}
	if v, ok := m.Files[from]; ok {
		m.Files[to] = v
		delete(m.Files, from)
	}
	return nil
}

var _ TreeFS = memTree{}
var _ sys.FS = memTree{}

func fp(parts ...string) string { return filepath.Join(parts...) }

func TestParseRefDigestOnly(t *testing.T) {
	h := strings.Repeat("ab", 32)
	if r, err := ParseRef("ghcr.io/o/t:1@sha256:" + h); err != nil || r.Digest != "sha256:"+h || r.Hex() != h {
		t.Fatalf("%v %v", r, err)
	}
	for _, bad := range []string{"ghcr.io/o/t:1", "ghcr.io/o/t", "ghcr.io/o/t@sha256:abc", ""} {
		if _, err := ParseRef(bad); !errors.Is(err, ErrNotDigest) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestUnpackToDisk(t *testing.T) {
	l1 := layer(t, true,
		ent{name: "bin/", typ: tar.TypeDir, mode: 0o755},
		ent{name: "bin/node", body: "NODE", mode: 0o755},
		ent{name: "lib/a.txt", body: "old", mode: 0o644},
		ent{name: "lib/b.txt", body: "b", mode: 0o644},
		ent{name: "cache/x", body: "x", mode: 0o644})
	l2 := layer(t, false,
		ent{name: "lib/.wh.b.txt", mode: 0o644},
		ent{name: "lib/a.txt", body: "new", mode: 0o644},
		ent{name: "cache/.wh..wh..opq", mode: 0o644},
		ent{name: "cache/y", body: "y", mode: 0o644})
	reg, ref := newReg(t, l1, l2)
	f := memTree{sysfake.NewFS()}
	dest := fp("/tools", ref.Hex())
	if err := Unpack(context.Background(), reg, f, ref, dest); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.ReadFile(fp(dest, "bin", "node")); string(got) != "NODE" || f.Modes[fp(dest, "bin", "node")] != 0o755 {
		t.Errorf("node: %q %v", got, f.Modes[fp(dest, "bin", "node")])
	}
	if got, _ := f.ReadFile(fp(dest, "lib", "a.txt")); string(got) != "new" {
		t.Errorf("upper layer wins: %q", got)
	}
	if f.Exists(fp(dest, "lib", "b.txt")) {
		t.Error("whiteout not applied")
	}
	if f.Exists(fp(dest, "cache", "x")) || !f.Exists(fp(dest, "cache", "y")) {
		t.Error("opaque whiteout not applied")
	}
	if f.Exists(dest + ".part") {
		t.Error("part left behind")
	}
	for k := range f.Files {
		if strings.Contains(k, ".part") {
			t.Errorf("part file %s", k)
		}
	}
}

func TestPullRefusesWrongDigests(t *testing.T) {
	good := layer(t, false, ent{name: "a", body: "a", mode: 0o644})
	ctx := context.Background()

	// Manifest that does not hash to the reference.
	reg, ref := newReg(t, good)
	ref.Digest = dig([]byte("other"))
	f := memTree{sysfake.NewFS()}
	if err := Unpack(ctx, reg, f, ref, fp("/t", "x")); !errors.Is(err, ErrDigestMismatch) {
		t.Errorf("manifest: %v", err)
	}

	// Layer bytes changed behind the manifest.
	reg, ref = newReg(t, good)
	for k := range reg.blobs {
		reg.blobs[k] = layer(t, false, ent{name: "evil", body: "e", mode: 0o755})
	}
	f = memTree{sysfake.NewFS()}
	err := Unpack(ctx, reg, f, ref, fp("/t", "x"))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("layer: %v", err)
	}
	if len(f.Files) != 0 || f.Exists(fp("/t", "x")) {
		t.Errorf("a refused image left files: %v", f.Names())
	}
}

func TestUnpackRefusesUnsafeEntries(t *testing.T) {
	for name, e := range map[string]ent{
		"dotdot":    {name: "../etc/passwd", body: "x", mode: 0o644},
		"nested":    {name: "a/../../b", body: "x", mode: 0o644},
		"absolute":  {name: "/etc/passwd", body: "x", mode: 0o644},
		"symlink":   {name: "l", typ: tar.TypeSymlink},
		"hardlink":  {name: "h", typ: tar.TypeLink},
		"device":    {name: "d", typ: tar.TypeChar},
		"backslash": {name: `a\..\b`, body: "x", mode: 0o644},
	} {
		reg, ref := newReg(t, layer(t, false, e))
		f := memTree{sysfake.NewFS()}
		err := Unpack(context.Background(), reg, f, ref, fp("/t", "x"))
		if !errors.Is(err, ErrUnsafeEntry) {
			t.Errorf("%s: %v", name, err)
		}
		if len(f.Files) != 0 {
			t.Errorf("%s: files left: %v", name, f.Names())
		}
	}
}

func TestIndexAndTagRefused(t *testing.T) {
	reg, ref := newReg(t, layer(t, false, ent{name: "a", body: "a", mode: 0o644}))
	reg.manifest = []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","layers":[]}`)
	ref.Digest = dig(reg.manifest)
	if _, err := FetchManifest(context.Background(), reg, ref); err == nil || !strings.Contains(err.Error(), "index") {
		t.Errorf("index: %v", err)
	}
}

type fakePacker struct {
	calls []string
	err   error
	fs    memTree
}

func (p *fakePacker) Pack(_ context.Context, src, out string) error {
	p.calls = append(p.calls, src+" -> "+out)
	if p.err != nil {
		return p.err
	}
	return p.fs.WriteFile(out, []byte("SQUASH"), 0o644)
}

func TestPrepareDiskAndVirtiofs(t *testing.T) {
	reg, ref := newReg(t, layer(t, true, ent{name: "bin/tool", body: "T", mode: 0o755}))
	f := memTree{sysfake.NewFS()}
	pk := &fakePacker{fs: f}
	d := Deps{Pull: reg, FS: f, Packer: pk}
	ctx := context.Background()

	m, err := Prepare(ctx, d, "/tools", ref, KindDisk)
	if err != nil {
		t.Fatal(err)
	}
	wantDisk := fp("/tools", ref.Hex()) + DiskExt
	if m.HostPath != wantDisk || !m.ReadOnly || m.GuestPath != backend.ToolGuestPath || m.Digest != ref.Digest || !f.Exists(wantDisk) {
		t.Errorf("disk mount: %+v", m)
	}
	if f.Exists(fp("/tools", ref.Hex())) || len(pk.calls) != 1 {
		t.Errorf("tree must be gone after packing: %v %v", f.Names(), pk.calls)
	}
	// Same digest again: reused, nothing pulled or packed.
	gets := reg.blobGets
	if _, err := Prepare(ctx, d, "/tools", ref, KindDisk); err != nil || reg.blobGets != gets || len(pk.calls) != 1 {
		t.Errorf("not reused: %v gets %d->%d", err, gets, reg.blobGets)
	}

	m, err = Prepare(ctx, d, "/tools2", ref, KindVirtiofs)
	if err != nil || m.Kind != KindVirtiofs || m.HostPath != fp("/tools2", ref.Hex()) || m.Tag != backend.ToolShareTag || !f.Exists(fp(m.HostPath, "bin", "tool")) {
		t.Errorf("virtiofs mount: %+v %v", m, err)
	}

	if _, err := Prepare(ctx, d, "/t3", ref, "nfs"); err == nil {
		t.Error("unknown kind must fail")
	}
}

func TestPrepareDiskPackFailureLeavesNothing(t *testing.T) {
	reg, ref := newReg(t, layer(t, false, ent{name: "a", body: "a", mode: 0o644}))
	f := memTree{sysfake.NewFS()}
	d := Deps{Pull: reg, FS: f, Packer: &fakePacker{fs: f, err: errors.New("no space")}}
	if _, err := Prepare(context.Background(), d, "/tools", ref, KindDisk); err == nil {
		t.Fatal("want error")
	}
	if len(f.Files) != 0 {
		t.Errorf("left: %v", f.Names())
	}
}

func TestSquashPackerCommand(t *testing.T) {
	c := &sysfake.Commands{}
	if err := (SquashPacker{Cmd: c}).Pack(context.Background(), "/src", "/out.sqsh"); err != nil {
		t.Fatal(err)
	}
	got := c.Texts()
	if len(got) != 1 || got[0] != "mksquashfs /src /out.sqsh -noappend -all-root -no-xattrs -quiet" {
		t.Errorf("%v", got)
	}
	c = &sysfake.Commands{Rules: []sysfake.Rule{{Match: "mksquashfs", Result: sys.Result{ExitCode: 1, Stderr: "boom"}}}}
	if err := (SquashPacker{Cmd: c}).Pack(context.Background(), "/s", "/o"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("%v", err)
	}
}

// fakeVM implements backend.Backend and ToolBooter.
type fakeVM struct {
	backend.Backend
	tools []backend.ToolMount
}

func (v *fakeVM) StartWithTools(_ context.Context, _ backend.RunnerSpec, _ backend.StartConfig, tools []backend.ToolMount) (backend.Handle, error) {
	v.tools = tools
	return backend.Handle{RunnerID: "r1", Ref: "vm1"}, nil
}

type plainVM struct{ backend.Backend }

func TestStartAttachesAtBoot(t *testing.T) {
	reg, ref := newReg(t, layer(t, false, ent{name: "a", body: "a", mode: 0o644}))
	f := memTree{sysfake.NewFS()}
	d := Deps{Pull: reg, FS: f, Packer: &fakePacker{fs: f}}
	vm := &fakeVM{}
	h, err := Start(context.Background(), vm, d, "/tools", ref.String(), KindDisk, backend.RunnerSpec{}, backend.StartConfig{})
	if err != nil || h.Ref != "vm1" || len(vm.tools) != 1 || !vm.tools[0].ReadOnly || vm.tools[0].Kind != KindDisk {
		t.Fatalf("%+v %v %+v", h, err, vm.tools)
	}
	// A backend without the hook: nothing is pulled.
	gets := reg.blobGets
	if _, err := Start(context.Background(), plainVM{}, d, "/tools9", ref.String(), KindDisk, backend.RunnerSpec{}, backend.StartConfig{}); !errors.Is(err, ErrNoToolMounts) || reg.blobGets != gets {
		t.Errorf("no hook: %v", err)
	}
	// A tag reference never starts a VM.
	if _, err := Start(context.Background(), vm, d, "/tools", "ghcr.io/o/tools:1", KindDisk, backend.RunnerSpec{}, backend.StartConfig{}); !errors.Is(err, ErrNotDigest) {
		t.Errorf("tag: %v", err)
	}
}
