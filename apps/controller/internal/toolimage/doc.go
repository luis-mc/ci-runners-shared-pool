// Package toolimage delivers prebuilt tool artifacts to VM jobs (runners
// R5.80, RES12). A job in a VM does not download its tools inside the VM; the
// agent pulls an OCI image of the tools by digest on the host, unpacks it once
// into a read-only disk image or a virtiofs share, and the VM backend attaches
// it at boot.
//
// The format of a tool artifact:
//
//   - An OCI image (manifest and layers, tar or tar+gzip). The merged layers
//     are the tool tree; its root is mounted at GuestPath in the VM.
//   - Referenced only by digest: name[:tag]@sha256:<64 hex>. A bare tag is
//     refused, because a tag can move.
//   - The manifest bytes must hash to the digest of the reference; each layer
//     blob must hash to the digest the manifest names. Any mismatch refuses
//     the whole image.
//   - Layers hold regular files and directories only. A link, device, fifo,
//     absolute path or ".." path is refused. Whiteout files (".wh.<name>",
//     ".wh..wh..opq") are applied.
//
// Everything outside the host (the registry, the disk packer, the VM) sits
// behind a small interface, so the tests use fakes and need no network, no
// Docker and no root.
package toolimage
