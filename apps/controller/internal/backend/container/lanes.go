package container

import (
	"errors"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// LaneInside is where the folders of a lane are inside a runner container (caches R5.43).
var LaneInside = runnercache.LaneDirs{
	Tools:     "/ci-tools",
	Rust:      "/rust-target",
	ToolCache: "/opt/hostedtoolcache",
	Wine:      "/wine",
	Browsers:  "/ms-playwright",
}

// LaneMountArgs returns the `docker run` arguments that mount a lane read-write
// and give the job the variables that find it: CI_TOOLS_DIR, CARGO_TARGET_DIR
// and RUNNER_TOOL_CACHE, and nothing else (caches R5.44). hostLane holds the
// paths the engine sees. A socket path is refused (runners R6.1).
func LaneMountArgs(hostLane runnercache.LaneDirs) ([]string, error) {
	pairs := []struct{ host, inside string }{
		{hostLane.Tools, LaneInside.Tools},
		{hostLane.Rust, LaneInside.Rust},
		{hostLane.ToolCache, LaneInside.ToolCache},
		{hostLane.Wine, LaneInside.Wine},
		{hostLane.Browsers, LaneInside.Browsers},
	}
	var args []string
	for _, p := range pairs {
		if p.host == "" {
			return nil, errors.New("the lane has an empty folder")
		}
		if IsSocketPath(p.host) {
			return nil, ErrSocketMount
		}
		if strings.ContainsAny(p.host, ",\n") || (strings.Contains(p.host, ":") && !isWindowsDrivePath(p.host)) {
			return nil, errors.New("a lane path has a character a mount cannot carry")
		}
		args = append(args, "--mount", "type=bind,source="+p.host+",target="+p.inside)
	}
	vars := runnercache.LaneVars(LaneInside)
	for _, k := range runnercache.JobCacheVariables {
		args = append(args, "-e", k+"="+vars[k])
	}
	return args, nil
}
