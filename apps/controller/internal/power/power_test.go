package power_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/power"
)

func tree(entries map[string]map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, files := range entries {
		for f, v := range files {
			m[name+"/"+f] = &fstest.MapFile{Data: []byte(v + "\n")}
		}
	}
	return m
}

// R5.63: Linux reads /sys/class/power_supply; no battery and unreadable count as mains.
func TestLinuxSupplies(t *testing.T) {
	cases := []struct {
		name string
		fs   fstest.MapFS
		want power.State
	}{
		{"desktop, no supplies", fstest.MapFS{}, power.Mains},
		{"desktop with a mains entry only", tree(map[string]map[string]string{"AC": {"type": "Mains", "online": "1"}}), power.Mains},
		{"laptop on mains", tree(map[string]map[string]string{
			"BAT0": {"type": "Battery", "status": "Charging"}, "AC": {"type": "Mains", "online": "1"}}), power.State{Known: true}},
		{"laptop on battery", tree(map[string]map[string]string{
			"BAT0": {"type": "Battery", "status": "Discharging"}, "AC": {"type": "Mains", "online": "0"}}), power.State{OnBattery: true, Known: true}},
		{"battery full on mains, status Full", tree(map[string]map[string]string{
			"BAT0": {"type": "Battery", "status": "Full"}, "AC": {"type": "Mains", "online": "1"}}), power.State{Known: true}},
		{"battery only, discharging", tree(map[string]map[string]string{
			"BAT0": {"type": "Battery", "status": "Discharging"}}), power.State{OnBattery: true, Known: true}},
		{"battery only, charging", tree(map[string]map[string]string{
			"BAT0": {"type": "Battery", "status": "Charging"}}), power.State{Known: true}},
		{"battery with no readable state", tree(map[string]map[string]string{
			"BAT0": {"type": "Battery"}}), power.Mains},
		{"a mouse battery does not count", tree(map[string]map[string]string{
			"hid-1-battery": {"type": "Battery", "scope": "Device", "status": "Discharging"}}), power.Mains},
		{"usb charger online", tree(map[string]map[string]string{
			"BAT0": {"type": "Battery", "status": "Discharging"}, "ucsi": {"type": "USB", "online": "1"}}), power.State{Known: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := power.ParseLinuxSupplies(c.fs); got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// R5.63: macOS reads `pmset -g batt`.
func TestPmset(t *testing.T) {
	cases := []struct {
		name, out string
		want      power.State
	}{
		{"battery", "Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1) 81%; discharging; 4:12 remaining present: true\n", power.State{OnBattery: true, Known: true}},
		{"ac", "Now drawing from 'AC Power'\n -InternalBattery-0 (id=1) 100%; charged; 0:00 remaining present: true\n", power.State{Known: true}},
		{"desktop", "Now drawing from 'AC Power'\n", power.State{Known: true}},
		{"empty", "", power.Mains},
		{"garbage", "pmset: not found", power.Mains},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := power.ParsePmset(c.out); got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// R5.63: Windows reads GetSystemPowerStatus.
func TestSystemPowerStatus(t *testing.T) {
	cases := []struct {
		name        string
		ac, batFlag uint8
		want        power.State
	}{
		{"on battery", 0, 1, power.State{OnBattery: true, Known: true}},
		{"on mains", 1, 8, power.State{Known: true}},
		{"desktop without a battery", 1, 128, power.Mains},
		{"desktop reporting offline without a battery", 0, 128, power.Mains},
		{"unknown line status", 255, 1, power.Mains},
		{"unknown battery flag", 255, 255, power.Mains},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := power.FromSystemPowerStatus(c.ac, c.batFlag); got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestStaticAndOSReaders(t *testing.T) {
	if got := power.Static(power.State{OnBattery: true, Known: true}).Read(context.Background()); !got.OnBattery {
		t.Fatalf("static reader lost its state: %+v", got)
	}
	// The real reader must never panic; on a CI container it reads mains.
	_ = power.OS().Read(context.Background())
}
