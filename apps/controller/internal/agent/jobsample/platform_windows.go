//go:build windows

package jobsample

// NewPlatform returns the reader of Windows (OPS66 R5.62): a Job Object around the runner's process tree.
// Jobs is the object that creates and reads the Job Objects; the agent attaches a job to it at job start.
func NewPlatform() Platform {
	q := &OSJobQuerier{}
	return Platform{JobObject: NewJobObjectSampler(q), Jobs: q}
}
