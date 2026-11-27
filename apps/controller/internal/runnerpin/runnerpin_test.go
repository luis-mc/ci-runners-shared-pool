package runnerpin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

type kv map[string]string

func (k kv) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := k[key]
	return v, ok, nil
}
func (k kv) Set(_ context.Context, key, v string) error { k[key] = v; return nil }

func h(c string) string { return c + strings.Repeat("0123456789abcdef", 4)[:63] }

var notes = "x\n<!-- BEGIN SHA win-x64 -->" + h("a") + "<!-- END SHA win-x64 -->\n" +
	"<!-- BEGIN SHA linux-x64 -->" + h("b") + "<!-- END SHA linux-x64 -->\n" +
	"<!-- BEGIN SHA osx-arm64 -->" + h("c") + "<!-- END SHA osx-arm64 -->"

var day = 24 * time.Hour

func TestParseReleaseAndCompare(t *testing.T) {
	r := ParseRelease("v2.340.0", time.Unix(0, 0), notes)
	if r.Version != "2.340.0" || r.Hashes["win-x64"] != h("a") || len(r.Hashes) != 3 {
		t.Fatalf("%+v", r)
	}
	if Compare("2.338.2", "2.340.0") != -1 || Compare("2.340.0", "2.340") != 0 || Compare("2.341.0", "2.340.9") != 1 {
		t.Fatal("compare")
	}
}

// R5.20: the pin moves only after the release is 7 days old, and only with a hash for each pinned platform.
func TestDecideWaitsSevenDaysAndNeedsEveryHash(t *testing.T) {
	cur := []runners.Pin{{OS: "windows", Arch: "x64", Version: "2.338.2", SHA256: h("1")}, {OS: "linux", Arch: "x64", Version: "2.338.2", SHA256: h("2")}}
	pub := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	rel := ParseRelease("v2.340.0", pub, notes)
	if d := Decide(cur, rel, pub.Add(6*day), 7*day); d.Move || !strings.Contains(d.Reason, "moves at 7 days") {
		t.Fatalf("day 6: %+v", d)
	}
	d := Decide(cur, rel, pub.Add(7*day), 7*day)
	if !d.Move || d.To != "2.340.0" || d.Pins[0].SHA256 != h("a") || d.Pins[1].SHA256 != h("b") {
		t.Fatalf("day 7: %+v", d)
	}
	cur = append(cur, runners.Pin{OS: "windows", Arch: "arm64", Version: "2.338.2", SHA256: h("3")})
	if d := Decide(cur, rel, pub.Add(9*day), 7*day); d.Move || !strings.Contains(d.Reason, "windows/arm64") {
		t.Fatalf("missing hash: %+v", d)
	}
	same := ParseRelease("v2.338.2", pub, notes)
	if d := Decide(cur, same, pub.Add(30*day), 7*day); d.Move {
		t.Fatalf("same version: %+v", d)
	}
}

func TestBehindDays(t *testing.T) {
	cur := []runners.Pin{{OS: "linux", Arch: "x64", Version: "2.338.2", SHA256: h("2")}}
	pub := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	rel := ParseRelease("v2.340.0", pub, notes)
	if n := BehindDays(cur, rel, pub.Add(21*day+time.Hour)); n != 21 {
		t.Fatalf("%d", n)
	}
	if n := BehindDays(cur, ParseRelease("v2.338.2", pub, notes), pub.Add(40*day)); n != 0 {
		t.Fatalf("%d", n)
	}
}

func TestTickMovesKeepsAndAudits(t *testing.T) {
	t.Cleanup(func() { runners.SetStoredPins(nil) })
	runners.SetStoredPins(nil)
	pub := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	store := kv{}
	var audits []string
	moved := 0
	m := &Manager{
		Fetch: func(context.Context) (Release, error) {
			return ParseRelease("v9.0.0", pub, allHashes()), nil
		},
		KV: store, Now: func() time.Time { return pub.Add(8 * day) },
		Audit: func(_ context.Context, a, d string) { audits = append(audits, a+": "+d) },
		Moved: func(context.Context) { moved++ },
	}
	// The embedded pins cover all six platforms, so the notes must carry all six.
	m.Fetch = func(context.Context) (Release, error) { return ParseRelease("v9.0.0", pub, allHashes()), nil }
	d, err := m.Tick(context.Background())
	if err != nil || !d.Move {
		t.Fatalf("%+v %v", d, err)
	}
	p, err := runners.PinFor("linux", "x64")
	if err != nil || p.Version != "9.0.0" {
		t.Fatalf("PinFor after the move: %+v %v", p, err)
	}
	if store[SettingKey] == "" || len(audits) != 1 || !strings.HasPrefix(audits[0], "runner.pin.move: ") || moved != 1 {
		t.Fatalf("%v %v %d", store, audits, moved)
	}
	// A second start reads the stored pins back.
	runners.SetStoredPins(nil)
	if err := (&Manager{KV: store}).Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p, _ := runners.PinFor("macos", "arm64"); p.Version != "9.0.0" {
		t.Fatalf("after Load: %+v", p)
	}
	if st := m.Status(); !st.Known || st.Pinned != "9.0.0" || st.Latest != "9.0.0" || st.BehindDays != 0 {
		t.Fatalf("%+v", st)
	}
}

func allHashes() string {
	var b strings.Builder
	for i, name := range []string{"win-x64", "win-arm64", "osx-x64", "osx-arm64", "linux-x64", "linux-arm64"} {
		b.WriteString("<!-- BEGIN SHA " + name + " -->" + h(string(rune('a'+i))) + "<!-- END SHA " + name + " -->\n")
	}
	return b.String()
}

func TestFetchReadsTheLatestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/actions/runner/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v2.340.0","published_at":"2026-11-01T00:00:00Z","body":"` + strings.ReplaceAll(strings.ReplaceAll(notes, "\n", `\n`), `"`, `\"`) + `"}`))
	}))
	defer srv.Close()
	r, err := Fetch(context.Background(), srv.Client(), srv.URL+"/")
	if err != nil || r.Version != "2.340.0" || r.Hashes["linux-x64"] != h("b") {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := Fetch(context.Background(), srv.Client(), srv.URL+"/nope"); err == nil {
		t.Fatal("a 404 is an error")
	}
}
