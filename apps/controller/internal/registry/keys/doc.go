// Package keys is REG3 of the registry (docs/features/registry R5.21 to
// R5.28): the Keyer that gives the key a run needs, the labels every image
// carries, the pointer rules and the update-of detection.
//
// The key arithmetic is in internal/registry/key.go and is frozen; this
// package feeds it. The place that picks the image of a job calls
// Keyer.KeyFor with the run's repository, commit, arch and labels, then asks
// the registry for that key (REG2) and builds only when the controller says
// the key is not there. Nothing here moves a custom pointer: that is REG7.
package keys
