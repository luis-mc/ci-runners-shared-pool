// Package power says whether the machine runs on battery (machines R5.63).
//
// Windows reads GetSystemPowerStatus, Linux reads /sys/class/power_supply and
// macOS reads the output of `pmset -g batt`. Every parser takes its input as a
// value, so tests feed fakes for each OS. A battery state that cannot be read
// counts as on mains: a desktop has no battery and must never be paused for one.
package power

import "context"

// State is what the machine's power source is.
type State struct {
	// OnBattery is true only when the machine is known to run on battery.
	OnBattery bool
	// Known is false when no reading was possible; the machine then counts as
	// on mains.
	Known bool
}

// Mains is the state of a machine on mains or without a battery.
var Mains = State{}

// Reader reads the power source now.
type Reader interface {
	Read(ctx context.Context) State
}

// ReaderFunc makes a Reader of a function.
type ReaderFunc func(ctx context.Context) State

// Read implements Reader.
func (f ReaderFunc) Read(ctx context.Context) State { return f(ctx) }

// Static is a Reader that always answers one state (tests and desktops).
type Static State

// Read implements Reader.
func (s Static) Read(context.Context) State { return State(s) }

// OS returns the Reader of this build's OS.
func OS() Reader { return osReader{} }
