// Package runnercache keeps the caches of one persistent runner small: a
// declared quota per cache with eviction between jobs, a disk guard that holds
// new jobs when the volume is nearly full, and the environment values the
// runner exports to jobs (caches R5.14, R5.36 to R5.40; runners R5.39).
//
// Sizes are declared in configuration, never measured live; only the bytes a
// cache folder holds are counted, to compare them with the declared quota.
package runnercache
