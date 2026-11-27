//go:build windows

package power

import (
	"context"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetSystemPowerStatus = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemPowerStatus")

// systemPowerStatus is SYSTEM_POWER_STATUS.
type systemPowerStatus struct {
	ACLineStatus        uint8
	BatteryFlag         uint8
	BatteryLifePercent  uint8
	SystemStatusFlag    uint8
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

type osReader struct{}

// Read implements Reader with GetSystemPowerStatus.
func (osReader) Read(context.Context) State {
	var s systemPowerStatus
	r, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s)))
	if r == 0 {
		return Mains
	}
	return FromSystemPowerStatus(s.ACLineStatus, s.BatteryFlag)
}
