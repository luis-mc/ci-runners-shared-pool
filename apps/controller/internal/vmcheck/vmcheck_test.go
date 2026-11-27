package vmcheck_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

func TestCatalogueHasTheFiveDistrosOfVmImagesJSON(t *testing.T) {
	c, err := vmcheck.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Names(), ","); got != "debian-12,fedora-45,rocky-10,rocky-9,ubuntu-24.04" {
		t.Fatalf("distros %s", got)
	}
	d, _ := c.Get("debian-12")
	if d.Algo != "sha512" || d.ChecksumFormat != "sums" || d.User != "debian" {
		t.Fatalf("%+v", d)
	}
	if _, err := c.Get("arch"); err == nil || !strings.Contains(err.Error(), "known: debian-12") {
		t.Fatalf("unknown distro: %v", err)
	}
}

func TestLoadExtraAddsAndReplacesAndValidates(t *testing.T) {
	extra := []byte(`{"distros":{"alma-9":{"base_url":"https://x/","checksum_url":"https://x/SUMS","checksum_format":"sums","algo":"sha256","pattern":"a.qcow2","user":"alma"},
	"rocky-9":{"base_url":"https://y/","checksum_url":"https://y/SUMS","checksum_format":"sums","algo":"sha256","pattern":"r.qcow2","user":"r"}}}`)
	c, err := vmcheck.Load(extra)
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 6 || c["rocky-9"].BaseURL != "https://y/" {
		t.Fatalf("%v", c.Names())
	}
	bad := []byte(`{"distros":{"x":{"base_url":"u","checksum_url":"u","checksum_format":"md5","algo":"sha256","pattern":"p","user":"u"}}}`)
	if _, err := vmcheck.Load(bad); err == nil {
		t.Fatal("bad format accepted")
	}
}

func TestParseChecksumsBSDAndSums(t *testing.T) {
	bsd := "# header\nSHA256 (Rocky-9.qcow2) = ABCDEF01\nSHA256 (Rocky-9.latest.qcow2) = 0123abcd\nSHA256 (Rocky-9.latest.qcow2.CHECKSUM) = ffff\n"
	img, err := vmcheck.ParseChecksums(bsd, "bsd", "Rocky-9*.qcow2")
	if err != nil || img.Name != "Rocky-9.latest.qcow2" || img.Digest != "0123abcd" {
		t.Fatalf("%+v %v", img, err)
	}
	sums := "AAAA  debian-12-genericcloud-amd64.qcow2\nBBBB *other.img\n"
	img, err = vmcheck.ParseChecksums(sums, "sums", "debian-12-genericcloud-amd64.qcow2")
	if err != nil || img.Digest != "aaaa" {
		t.Fatalf("%+v %v", img, err)
	}
	img, _ = vmcheck.ParseChecksums(sums, "sums", "other.img")
	if img.Digest != "bbbb" {
		t.Fatalf("star prefix: %+v", img)
	}
	if _, err := vmcheck.ParseChecksums(sums, "sums", "nope*"); err == nil {
		t.Fatal("no match accepted")
	}
}

func TestResolveChecksumURLPicksNewestListed(t *testing.T) {
	listing := `<a href="Fedora-Cloud-45-1.1-x86_64-CHECKSUM">a</a><a href="Fedora-Cloud-45-1.2-x86_64-CHECKSUM">b</a><a href="other">c</a><a href="Fedora-Cloud-45-1.2-x86_64-CHECKSUM">dup</a>`
	got, err := vmcheck.ResolveChecksumURL("https://h/images/Fedora-Cloud-45-*-x86_64-CHECKSUM", listing)
	if err != nil || got != "https://h/images/Fedora-Cloud-45-1.2-x86_64-CHECKSUM" {
		t.Fatalf("%q %v", got, err)
	}
	if got, _ := vmcheck.ResolveChecksumURL("https://h/SUMS", ""); got != "https://h/SUMS" {
		t.Fatalf("plain url changed: %q", got)
	}
	if _, err := vmcheck.ResolveChecksumURL("https://h/z-*", listing); err == nil {
		t.Fatal("no hit accepted")
	}
}

func TestKeys(t *testing.T) {
	a := vmcheck.BaseKey("sum", []string{"git", "make"})
	if a != vmcheck.BaseKey("sum", []string{"make", "git"}) || len(a) != 16 {
		t.Fatalf("base key depends on package order: %s", a)
	}
	if a == vmcheck.BaseKey("sum2", []string{"git", "make"}) || a == vmcheck.BaseKey("sum", []string{"git"}) {
		t.Fatal("base key ignores its inputs")
	}
	s := vmcheck.SnapKey(a, "QEMU 9", "cpu", 2048, 2)
	for _, other := range []string{vmcheck.SnapKey(a, "QEMU 10", "cpu", 2048, 2), vmcheck.SnapKey(a, "QEMU 9", "cpu2", 2048, 2),
		vmcheck.SnapKey(a, "QEMU 9", "cpu", 4096, 2), vmcheck.SnapKey(a, "QEMU 9", "cpu", 2048, 4)} {
		if s == other {
			t.Fatal("snapshot key ignores hardware")
		}
	}
}

func TestSELinuxVerdict(t *testing.T) {
	avc := vmcheck.CountAVC("ok line\ntype=AVC msg=audit: avc:  denied  { read } for pid=1\nAVC granted\n")
	if len(avc) != 1 {
		t.Fatalf("avc %v", avc)
	}
	if v := vmcheck.SELinuxVerdict("Enforcing\n", nil); v != "" {
		t.Fatal(v)
	}
	if v := vmcheck.SELinuxVerdict("Permissive", nil); v != "SELinux is Permissive, not Enforcing" {
		t.Fatal(v)
	}
	if v := vmcheck.SELinuxVerdict("", nil); !strings.Contains(v, "unknown") {
		t.Fatal(v)
	}
	if v := vmcheck.SELinuxVerdict("Enforcing", avc); !strings.HasPrefix(v, "1 AVC denial(s) since boot") {
		t.Fatal(v)
	}
}

type fakeSource struct {
	pages  map[string]string
	cached string
}

func (f fakeSource) Text(_ context.Context, url string) (string, error) {
	if p, ok := f.pages[url]; ok {
		return p, nil
	}
	return "", errors.New("no route to host")
}
func (f fakeSource) CachedDigest(string) (string, error) { return f.cached, nil }

func TestVerifyComparesCachedWithPublished(t *testing.T) {
	d := vmcheck.Distro{BaseURL: "https://h/", ChecksumURL: "https://h/SUMS", ChecksumFormat: "sums", Algo: "sha256", Pattern: "img.qcow2", User: "u"}
	pages := map[string]string{"https://h/SUMS": "AB12  img.qcow2\n"}
	cases := []struct {
		cached string
		want   vmcheck.Status
	}{{"ab12", vmcheck.Current}, {"ffff", vmcheck.Stale}, {"", vmcheck.Missing}}
	for _, c := range cases {
		r, err := vmcheck.Verify(context.Background(), fakeSource{pages, c.cached}, "d", d)
		if err != nil || r.Status != c.want || r.Published != "ab12" {
			t.Errorf("cached %q: %+v %v", c.cached, r, err)
		}
	}
	r, err := vmcheck.Verify(context.Background(), fakeSource{nil, "ab12"}, "d", d)
	if err != nil || r.Status != vmcheck.Offline || r.Cached != "ab12" || !strings.Contains(r.Detail, "no route") {
		t.Fatalf("offline: %+v %v", r, err)
	}
}

func TestVerifyResolvesGlobbedChecksumURL(t *testing.T) {
	d := vmcheck.Distro{BaseURL: "https://h/", ChecksumURL: "https://h/F-*-CHECKSUM", ChecksumFormat: "bsd", Algo: "sha256", Pattern: "F-Base-*.qcow2", User: "u"}
	pages := map[string]string{
		"https://h/":             `<a href="F-1-CHECKSUM">x</a><a href="F-2-CHECKSUM">y</a>`,
		"https://h/F-2-CHECKSUM": "SHA256 (F-Base-2.qcow2) = 99aa\n",
	}
	r, err := vmcheck.Verify(context.Background(), fakeSource{pages, "99aa"}, "f", d)
	if err != nil || r.Status != vmcheck.Current {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestReadiness(t *testing.T) {
	all := map[string]bool{}
	for _, tool := range vmcheck.Tools {
		all[tool] = true
	}
	if p := vmcheck.Readiness(vmcheck.Host{KVMUsable: true, Tools: all, CacheWritable: true}); len(p) != 0 {
		t.Fatalf("%v", p)
	}
	all["qemu-img"] = false
	p := vmcheck.Readiness(vmcheck.Host{Tools: all})
	if strings.Join(p, "; ") != "/dev/kvm is not usable; qemu-img is missing; cache is not writable" {
		t.Fatalf("%v", p)
	}
}

func TestPrunePlanKeepsNewestPerDistroAndOldTempFiles(t *testing.T) {
	const now = 1_000_000
	f := func(n string, m int64) vmcheck.File { return vmcheck.File{Path: "/c/" + n, ModUnix: m} }
	files := map[string][]vmcheck.File{
		"images": {f("fedora-45-aaaaaaaaaaaaaaaa.qcow2", 10), f("fedora-45-bbbbbbbbbbbbbbbb.qcow2", 20), f("rocky-9-cccccccccccccccc.qcow2", 5), f("note.txt", 1)},
		"snaps":  {f("rocky-9-1111111111111111.qcow2", 1), f("rocky-9-2222222222222222.qcow2", 2), f("rocky-9-3333333333333333.qcow2", 3)},
		"tmp":    {f("x.qcow2.tmp-12", now-90000), f("y.qcow2.tmp-13", now-100), f("z.qcow2", 1)},
	}
	got := strings.Join(vmcheck.PrunePlan(files, 1, now), " ")
	want := "/c/fedora-45-aaaaaaaaaaaaaaaa.qcow2 /c/rocky-9-2222222222222222.qcow2 /c/rocky-9-1111111111111111.qcow2 /c/x.qcow2.tmp-12"
	if got != want {
		t.Fatalf("\ngot  %s\nwant %s", got, want)
	}
}

// The catalogue is the data of scripts/delivery/vm-images.json (plugin-owned, not edited here). A
// difference means the plugin changed it: copy the new file to internal/vmcheck/vm-images.json.
func TestCatalogueMatchesThePluginFile(t *testing.T) {
	b, err := os.ReadFile("../../../../scripts/delivery/vm-images.json")
	if err != nil {
		t.Skip("plugin file not in this checkout")
	}
	own, err := vmcheck.Load()
	if err != nil {
		t.Fatal(err)
	}
	plugin, err := vmcheck.LoadOnly(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(own, plugin) {
		t.Fatalf("internal/vmcheck/vm-images.json differs from scripts/delivery/vm-images.json:\n%v\n%v", own.Names(), plugin.Names())
	}
}
