package reconcile

import "context"

type classifier struct{ runners Runners }

// NewClassifier makes the Classify of R5.39: a runner name crsp made is Crsp, the group HostedGroup is Hosted,
// any other named runner is Other, no runner yet is Unknown. crsp's own name wins over the group, so a runner
// crsp made is never marked as run elsewhere.
func NewClassifier(r Runners) Classify { return classifier{runners: r} }

func (c classifier) Origin(ctx context.Context, runnerName, runnerGroup string) (Origin, error) {
	if runnerName != "" && c.runners != nil {
		ok, err := c.runners.IsCrsp(ctx, runnerName)
		if err != nil {
			return Unknown, err
		}
		if ok {
			return Crsp, nil
		}
	}
	switch {
	case runnerGroup == HostedGroup:
		return Hosted, nil
	case runnerName != "":
		return Other, nil
	}
	return Unknown, nil
}
