package updatewindow

import (
	"flag"
	"os"
	"path/filepath"
)

// RunLifecycle is `crsp lifecycle-window <question>`: a question window of the lifecycle
// standard. Today it has one question, remove. It returns the exit code of the process:
// ExitKeep (10), ExitDelete (11) or ExitCancel (1); 2 for bad arguments.
//
//	crsp lifecycle-window remove [--product NAME] [--size GB] [--path DIR] [--layout-check]
func RunLifecycle(o Options) int {
	if len(o.Args) == 0 || o.Args[0] != "remove" {
		say(o.Stderr, "usage: crsp lifecycle-window remove [--size GB] [--path DIR]")
		return 2
	}
	fs := flag.NewFlagSet("lifecycle-window remove", flag.ContinueOnError)
	fs.SetOutput(o.Stderr)
	c := Config{Mode: ModeRemove}
	fs.StringVar(&c.Product, "product", "CI Runners Shared Pool", "the product name the window shows")
	fs.StringVar(&c.Path, "path", "", "the data folder (default: the crsp folder under ProgramData)")
	fs.StringVar(&c.Size, "size", "", "the size of the data in GB (default: measured from --path)")
	fs.BoolVar(&c.LayoutCheck, "layout-check", false, "build the window off screen for every state and DPI; exit 1 when anything is clipped")
	fs.StringVar(&c.Shots, "screenshots", "", "with --layout-check: write PNG files into this folder")
	if err := fs.Parse(o.Args[1:]); err != nil {
		return 2
	}
	if c.Path == "" {
		c.Path = filepath.Join(os.Getenv("ProgramData"), "crsp")
	}
	if c.Size == "" {
		c.Size = DataSizeGB(c.Path)
	}
	if c.LayoutCheck {
		return layoutCheck(c, o)
	}
	code := runWindow(c, o)
	if code == 0 {
		code = ExitCancel // the window closed without an answer
	}
	return code
}
