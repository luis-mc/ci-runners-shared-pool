// Package mirror is the controller side of the pool's shared caches of
// milestone 7 (caches R5.65 to R5.72, CRSP272): the settings of the Actions
// cache receiver, the git mirror and the registry mirror, the git mirror itself,
// the warmth value per machine and repository, and the wipe with its plan.
//
// Every operating-system difference sits behind an interface: git runs through
// sys.Commands (no shell), folders are made through toolcache.Platform, and
// the clock and the file system are injected. The code builds and its tests
// run for Windows, Linux and macOS; macOS has not run on a real Mac.
package mirror
