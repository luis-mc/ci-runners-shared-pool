// Package profiles holds the image profiles (IMG3, docs/features/registry
// R5.77 to R5.80): the named sets of layers a standard or custom image is
// built from. A profile is a base, toolchains, pins, services and the labels
// that pick it. This package stores profiles, seeds the defaults of the
// standard ones, maps a repository's stack to a profile, implements
// registry.ProfileSource and gives the read side the console needs.
package profiles
