//go:build !unix

package main

import "time"

// cpuTime is not measured off Unix; the soak runs on Linux.
func cpuTime() time.Duration { return 0 }
