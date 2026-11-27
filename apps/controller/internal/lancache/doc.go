// Package lancache is the shared cache one pool machine serves to the others
// on the LAN (caches R5.50): a tool cache, a package cache and an Actions
// cache receiver, all in one content-addressed store.
//
// Only machines of this pool may read or write: every call carries the
// machine id and the machine credential of the agent link and is checked by
// the Authenticator (machines.Service.Authenticate). Entries are stored under
// the SHA-256 of their bytes and checked on write and on read. The store has a
// size cap and drops the least recently used entries. The server binds one
// private LAN address and never a public or wildcard one. A client that cannot
// reach the holder, or gets a wrong digest, falls back to the normal path.
package lancache
