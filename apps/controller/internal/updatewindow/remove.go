package updatewindow

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tray"
)

// Exit codes of `crsp lifecycle-window remove` (OPS69 reads them in the uninstall).
const (
	ExitCancel = 1  // Cancel, Escape or the close button: nothing is removed
	ExitKeep   = 10 // Remove with Keep my data
	ExitDelete = 11 // Remove with Delete my data too
)

// RemoveModel is the remove question: it asks every time, with no default. Remove stays
// disabled until Keep my data or Delete my data too is chosen (LC-24).
type RemoveModel struct {
	Product string
	Size    string // gigabytes, as text: "1.4"
	Path    string
	choice  ChoiceID
}

// NewRemoveModel starts the question with nothing chosen.
func NewRemoveModel(product, size, path string) *RemoveModel {
	return &RemoveModel{Product: product, Size: size, Path: path}
}

// Choose selects Keep my data or Delete my data too. An unknown ID changes nothing.
func (m *RemoveModel) Choose(c ChoiceID) {
	if c == ChoiceKeep || c == ChoicePurge {
		m.choice = c
	}
}

// Choice is what is chosen now; empty when nothing is.
func (m *RemoveModel) Choice() ChoiceID { return m.choice }

// Step moves the choice one down (+1) or up (-1), the arrow keys of a radio group. From nothing
// it lands on the first (down) or the last (up).
func (m *RemoveModel) Step(d int) {
	order := []ChoiceID{ChoiceKeep, ChoicePurge}
	i := -1
	for k, c := range order {
		if c == m.choice {
			i = k
		}
	}
	switch {
	case i < 0 && d > 0:
		i = 0
	case i < 0:
		i = len(order) - 1
	default:
		i = (i + d + len(order)) % len(order)
	}
	m.choice = order[i]
}

// ExitCode is what pressing Remove returns; ok is false while nothing is chosen.
func (m *RemoveModel) ExitCode() (code int, ok bool) {
	switch m.choice {
	case ChoiceKeep:
		return ExitKeep, true
	case ChoicePurge:
		return ExitDelete, true
	}
	return 0, false
}

// View is what the window draws: two radio choices, Cancel and Remove. No button is the default.
func (m *RemoveModel) View() View {
	var body []string
	if m.choice == "" {
		body = []string{tray.WordRemoveDisabledTil} // the reason Remove is disabled
	}
	return View{
		State:   "remove",
		Heading: tray.Fill(tray.WordRemoveTitle, "product", m.Product),
		Body:    body,
		Choices: []Choice{
			{ID: ChoiceKeep, Label: tray.WordRemoveKeep, Chosen: m.choice == ChoiceKeep},
			{ID: ChoicePurge, Label: tray.Fill(tray.WordRemovePurge, "size", m.Size, "path", m.Path), Chosen: m.choice == ChoicePurge},
		},
		Percent: -1,
		Buttons: []Button{
			{ID: BtnCancel, Label: tray.WordRemoveCancel},
			{ID: BtnRemove, Label: tray.Fill(tray.WordRemoveConfirm, "product", m.Product), Primary: true, Disabled: m.choice == ""},
		},
		Default:  "", // no default: Enter presses nothing
		EscapeIs: BtnCancel,
	}
}

// DataSizeGB is the size of the folder in gigabytes with one decimal ("1.4"); "0.0" when it
// cannot be read.
func DataSizeGB(dir string) string {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return formatGB(n)
}

func formatGB(n int64) string {
	tenths := (n*10 + (1 << 29)) / (1 << 30)
	return strconv.FormatInt(tenths/10, 10) + "." + strconv.FormatInt(tenths%10, 10)
}
