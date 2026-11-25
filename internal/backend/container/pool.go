package container

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/runnercache"
)

// The job user of the pool container: never root, no sudo (runners R5.27,
// R5.49; open question 13).
const (
	JobUser = "runner"
	JobUID  = 1001
	// RunnerHome and JobsDir hold one folder per runner. CleanUp removes it, so
	// nothing carries over to the next job (runners R5.26).
	RunnerHome = "/home/runner"
	JobsDir    = RunnerHome + "/jobs"
	// RunnerDir is the Actions runner in the image; each job gets its own copy.
	RunnerDir = "/opt/actions-runner"
	// KVMDevice is the only device a pool container may use, allowed by KVMCgroupRule (runners R5.27).
	KVMDevice = "/dev/kvm"
)

// Labels on every container and image the backend makes.
const (
	LabelPool  = "crsp.pool"
	LabelImage = "crsp.image-key"
)

// CapDrop is the one capability set of a pool container: every capability is
// dropped and none is added (runners R5.27; open question 14). No other set
// is built, so no flag adds one.
const CapDrop = "ALL"

// Work dirs of a pool container. The root file system is read-only (runners
// R5.63); these are the only places the job user can write, all tmpfs.
const (
	WorkTmpfsHome      = RunnerHome
	WorkTmpfsTmp       = "/tmp"
	WorkTmpfsVarTmp    = "/var/tmp"
	WorkTmpfsToolCache = "/opt/hostedtoolcache"
)

// Limits are the caps from the machine's load budget (runners R5.27).
type Limits struct {
	MemoryMB int
	CPUs     float64
}

// PoolSpec is one pool container: one per repository.
type PoolSpec struct {
	Owner, Repo string
	ImageTag    string
	ImageKey    string
	Limits      Limits
	// CPUSet pins the container to these CPUs (`--cpuset-cpus`), taken from the
	// machine's CPU budget by the backend (machines R5.54). Empty: not pinned.
	CPUSet string
	// KVM allows /dev/kvm with --device-cgroup-rule when the machine allows it for this
	// runner and the agent checked it (runners R5.27, R5.41).
	KVM bool
	// CrspHostPath, when set, is the agent's own linux binary, mounted
	// read-only (runners R5.48).
	CrspHostPath string
	// UID is the persistent uid/gid of this pool (runners R5.72); 0 means JobUID.
	UID int
	// KVMGroup is the group that owns /dev/kvm on the host; with KVM it is
	// added with --group-add so the job user can open the device.
	KVMGroup int
	// LaneRepoID asks for the cache lane of the repository; Lane holds its host
	// folders once the backend took it (caches R5.43, R5.44).
	LaneRepoID string
	Lane       *runnercache.LaneDirs
	// ToolCacheHost is the host folder of the machine's shared tool cache; it
	// is mounted into the container when set (caches R5.63).
	ToolCacheHost string
	// Env is the pool-wide job environment (runners R5.53): the browsers path
	// and the Wine prefix.
	Env map[string]string
	// Isolation is the container isolation mode (runners R5.65); empty is
	// process isolation, the engine default.
	Isolation Isolation
}

var nameRe = regexp.MustCompile(`[^a-z0-9]+`)

// PoolName is the container name for a repository: readable, and made unique
// by a hash of owner/repo, so two repositories never share one (runners R5.27).
func PoolName(owner, repo string) string {
	slug := strings.Trim(nameRe.ReplaceAllString(strings.ToLower(owner+"-"+repo), "-"), "-")
	if len(slug) > 36 {
		slug = strings.Trim(slug[:36], "-")
	}
	sum := sha256.Sum256([]byte(strings.ToLower(owner + "/" + repo)))
	return "crsp-pool-" + slug + "-" + hex.EncodeToString(sum[:])[:6]
}

// RunArgs builds the `docker run` arguments of a pool container. The result is
// always checked by CheckRunArgs before it is used.
func RunArgs(p PoolSpec) ([]string, error) {
	if p.Owner == "" || p.Repo == "" {
		return nil, errors.New("a pool container belongs to one repository: owner and repo are needed")
	}
	if err := ValidateImageRef(p.ImageTag); err != nil {
		return nil, err
	}
	name := PoolName(p.Owner, p.Repo)
	uid := p.UID
	if uid == 0 {
		uid = JobUID
	}
	a := []string{"run", "-d", "--name", name, "--hostname", name,
		"--user", strconv.Itoa(uid) + ":" + strconv.Itoa(uid),
		"--cap-drop", CapDrop,
		"--security-opt", "no-new-privileges",
		"--pids-limit", "8192",
		"--read-only",
		"--label", LabelPool + "=" + strings.ToLower(p.Owner+"/"+p.Repo),
		"--label", LabelImage + "=" + p.ImageKey,
	}
	a = append(a, IsolationArgs(p.Isolation)...)
	a = append(a, workTmpfsArgs(p.Lane != nil, uid)...)
	a = append(a, ResourceArgs(p.Limits.MemoryMB, p.CPUSet)...)
	if p.Limits.CPUs > 0 {
		a = append(a, "--cpus", strconv.FormatFloat(p.Limits.CPUs, 'f', -1, 64))
	}
	if p.KVM {
		a = append(a, KVMArgs(p.KVMGroup)...)
	}
	if p.CrspHostPath != "" {
		m, err := CrspMountArgs(p.CrspHostPath)
		if err != nil {
			return nil, err
		}
		a = append(a, m...)
	}
	if p.Lane != nil {
		m, err := LaneMountArgs(*p.Lane)
		if err != nil {
			return nil, err
		}
		a = append(a, m...)
	}
	if p.ToolCacheHost != "" {
		m, err := ToolCacheMountArgs(p.ToolCacheHost, p.Lane == nil)
		if err != nil {
			return nil, err
		}
		a = append(a, m...)
	}
	a = append(a, envArgs(p.Env)...)
	a = append(a, p.ImageTag, "sleep", "infinity")
	if err := CheckRunArgs(a); err != nil {
		return nil, err
	}
	return a, nil
}

// workTmpfsArgs makes the writable work dirs of a read-only root: the runner
// home (jobs and the job's own files), /tmp and /var/tmp. The tool cache is a
// tmpfs only when no cache lane mounts it. Sizes are caps, not reservations; the
// container's memory limit still bounds them. A build output is run from
// home, so exec stays on; setuid files and device nodes never work (nosuid,
// nodev).
func workTmpfsArgs(laneMountsToolCache bool, uid int) []string {
	own := fmt.Sprintf("uid=%d,gid=%d,mode=0755", uid, uid)
	a := []string{
		"--tmpfs", WorkTmpfsHome + ":rw,nosuid,nodev,size=8g," + own,
		"--tmpfs", WorkTmpfsTmp + ":rw,nosuid,nodev,size=2g,mode=1777",
		"--tmpfs", WorkTmpfsVarTmp + ":rw,nosuid,nodev,size=1g,mode=1777",
	}
	if !laneMountsToolCache {
		a = append(a, "--tmpfs", WorkTmpfsToolCache+":rw,nosuid,nodev,size=4g,"+own)
	}
	return a
}

// ErrUnsafeRun is returned for a `docker run` that would break a rule of
// runners R5.27 or R6.1.
var ErrUnsafeRun = errors.New("container run refused")

// CheckRunArgs refuses every `docker run` that is privileged, shares the host
// PID, network, IPC or user namespace, adds a capability, maps a device or
// allows a device other than the KVM rule, mounts an engine socket, or runs as root. It is the guard on
// the one place the backend starts a container (runners R5.27, R6.1).
func CheckRunArgs(args []string) error {
	refuse := func(why string) error { return fmt.Errorf("%w: %s", ErrUnsafeRun, why) }
	var user string
	dropped, noNewPrivs, readOnly := false, false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, val, hasVal := strings.Cut(a, "=")
		next := func() string {
			if hasVal {
				return val
			}
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch flag {
		case "--privileged":
			return fmt.Errorf("%w: %w: --privileged is never used", ErrUnsafeRun, ErrPrivileged)
		case "--pid", "--ipc", "--uts", "--userns", "--cgroupns":
			if v := next(); v == "host" || strings.HasPrefix(v, "container:") {
				return refuse(flag + " " + v + " shares the host")
			}
		case "--network", "--net":
			if v := next(); v == "host" || strings.HasPrefix(v, "container:") {
				return refuse("the host network is never shared")
			}
		case "--cap-add":
			return refuse("no capability is added: " + next())
		case "--cap-drop":
			if next() == CapDrop {
				dropped = true
			}
		case "--security-opt":
			v := next()
			if v == "no-new-privileges" || v == "no-new-privileges:true" {
				noNewPrivs = true
			} else if strings.Contains(v, "unconfined") {
				return refuse("a security profile is never turned off")
			} else if strings.HasPrefix(v, "seccomp=") || strings.HasPrefix(v, "seccomp:") {
				return refuse("the default seccomp profile is never replaced")
			}
		case "--read-only":
			readOnly = !hasVal || val == "true"
		case "--device":
			return refuse("no device is mapped, not even " + KVMDevice + ": " + next() + " (use the cgroup rule " + KVMCgroupRule + ")")
		case "--device-cgroup-rule":
			if v := next(); v != KVMCgroupRule {
				return refuse("the only device rule is " + KVMCgroupRule + ", not " + v)
			}
		case "-v", "--volume", "--mount":
			if v := next(); mentionsSocket(v) {
				return refuse(ErrSocketMount.Error())
			}
		case "-u", "--user":
			user = next()
		}
		if IsSocketPath(a) {
			return refuse(ErrSocketMount.Error())
		}
	}
	if user == "" || user == "0" || strings.HasPrefix(user, "0:") || user == "root" || strings.HasPrefix(user, "root:") {
		return refuse("the job user is never root")
	}
	if !readOnly {
		return refuse("the root file system is read-only; work dirs are tmpfs")
	}
	if !dropped || !noNewPrivs {
		return refuse("the one capability set is --cap-drop ALL with no-new-privileges")
	}
	return nil
}

// mentionsSocket is true when any part of a mount value, split at `,`, `:` and
// `=`, is the path of an engine socket.
func mentionsSocket(v string) bool {
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ':' || r == '=' }) {
		if IsSocketPath(part) {
			return true
		}
	}
	return IsSocketPath(v)
}
