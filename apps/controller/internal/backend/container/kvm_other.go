//go:build !linux

package container

// ProbeKVM has no device to read on this OS; the pool then starts without KVM.
func ProbeKVM() KVMFacts { return KVMFacts{} }
