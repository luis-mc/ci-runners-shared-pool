package updatewindow

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
)

// Service is the local channel as the window uses it. *tray.Client implements it.
type Service interface {
	Status(ctx context.Context) (tray.Status, error)
	Install(ctx context.Context) (tray.Status, error)
	Cancel(ctx context.Context) (tray.Status, error)
}

var _ Service = (*tray.Client)(nil)

// Options configure Run.
type Options struct {
	Args    []string
	Service Service
	Stdout  io.Writer
	Stderr  io.Writer
}

// Config is what the arguments say.
type Config struct {
	From, To    string
	Mode        Mode
	At          [2]int // a point on the screen of the tray icon; centred there
	HasAt       bool
	LayoutCheck bool
	Web         bool   // the web page instead of the native window (the only window off Windows)
	Shots       string // layout check: write a PNG of every state and DPI into this folder
	// The remove question (ModeRemove): the product, the size of its data in GB and the folder.
	Product, Size, Path string
}

// ParseArgs reads the arguments of the hidden `crsp update-window` command.
func ParseArgs(args []string, stderr io.Writer) (Config, error) {
	fs := flag.NewFlagSet("update-window", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c Config
	mode := fs.String("mode", string(ModeReview), "review (the question) or live (what runs now)")
	at := fs.String("at", "", "x,y: a point on the screen where the tray icon is; the window centres there")
	fs.BoolVar(&c.Web, "web", false, "open the web page window (macOS and Linux always do)")
	fs.StringVar(&c.From, "from", "", "the version that runs")
	fs.StringVar(&c.To, "to", "", "the version offered")
	fs.BoolVar(&c.LayoutCheck, "layout-check", false, "build the window off screen for every state and DPI; exit 1 when anything is clipped")
	fs.StringVar(&c.Shots, "screenshots", "", "with --layout-check: write PNG files of every state into this folder")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	switch Mode(*mode) {
	case ModeReview, ModeLive:
		c.Mode = Mode(*mode)
	default:
		return c, fmt.Errorf("mode %q is review or live", *mode)
	}
	if *at != "" {
		xs, ys, ok := strings.Cut(*at, ",")
		x, ex := strconv.Atoi(xs)
		y, ey := strconv.Atoi(ys)
		if !ok || ex != nil || ey != nil {
			return c, fmt.Errorf("--at wants x,y: %q", *at)
		}
		c.At, c.HasAt = [2]int{x, y}, true
	}
	return c, nil
}

// Run is `crsp update-window`: the one native window of the update. It returns the exit code.
func Run(o Options) int {
	c, err := ParseArgs(o.Args, o.Stderr)
	if err != nil {
		if o.Stderr != nil {
			_, _ = fmt.Fprintln(o.Stderr, "error:", err)
		}
		return 2
	}
	if c.LayoutCheck {
		return layoutCheck(c, o)
	}
	if c.Web && c.Mode != ModeRemove {
		return RunWeb(NewUpdateScreen(c, o.Service), o)
	}
	return runWindow(c, o)
}

func say(w io.Writer, s string) {
	if w != nil {
		_, _ = fmt.Fprintln(w, s)
	}
}
