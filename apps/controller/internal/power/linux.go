package power

import (
	"io/fs"
	"path"
	"strings"
)

// ParseLinuxSupplies reads a power_supply tree (the contents of
// /sys/class/power_supply) and says whether the machine runs on battery.
//
// A machine with no supply of type Battery is on mains. With a battery, any
// Mains, USB or Wireless supply that is online means mains; supplies that are
// all offline mean battery. When the tree lists a battery but no other supply,
// the battery's status decides: Discharging is on battery.
func ParseLinuxSupplies(root fs.FS) State {
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		return Mains
	}
	var haveBattery, haveSource, sourceOnline, discharging, statusKnown bool
	for _, e := range entries {
		kind := strings.TrimSpace(readFile(root, path.Join(e.Name(), "type")))
		switch strings.ToLower(kind) {
		case "battery":
			// A battery of a peripheral (a mouse, a headset) has scope Device.
			if strings.EqualFold(strings.TrimSpace(readFile(root, path.Join(e.Name(), "scope"))), "device") {
				continue
			}
			haveBattery = true
			if st := strings.TrimSpace(readFile(root, path.Join(e.Name(), "status"))); st != "" {
				statusKnown = true
				if strings.EqualFold(st, "discharging") {
					discharging = true
				}
			}
		case "mains", "usb", "wireless", "ups":
			online := strings.TrimSpace(readFile(root, path.Join(e.Name(), "online")))
			if online == "" {
				continue
			}
			haveSource = true
			if online != "0" {
				sourceOnline = true
			}
		}
	}
	switch {
	case !haveBattery:
		return Mains
	case haveSource:
		return State{OnBattery: !sourceOnline, Known: true}
	case statusKnown:
		return State{OnBattery: discharging, Known: true}
	}
	return Mains
}

func readFile(root fs.FS, name string) string {
	b, err := fs.ReadFile(root, name)
	if err != nil {
		return ""
	}
	return string(b)
}
