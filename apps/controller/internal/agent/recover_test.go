package agent

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
)

// operations R5.44: the controller's "too old" refusal is told apart from a
// removed machine and from a plain link loss.
func TestClassifyTooOldIsNeedsRecovery(t *testing.T) {
	if err := classify(connect.NewError(connect.CodeFailedPrecondition, errors.New("old"))); !errors.Is(err, ErrNeedsRecovery) {
		t.Fatalf("classify = %v", err)
	}
	if err := classify(connect.NewError(connect.CodeUnauthenticated, errors.New("gone"))); !errors.Is(err, ErrRemoved) {
		t.Fatalf("classify removed = %v", err)
	}
}
