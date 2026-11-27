package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStageFileRoundTripAndMissing(t *testing.T) {
	dir := t.TempDir()
	if _, ok := ReadStage(dir); ok {
		t.Fatal("no stage file yet")
	}
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	want := Stage{Name: StageDownloading, Percent: 42, Version: "1.5.0", At: at}
	if err := WriteStage(dir, want); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadStage(dir)
	if !ok || got.Name != want.Name || got.Percent != 42 || got.Version != "1.5.0" || !got.At.Equal(at) {
		t.Fatalf("%+v %v", got, ok)
	}
	// It sits beside result.json and is replaced whole, never half written.
	if _, err := os.Stat(filepath.Join(dir, StageFile+".tmp")); err == nil {
		t.Fatal("the temporary file must be renamed away")
	}
	_ = os.WriteFile(filepath.Join(dir, StageFile), []byte("{not json"), 0o600)
	if _, ok := ReadStage(dir); ok {
		t.Fatal("a broken file is no stage")
	}
}

func TestPercentOfDownload(t *testing.T) {
	for _, c := range []struct {
		done, total int64
		want        int
	}{{0, 100, 0}, {50, 200, 25}, {200, 200, 100}, {300, 200, 100}, {5, 0, -1}} {
		if got := Percent(c.done, c.total); got != c.want {
			t.Errorf("Percent(%d,%d)=%d want %d", c.done, c.total, got, c.want)
		}
	}
}

func msiLog(actions ...string) []byte {
	var b strings.Builder
	b.WriteString("=== Verbose logging started: 17.11.2026  10:00:00 ===\n")
	for _, a := range actions {
		b.WriteString("Action start 10:00:01: " + a + ".\n")
	}
	return []byte(b.String())
}

func TestMSIPercentFromTheVerboseLog(t *testing.T) {
	if got := MSIPercent(nil); got != 0 {
		t.Fatalf("empty log: %d", got)
	}
	early := MSIPercent(msiLog("INSTALL", "CostInitialize"))
	mid := MSIPercent(msiLog("INSTALL", "CostInitialize", "StopServices", "InstallFiles"))
	late := MSIPercent(msiLog("INSTALL", "InstallFiles", "StartServices", "PublishProduct"))
	if early <= 0 || early >= mid || mid >= late || late >= 100 {
		t.Fatalf("percent must grow and stay below 100 until the end line: %d %d %d", early, mid, late)
	}
	// An action that is not in the sequence does not move the bar back.
	if got := MSIPercent(msiLog("InstallFiles", "SomeCustomAction")); got != MSIPercent(msiLog("InstallFiles")) {
		t.Fatalf("unknown action moved the bar: %d", got)
	}
	done := append(msiLog("InstallFinalize"), []byte("MainEngineThread is returning 0\n")...)
	if got := MSIPercent(done); got != 100 {
		t.Fatalf("finished log: %d", got)
	}
}

func TestMSIPercentReadsUTF16Logs(t *testing.T) {
	src := msiLog("INSTALL", "InstallFiles")
	b := []byte{0xFF, 0xFE}
	for _, c := range src {
		b = append(b, c, 0)
	}
	if got := MSIPercent(b); got != MSIPercent(src) || got == 0 {
		t.Fatalf("utf-16: %d", got)
	}
}

func TestManagerWritesTheStagesOfAnUpdate(t *testing.T) {
	h := newHarness(t, "linux", false, true)
	ctx := context.Background()
	dir := filepath.Join(h.dir, "updates")
	var seen []string
	h.m.o.Fetch = func(ctx context.Context, url, dst string) error {
		if strings.HasSuffix(url, "SHA256SUMS") {
			return os.WriteFile(dst, []byte(sum([]byte("payload"))+"  "+VersionedName(AssetName("linux", "amd64"), "1.5.0")+"\n"), 0o600)
		}
		reportProgress(ctx, 50, 100)
		if s, ok := ReadStage(dir); ok {
			seen = append(seen, s.Name+":"+itoa(s.Percent))
		}
		reportProgress(ctx, 100, 100)
		return os.WriteFile(dst, []byte("payload"), 0o600)
	}
	h.d.jobs = []Job{{Repository: "luis-mc/app", RunID: 1, Job: "build"}}
	if _, err := h.m.Apply(ctx, KindUpdate, true, "octo"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "downloading:50" {
		t.Fatalf("the download percent must reach the stage file: %v", seen)
	}
	if s, _ := ReadStage(dir); s.Name != StageWaitingForJobs || s.Version != "1.5.0" {
		t.Fatalf("after the download it waits for jobs: %+v", s)
	}
	if st := h.m.Status(ctx); st.Stage == nil || st.Stage.Name != StageWaitingForJobs {
		t.Fatalf("the status carries the stage: %+v", st.Stage)
	}
	h.d.jobs = nil
	if err := h.m.OnIdle(ctx, KindUpdate); err != nil {
		t.Fatal(err)
	}
	if s, _ := ReadStage(dir); s.Name != StageRestarting {
		t.Fatalf("the helper took over: %+v", s)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestManagerStageFailedCarriesErrorAndLog(t *testing.T) {
	h := newHarness(t, "linux", false, false) // bad checksum
	if _, err := h.m.Apply(context.Background(), KindUpdate, true, "octo"); err == nil {
		t.Fatal("a bad checksum must refuse")
	}
	s, ok := ReadStage(filepath.Join(h.dir, "updates"))
	if !ok || s.Name != StageFailed || s.Error == "" || !strings.HasSuffix(s.LogPath, LogFile) || s.FailedAt != StageDownloading {
		t.Fatalf("%+v", s)
	}
}

func TestHelperWritesDoneAndFailedStages(t *testing.T) {
	p, _, dir := unixPlan(t, "linux")
	if err := RunPlan(context.Background(), p, helperEnv(&execLog{}, func(string) (string, error) { return "1.5.0", nil })); err != nil {
		t.Fatal(err)
	}
	if s, _ := ReadStage(dir); s.Name != StageDone || s.Version != "1.5.0" || s.Percent != 100 {
		t.Fatalf("%+v", s)
	}
	p, _, dir = unixPlan(t, "linux")
	p.HealthTimeout = time.Millisecond
	env := helperEnv(&execLog{}, func(string) (string, error) { return "1.4.0", nil })
	if err := RunPlan(context.Background(), p, env); err == nil {
		t.Fatal("an unhealthy build must fail")
	}
	if s, _ := ReadStage(dir); s.Name != StageFailed || s.Error == "" || s.LogPath != filepath.Join(dir, LogFile) || s.FailedAt != StageInstalling {
		t.Fatalf("%+v", s)
	}
}

func TestRecoverMSIWritesDoneAndFailedStage(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "msi.log")
	now := time.Now()
	pendingFor(t, dir, log, now)
	if !RecoverMSI(dir, "1.5.0", now) {
		t.Fatal("settled")
	}
	if s, _ := ReadStage(dir); s.Name != StageDone || s.Version != "1.5.0" {
		t.Fatalf("%+v", s)
	}
	pendingFor(t, dir, log, now)
	_ = os.WriteFile(log, []byte("MainEngineThread is returning 1603\n"), 0o600)
	if !RecoverMSI(dir, "1.4.0", now) {
		t.Fatal("settled")
	}
	if s, _ := ReadStage(dir); s.Name != StageFailed || s.LogPath != log || !strings.Contains(s.Error, "1603") {
		t.Fatalf("%+v", s)
	}
}

func TestHTTPFetchReportsProgress(t *testing.T) {
	body := strings.Repeat("x", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	var last, total int64
	ctx := WithProgress(context.Background(), func(d, tt int64) { last, total = d, tt })
	if err := httpFetch(ctx, srv.URL, filepath.Join(t.TempDir(), "f")); err != nil {
		t.Fatal(err)
	}
	if last != 4096 || total != 4096 {
		t.Fatalf("progress %d of %d", last, total)
	}
}
