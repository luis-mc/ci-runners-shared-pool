//go:build !linux && !darwin && !windows

package jobsample

// NewPlatform has no reader on this system: every sample is unknown with a reason.
func NewPlatform() Platform { return Platform{} }
