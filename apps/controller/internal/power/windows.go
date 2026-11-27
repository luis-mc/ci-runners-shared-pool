package power

// The ACLineStatus and BatteryFlag values of SYSTEM_POWER_STATUS.
const (
	acOffline      = 0
	acOnline       = 1
	batteryNoBat   = 128 // no system battery
	batteryUnknown = 255
)

// FromSystemPowerStatus turns the two fields of GetSystemPowerStatus into a
// State: AC offline with a battery means on battery. No system battery, or an
// unknown line status, counts as on mains.
func FromSystemPowerStatus(acLineStatus, batteryFlag uint8) State {
	if batteryFlag&batteryNoBat != 0 && batteryFlag != batteryUnknown {
		return Mains
	}
	switch acLineStatus {
	case acOffline:
		return State{OnBattery: true, Known: true}
	case acOnline:
		return State{Known: true}
	}
	return Mains
}
