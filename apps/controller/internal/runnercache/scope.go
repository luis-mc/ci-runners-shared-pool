package runnercache

// What crsp takes of `ci_tool.py` and `rust_ci.py` (caches R5.44, decision
// D308): only the cache parts. The pinned download, unpack and build logic and
// the `tools.json` format stay in the delivery scripts, because projects also
// run them on GitHub-hosted runners. A job finds the cache through
// CI_TOOLS_DIR and CARGO_TARGET_DIR only (runners R5.53); crsp never reads
// `tools.json`.

// JobCacheVariables are the variables a job reads to find the caches. The list
// is closed: a lane exports these two and RUNNER_TOOL_CACHE for
// `actions/setup-*`, and nothing else.
var JobCacheVariables = []string{EnvToolsDir, EnvRustDir, EnvToolCache}

// NotTaken names what stays in the delivery scripts (D308), for the doctor and
// the docs.
var NotTaken = []string{
	"pinned download and checksum of tools",
	"unpack of archives",
	"the tools.json format",
}
