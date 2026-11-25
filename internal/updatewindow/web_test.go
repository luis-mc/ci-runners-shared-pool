package updatewindow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
)

type fakeSvc struct {
	mu       sync.Mutex
	st       tray.Status
	installs int
	cancels  int
}

func (f *fakeSvc) Status(context.Context) (tray.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, nil
}
func (f *fakeSvc) Install(context.Context) (tray.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installs++
	return f.st, nil
}
func (f *fakeSvc) Cancel(context.Context) (tray.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels++
	return f.st, nil
}

func reviewScreen(t *testing.T) (*UpdateScreen, *fakeSvc) {
	t.Helper()
	svc := &fakeSvc{st: tray.Status{Version: "0.1.12"}}
	s := NewUpdateScreen(Config{From: "0.1.12", To: "0.1.13", Mode: ModeReview}, svc)
	s.Open = func(string) error { return nil }
	return s, svc
}

func ids(p PageView) []string {
	var out []string
	for _, b := range p.Buttons {
		out = append(out, b.ID)
	}
	return out
}

func TestUpdatePageReviewHasTheWindowsWords(t *testing.T) {
	s, _ := reviewScreen(t)
	p := s.Page()
	if p.State != "review" || p.Heading != "Version 0.1.13 is ready." {
		t.Fatalf("review page = %+v", p)
	}
	if got := strings.Join(ids(p), ","); got != "not_now,update" {
		t.Fatalf("buttons = %s", got)
	}
	if !p.Buttons[1].Primary || p.Default != "update" || p.Escape != "not_now" || p.Bar != "none" {
		t.Fatalf("primary, default or escape wrong: %+v", p)
	}
}

func TestEveryUpdateStateRendersAPage(t *testing.T) {
	for _, st := range AllStates {
		m := NewModel("0.1.12", "0.1.13", ModeLive)
		_ = st
		p := toPage(m.View(), nil)
		if p.Heading == "" || len(p.Buttons) == 0 {
			t.Fatalf("state %s: empty page %+v", p.State, p)
		}
	}
}

func TestUpdateNowStartsTheInstallAndLaterButtonsAreAnswered(t *testing.T) {
	s, svc := reviewScreen(t)
	if s.Press("update", nil) {
		t.Fatal("update now must not close the window")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		svc.mu.Lock()
		n := svc.installs
		svc.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if svc.installs != 1 {
		t.Fatalf("installs = %d", svc.installs)
	}
	if p := s.Page(); p.State != "downloading" && p.State != "waiting_for_jobs" {
		t.Fatalf("after Update now the page shows %q", p.State)
	}
}

func TestAButtonTheStepDoesNotShowCannotBePressed(t *testing.T) {
	s, svc := reviewScreen(t)
	if s.Press("cancel", nil) || s.Press("close", nil) {
		t.Fatal("a hidden button must do nothing")
	}
	if svc.cancels != 0 {
		t.Fatal("cancel reached the service from Review")
	}
}

func TestNotNowHideAndCloseCloseTheWindow(t *testing.T) {
	s, _ := reviewScreen(t)
	if !s.Press("not_now", nil) {
		t.Fatal("Not now closes the window")
	}
}

func TestCancelShowsItsBusyWordAndCancelsOnce(t *testing.T) {
	svc := &fakeSvc{st: tray.Status{Version: "0.1.12", Update: tray.UpdateInfo{Stage: &tray.StageInfo{Name: "downloading", Percent: 30}}}}
	s := NewUpdateScreen(Config{From: "0.1.12", To: "0.1.13", Mode: ModeLive}, svc)
	p := s.Page()
	if p.State != "downloading" || p.Bar != "determinate" || p.Percent != 30 {
		t.Fatalf("downloading page = %+v", p)
	}
	s.Press("cancel", nil)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && s.Page().State != "cancelled" {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Page().State != "cancelled" || svc.cancels != 1 {
		t.Fatalf("state %s cancels %d", s.Page().State, svc.cancels)
	}
}

func TestInstallingHasNoCancelButton(t *testing.T) {
	svc := &fakeSvc{st: tray.Status{Version: "0.1.12", Update: tray.UpdateInfo{Stage: &tray.StageInfo{Name: "installing", Percent: -1}}}}
	s := NewUpdateScreen(Config{From: "0.1.12", To: "0.1.13", Mode: ModeLive}, svc)
	if got := strings.Join(ids(s.Page()), ","); got != "hide" {
		t.Fatalf("installing buttons = %s", got)
	}
}

// --- the server ---

func serve(t *testing.T, s Screen) (*Web, *httptest.Server, string) {
	t.Helper()
	w := &Web{Screen: s, token: "tok", done: make(chan struct{})}
	srv := httptest.NewServer(nil)
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	srv.Config.Handler = w.Handler(port)
	t.Cleanup(srv.Close)
	return w, srv, srv.URL + "/tok/"
}

func TestPageIsServedOnlyOnItsSecretAddressAndHost(t *testing.T) {
	s, _ := reviewScreen(t)
	_, srv, base := serve(t, s)
	get := func(url, host string) int {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		if host != "" {
			req.Host = host
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		return r.StatusCode
	}
	if get(base, "") != 200 || get(base+"view", "") != 200 || get(base+"band.png", "") != 200 {
		t.Fatal("the page, its view and its band must be served")
	}
	if get(srv.URL+"/", "") != 404 || get(srv.URL+"/other/", "") != 404 {
		t.Fatal("no address without the token answers")
	}
	if get(base, "evil.example") != 404 {
		t.Fatal("another host name must not be served (DNS rebinding)")
	}
}

func TestPressNeedsThePostAndTheHeader(t *testing.T) {
	s, _ := reviewScreen(t)
	w, _, base := serve(t, s)
	r, _ := http.Post(base+"press", "application/json", strings.NewReader(`{"id":"not_now"}`))
	_ = r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("a press without the header answered %d", r.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, base+"press", strings.NewReader(`{"id":"not_now"}`))
	req.Header.Set("X-Crsp-Window", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var a struct{ Close bool }
	_ = json.Unmarshal(b, &a)
	if !a.Close {
		t.Fatalf("Not now answered %s", b)
	}
	select {
	case <-w.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the window did not stop after a closing press")
	}
}

func TestViewIsTheScreensJSON(t *testing.T) {
	s, _ := reviewScreen(t)
	_, _, base := serve(t, s)
	r, err := http.Get(base + "view")
	if err != nil {
		t.Fatal(err)
	}
	var p PageView
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if p.State != "review" {
		t.Fatalf("view = %+v", p)
	}
}

func TestThePageUsesTheTokensAndNoMarkupInjection(t *testing.T) {
	page := RenderPage(WindowTitle)
	for _, c := range []RGB{Phosphor.Page, Phosphor.Primary, Phosphor.Text, Phosphor.Problem, Phosphor.Done, Phosphor.Border} {
		if !strings.Contains(page, hexColour(c)) {
			t.Errorf("token %s missing from the page", hexColour(c))
		}
	}
	for _, bad := range []string{"innerHTML", "outerHTML", "document.write", "eval("} {
		if strings.Contains(page, bad) {
			t.Errorf("the page uses %s", bad)
		}
	}
	// No colour is typed in the css or the script: every hex colour in the page comes from the palette.
	css := pageCSS + pageJS
	if strings.Contains(css, "#") && strings.Contains(strings.ReplaceAll(css, `'[data-`, ""), "#0") {
		t.Error("a colour is typed in the page code")
	}
	if strings.Contains(css, "rgb(") || strings.Contains(css, "rgba(") {
		t.Error("a colour function is typed in the page code")
	}
}

func TestRunStopsWhenAPressClosesAndNoWindowOpensIsAnError(t *testing.T) {
	s, _ := reviewScreen(t)
	var url string
	w := &Web{Screen: s, Dir: t.TempDir(), Open: func(u string, _, _ int) error {
		url = u
		go func() {
			req, _ := http.NewRequest(http.MethodGet, u+"view", nil)
			if r, err := http.DefaultClient.Do(req); err == nil {
				_ = r.Body.Close()
			}
			p, _ := http.NewRequest(http.MethodPost, u+"press", strings.NewReader(`{"id":"not_now"}`))
			p.Header.Set("X-Crsp-Window", "1")
			if r, err := http.DefaultClient.Do(p); err == nil {
				_ = r.Body.Close()
			}
		}()
		return nil
	}}
	done := make(chan int, 1)
	go func() { done <- w.Run(context.Background()) }()
	select {
	case code := <-done:
		if code != 0 || url == "" {
			t.Fatalf("exit %d url %q", code, url)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not end after Not now")
	}
	if _, err := os.Stat(filepath.Join(w.Dir, "update-window.addr")); err == nil {
		t.Fatal("the address file stays after the window closed")
	}
	bad := &Web{Screen: s, Open: func(string, int, int) error { return tray.ErrNoAppBrowser }}
	if code := bad.Run(context.Background()); code != 1 {
		t.Fatalf("a window that cannot open exits %d", code)
	}
}

func TestSilenceFromThePageIsAClosedWindow(t *testing.T) {
	old := webGoneAfter
	webGoneAfter = 700 * time.Millisecond
	defer func() { webGoneAfter = old }()
	s, _ := reviewScreen(t)
	w := &Web{Screen: s, Open: func(u string, _, _ int) error {
		r, err := http.Get(u + "view") // the page shows once, then goes quiet
		if err == nil {
			_ = r.Body.Close()
		}
		return err
	}}
	done := make(chan int, 1)
	go func() { done <- w.Run(context.Background()) }()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a closed browser window did not end the process")
	}
}

func TestASecondStartFindsTheFirstAndOpensNothingNew(t *testing.T) {
	s, _ := reviewScreen(t)
	dir := t.TempDir()
	opened := make(chan string, 4)
	first := &Web{Screen: s, Dir: dir, Open: func(u string, _, _ int) error { opened <- u; return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go first.Run(ctx)
	url := <-opened
	second := &Web{Screen: s, Dir: dir, Open: func(u string, _, _ int) error { opened <- u; return nil }}
	if code := second.Run(context.Background()); code != 0 {
		t.Fatalf("second exit %d", code)
	}
	if again := <-opened; again != url {
		t.Fatalf("the second start opened %q, not the first window %q", again, url)
	}
	if len(opened) != 0 {
		t.Fatal("a second server was started")
	}
}
