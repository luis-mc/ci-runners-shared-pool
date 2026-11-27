// Package toolcache is the shared per-machine tool cache of the pool (caches
// R5.14, R5.18, R5.19, R5.61 to R5.64): one folder on the host that survives
// jobs and is mounted into every ephemeral pool container.
//
// An entry is `<tool>/<version>`, a whole folder. It is built in a stage folder
// on the same volume and renamed into place in one step, so two jobs never see a
// half-written entry and a job that loses the rename race uses the winner's
// entry. A size cap evicts the least recently used entries; an entry used
// within MinAge is never evicted, and an entry is renamed out of sight before it
// is deleted. The doctor reads the store through the Checker interface; the
// doctor code itself lives elsewhere.
//
// Everything that differs by operating system sits behind Platform and FS, so
// the logic runs the same on Windows, Linux and macOS. macOS is compiled and
// unit-tested in CI only; it has not run on a real Mac.
package toolcache
