//go:build !windows && !darwin

package toolstore

// DefaultCloner is the cloner of a Unix machine that is not a Mac: a tree of hard links. It is used by tests
// on Linux; the store itself serves Windows and macOS only.
func DefaultCloner() Cloner { return LinkCloner{} }
