package main

// The Windows icon of crsp.exe: the healthy CRSP disc, drawn by internal/tray.
// `go generate ./apps/controller/cmd/crsp` rewrites winres/crsp.ico and the committed
// rsrc_windows_*.syso files (pure Go, no cgo, runs on Linux). The go linker
// picks the .syso files up by their file name on Windows builds only.
//
//go:generate go run ../../internal/tray/geniconcmd winres/crsp.ico ../../../../apps/controller-console/public/crsp-icon.png
//go:generate go run github.com/tc-hib/go-winres@v0.3.3 make --in winres/winres.json --out rsrc --arch amd64,arm64
