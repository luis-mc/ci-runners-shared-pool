package updatewindow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
)

// The web window (TRAY7, decisions tray D19 and D20): on macOS and Linux the update and the
// first start are a small web page that this process serves on 127.0.0.1 and opens in an app
// window of the browser, in its own profile so it opens at its own size. The page draws what a
// Screen says and sends the pressed button back; it holds no rule of its own. The states and
// the words are the ones of the Windows window (model.go), so a state is one test, not two.

// Window sizes of the two windows, width by height in pixels.
const (
	UpdateWidth, UpdateHeight         = 520, 440
	FirstStartWidth, FirstStartHeight = 560, 520
	webPollEvery                      = time.Second

	webInstallMax = 30 * time.Minute
	webAlivePath  = "alive"
)

// The page asks every second; silence for webGoneAfter is a closed window. The browser must
// show the page within webNeverOpenedAfter. Variables so a test can shorten them.
var (
	webGoneAfter        = 12 * time.Second
	webNeverOpenedAfter = 45 * time.Second
)

// PageButton is one button of the page. Busy is the word shown while it works.
type PageButton struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Primary  bool   `json:"primary"`
	Disabled bool   `json:"disabled"`
	Busy     string `json:"busy,omitempty"`
}

// PageOption is one choice of the page: a radio (Group set) or a tick box.
type PageOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
	On    bool   `json:"on"`
}

// PageView is everything the page draws for one step.
type PageView struct {
	State   string       `json:"state"`
	Step    string       `json:"step,omitempty"` // "Step 2 of 3" on a wizard
	Heading string       `json:"heading"`
	Body    []string     `json:"body"`
	Bar     string       `json:"bar"` // none, determinate, indeterminate
	Percent int          `json:"percent"`
	Tone    string       `json:"tone"` // normal, done, problem
	Buttons []PageButton `json:"buttons"`
	Options []PageOption `json:"options,omitempty"`
	Default string       `json:"default"`
	Escape  string       `json:"escape"`
}

// Screen is what the page shows and does. Each method may be called from another goroutine.
type Screen interface {
	Name() string // the instance name: one window per name
	Title() string
	Size() (w, h int)
	Page() PageView
	// Tick reads the outside world (the service); the engine calls it every second.
	Tick(ctx context.Context)
	// Press does what a button says. on holds the options as the page shows them. It returns
	// true when the window should close now.
	Press(id string, on map[string]bool) (closeNow bool)
}

// Opener opens url in a window of w by h pixels. It is the browser in real use.
type Opener func(url string, w, h int) error

// OSOpener opens the page in an app window of an own browser profile (D19).
func OSOpener() Opener {
	lookup := tray.OSBrowserLookup()
	return func(url string, w, h int) error { return tray.OpenProfileWindow(lookup, nil, url, w, h) }
}

// HasBrowser says a Chromium-family browser exists, so the page can open at its size.
func HasBrowser() bool {
	_, err := tray.OSBrowserLookup().Find()
	return err == nil
}

// Web serves one Screen.
type Web struct {
	Screen Screen
	Open   Opener
	// Dir holds the address file that makes the window single (the cache folder of crsp).
	Dir    string
	Stderr io.Writer

	token    string
	mu       sync.Mutex
	lastSeen time.Time
	seen     bool
	done     chan struct{}
	once     sync.Once
}

// token makes the secret in the address: another program on the machine cannot press a button.
func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (w *Web) stop() { w.once.Do(func() { close(w.done) }) }

// Handler is the page and its two calls. It is exported for the tests.
func (w *Web) Handler(port string) http.Handler {
	mux := http.NewServeMux()
	base := "/" + w.token + "/"
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(rw http.ResponseWriter, r *http.Request) {
			// Only the page itself, on the loopback address: a page of another site cannot
			// reach this server through a rebinding name or a form post.
			if r.Host != "127.0.0.1:"+port {
				http.Error(rw, "not found", http.StatusNotFound)
				return
			}
			rw.Header().Set("Cache-Control", "no-store")
			rw.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'")
			rw.Header().Set("X-Content-Type-Options", "nosniff")
			rw.Header().Set("Referrer-Policy", "no-referrer")
			h(rw, r)
		}
	}
	mux.HandleFunc(base, guard(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != base {
			http.NotFound(rw, r)
			return
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(rw, RenderPage(w.Screen.Title()))
	}))
	mux.HandleFunc(base+"band.png", guard(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "image/png")
		_ = png.Encode(rw, DrawBand(Phosphor, 1040, 112))
	}))
	mux.HandleFunc(base+"view", guard(func(rw http.ResponseWriter, _ *http.Request) {
		w.mu.Lock()
		w.lastSeen, w.seen = time.Now(), true
		w.mu.Unlock()
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(w.Screen.Page())
	}))
	mux.HandleFunc(base+webAlivePath, guard(func(rw http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(rw, "ok")
	}))
	mux.HandleFunc(base+"press", guard(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Crsp-Window") != "1" {
			http.Error(rw, "no", http.StatusBadRequest)
			return
		}
		var in struct {
			ID      string          `json:"id"`
			Options map[string]bool `json:"options"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 4096)).Decode(&in); err != nil {
			http.Error(rw, "bad request", http.StatusBadRequest)
			return
		}
		closeNow := w.Screen.Press(in.ID, in.Options)
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]bool{"close": closeNow})
		if closeNow {
			go func() { time.Sleep(200 * time.Millisecond); w.stop() }() // the page reads the answer first
		}
	}))
	return mux
}

// Run serves the window until a button closes it, the page goes quiet or ctx ends. It returns
// the exit code: 0 when the window was shown and closed, 1 when no window could be opened.
func (w *Web) Run(ctx context.Context) int {
	w.done = make(chan struct{})
	w.token = newToken()
	if w.Dir != "" {
		if w.focusRunning() {
			return 0 // one window per name: the first one shows
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		say(w.Stderr, "error: "+err.Error())
		return 1
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	srv := &http.Server{Handler: w.Handler(port), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	url := "http://127.0.0.1:" + port + "/" + w.token + "/"
	w.writeAddress(url)
	defer w.clearAddress()

	cw, ch := w.Screen.Size()
	if err := w.Open(url, cw, ch); err != nil {
		say(w.Stderr, "error: could not open the window: "+err.Error())
		return 1
	}
	started := time.Now()
	go func() {
		t := time.NewTicker(webPollEvery)
		defer t.Stop()
		for {
			select {
			case <-w.done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
				w.Screen.Tick(tctx)
				cancel()
			}
		}
	}()
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-w.done:
			return 0
		case <-ctx.Done():
			return 0
		case <-t.C:
			w.mu.Lock()
			seen, last := w.seen, w.lastSeen
			w.mu.Unlock()
			// Closing the browser window is Hide: the update goes on in the service.
			if seen && time.Since(last) > webGoneAfter {
				return 0
			}
			if !seen && time.Since(started) > webNeverOpenedAfter {
				say(w.Stderr, "error: the window did not open")
				return 1
			}
		}
	}
}

func (w *Web) addressFile() string {
	return filepath.Join(w.Dir, w.Screen.Name()+".addr")
}

func (w *Web) writeAddress(url string) {
	if w.Dir == "" {
		return
	}
	_ = os.MkdirAll(w.Dir, 0o700)
	_ = os.WriteFile(w.addressFile(), []byte(url), 0o600)
}

func (w *Web) clearAddress() {
	if w.Dir != "" {
		_ = os.Remove(w.addressFile())
	}
}

// focusRunning asks the window of an earlier start whether it lives. If so it opens the same
// address again: the browser, which keeps one profile process, raises the window it has.
func (w *Web) focusRunning() bool {
	b, err := os.ReadFile(w.addressFile())
	if err != nil {
		return false
	}
	url := strings.TrimSpace(string(b))
	cl := http.Client{Timeout: time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url+webAlivePath, nil)
	if err != nil {
		return false
	}
	resp, err := cl.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	cw, ch := w.Screen.Size()
	_ = w.Open(url, cw, ch)
	return true
}

// RunWeb opens the web window for a Screen with the real browser and the cache folder of crsp.
func RunWeb(s Screen, o Options) int {
	if !HasBrowser() {
		say(o.Stderr, "error: no Edge, Chrome or Chromium found for the window")
		return 1
	}
	dir := filepath.Dir(tray.ProfileDir())
	w := &Web{Screen: s, Open: OSOpener(), Dir: dir, Stderr: o.Stderr}
	return w.Run(context.Background())
}

// toPage turns the Windows model's View into the page's.
func toPage(v View, busy map[ButtonID]string) PageView {
	p := PageView{State: string(v.State), Heading: v.Heading, Body: append([]string{}, v.Body...),
		Percent: v.Percent, Default: string(v.Default), Escape: string(v.EscapeIs), Tone: "normal"}
	switch v.Bar {
	case BarDeterminate:
		p.Bar = "determinate"
	case BarIndeterminate:
		p.Bar = "indeterminate"
	default:
		p.Bar = "none"
	}
	switch v.Tone {
	case ToneDone:
		p.Tone = "done"
	case ToneProblem:
		p.Tone = "problem"
	}
	for _, b := range v.Buttons {
		pb := PageButton{ID: string(b.ID), Label: b.Label, Primary: b.Primary, Disabled: b.Disabled}
		if w, ok := busy[b.ID]; ok {
			pb.Busy, pb.Disabled = w, true
		}
		p.Buttons = append(p.Buttons, pb)
	}
	return p
}
