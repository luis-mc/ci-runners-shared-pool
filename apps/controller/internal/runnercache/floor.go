package runnercache

import "fmt"

// DefaultDiskFloorGiB is the free space a machine needs before it starts a job
// or a VM (runners R5.83). 0 turns the floor off.
const DefaultDiskFloorGiB = 10

// floorResume is added to the floor before a held machine takes jobs again, so
// the floor does not flap at the limit.
const floorResume = int64(1) << 30

// Floor holds new work while the free space of a volume is under a number of
// bytes. It is the absolute twin of Guard (a percentage). The zero value never
// holds. It is not safe for concurrent use; the gate calls it under its lock.
type Floor struct {
	// Bytes is the floor; 0 or less never holds.
	Bytes int64
	// Path is the folder whose volume is read.
	Path string
	// Read reads a volume; nil reads the OS (OSDisk).
	Read func(path string) (Disk, error)

	held bool
}

// FloorBytes turns a setting in GiB into bytes. Zero or less is no floor.
func FloorBytes(gib int) int64 {
	if gib <= 0 {
		return 0
	}
	return int64(gib) << 30
}

// FloorSetting resolves a configured value: 0 or absent takes the default, a
// negative value turns the floor off.
func FloorSetting(gib int) int {
	switch {
	case gib == 0:
		return DefaultDiskFloorGiB
	case gib < 0:
		return 0
	}
	return gib
}

// Check reads the disk and says whether new work is held, with the reason. A
// volume that cannot be read never holds (the doctor reports a missing figure).
func (f *Floor) Check() (held bool, reason string) {
	if f.Bytes <= 0 {
		return false, ""
	}
	read := f.Read
	if read == nil {
		read = OSDisk
	}
	d, err := read(f.Path)
	if err != nil || d.Total <= 0 {
		return f.held, ""
	}
	switch {
	case !f.held && d.Free < f.Bytes:
		f.held = true
	case f.held && d.Free >= f.Bytes+floorResume:
		f.held = false
	}
	if !f.held {
		return false, ""
	}
	return true, fmt.Sprintf("%s free, under the floor of %s", gibText(d.Free), gibText(f.Bytes))
}

func gibText(b int64) string { return fmt.Sprintf("%.1f GiB", float64(b)/float64(int64(1)<<30)) }
