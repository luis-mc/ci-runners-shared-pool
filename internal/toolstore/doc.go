// Package toolstore is the tool store with a private job home for Windows and macOS (runners R5.110 to
// R5.119, OPS75, tier 1). The scan gives a tool plan in the shape the Linux profiles use
// (registry.Toolchain); on Windows and macOS the plan is a lookup in a read-only store per machine, with
// versions side by side. The agent installs what is missing once, as its admin identity, each tool with its
// own manager (rustup, pyenv, SDKMAN, the official archive, dotnet-install), Node from the official nodejs.org
// archive with no version manager, then seals the store. Xcode is installed by the owner: the store only finds
// it and selects it with DEVELOPER_DIR in the job's environment. A job
// gets a private home, a PATH built from the plan and caches restored by hash; it never runs a manager
// against the store.
//
// Everything that touches the machine sits behind an interface (Installer, Fetcher, Cloner, Identity,
// TreeKiller) with fakes in fake.go, so a test never downloads, never runs a manager and never leaves its
// t.TempDir(). The real implementations are in the *_windows.go, *_darwin.go and *_other.go files.
package toolstore
