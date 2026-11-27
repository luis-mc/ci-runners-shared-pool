package runners

import "context"

// NotifyStore wraps a Store and tells OnChange after every change to a runner
// record (made, state, service, registration, removed), so the live view can
// tell the console to read the list again (web-console R5.6). gone is true
// when the record was removed; rec is then the last record the store held.
type NotifyStore struct {
	Store
	OnChange func(rec Record, gone bool)
}

func (n NotifyStore) changed(ctx context.Context, id string) {
	if n.OnChange == nil {
		return
	}
	if rec, err := n.GetRunner(ctx, id); err == nil {
		n.OnChange(rec, false)
	}
}

// InsertRunner implements Store.
func (n NotifyStore) InsertRunner(ctx context.Context, r Record) error {
	if err := n.Store.InsertRunner(ctx, r); err != nil {
		return err
	}
	n.changed(ctx, r.ID)
	return nil
}

// SetRunnerState implements Store.
func (n NotifyStore) SetRunnerState(ctx context.Context, id string, to State, detail string) error {
	if err := n.Store.SetRunnerState(ctx, id, to, detail); err != nil {
		return err
	}
	n.changed(ctx, id)
	return nil
}

// SetRunnerService implements Store.
func (n NotifyStore) SetRunnerService(ctx context.Context, id, serviceName, account string) error {
	if err := n.Store.SetRunnerService(ctx, id, serviceName, account); err != nil {
		return err
	}
	n.changed(ctx, id)
	return nil
}

// SetRunnerRegistration implements Store.
func (n NotifyStore) SetRunnerRegistration(ctx context.Context, id string, githubRunnerID int64, version string) error {
	if err := n.Store.SetRunnerRegistration(ctx, id, githubRunnerID, version); err != nil {
		return err
	}
	n.changed(ctx, id)
	return nil
}

// SetRunnerHygiene implements Store.
func (n NotifyStore) SetRunnerHygiene(ctx context.Context, id string, h Hygiene) error {
	if err := n.Store.SetRunnerHygiene(ctx, id, h); err != nil {
		return err
	}
	n.changed(ctx, id)
	return nil
}

// DeleteRunner implements Store.
func (n NotifyStore) DeleteRunner(ctx context.Context, id string) error {
	rec, getErr := n.GetRunner(ctx, id)
	if err := n.Store.DeleteRunner(ctx, id); err != nil {
		return err
	}
	if n.OnChange != nil && getErr == nil {
		n.OnChange(rec, true)
	}
	return nil
}
