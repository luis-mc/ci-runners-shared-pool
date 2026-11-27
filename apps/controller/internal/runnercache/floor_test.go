package runnercache

import (
	"errors"
	"strings"
	"testing"
)

const gibB = int64(1) << 30

// CRSP83: under the floor new work is held with a reason; it resumes one GiB
// above the floor, not at it.
func TestFloorHoldsAndResumesWithMargin(t *testing.T) {
	free := 50 * gibB
	f := &Floor{Bytes: FloorBytes(10), Path: "x", Read: func(string) (Disk, error) { return Disk{Free: free, Total: 500 * gibB}, nil }}
	if held, _ := f.Check(); held {
		t.Fatal("held with 50 GiB free")
	}
	free = 9 * gibB
	held, why := f.Check()
	if !held || !strings.Contains(why, "9.0 GiB") || !strings.Contains(why, "10.0 GiB") {
		t.Fatalf("held %v, reason %q", held, why)
	}
	free = 10 * gibB // at the floor: still held
	if held, _ := f.Check(); !held {
		t.Fatal("resumed at the floor itself")
	}
	free = 11 * gibB
	if held, _ := f.Check(); held {
		t.Fatal("still held 1 GiB above the floor")
	}
}

func TestFloorOffAndUnreadable(t *testing.T) {
	low := func(string) (Disk, error) { return Disk{Free: 1, Total: 100 * gibB}, nil }
	if held, _ := (&Floor{Bytes: 0, Read: low}).Check(); held {
		t.Fatal("a floor of 0 holds")
	}
	if FloorBytes(-1) != 0 || FloorSetting(0) != 10 || FloorSetting(-1) != 0 || FloorSetting(4) != 4 || FloorBytes(0) != 0 || FloorBytes(3) != 3*gibB {
		t.Fatal("FloorBytes")
	}
	f := &Floor{Bytes: 10 * gibB, Read: func(string) (Disk, error) { return Disk{}, errors.New("no volume") }}
	if held, _ := f.Check(); held {
		t.Fatal("an unreadable volume holds")
	}
}
