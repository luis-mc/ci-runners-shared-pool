package runners

import "context"

// ClassStore keeps the class table and the reservation numbers (scheduling
// R5.89, R5.93). *store.Store implements it.
type ClassStore interface {
	ClassSizes(ctx context.Context) ClassSizes
	SaveClassSizes(ctx context.Context, c ClassSizes, by string) error
	ReserveLimits(ctx context.Context) (passes, minutes int)
	SaveReserveLimits(ctx context.Context, passes, minutes int, by string) error
}

// ClassRunner is a runner as the class settings see it: its class and the pool
// of the machine it lives on.
type ClassRunner struct {
	Name  string
	Class SizeClass
	Pool  PoolRoom
}

// ClassFacts lists the runners and the pools the saved table must still fit.
type ClassFacts interface {
	ClassRunners(ctx context.Context) ([]ClassRunner, error)
	Pools(ctx context.Context) ([]PoolRoom, error)
}

// ClassService reads and saves the class settings (scheduling R5.89 to R5.93).
type ClassService struct {
	Store ClassStore
	Facts ClassFacts
}

// ClassView is the settings as the API shows them.
type ClassView struct {
	Preset         string
	Presets        []string
	Sizes          ClassSizes
	Hints          []string
	Unheld         []SizeClass // classes no pool holds: a note, not an error
	ReservePasses  int
	ReserveMinutes int
}

// ClassInput is a save. A non-empty Preset other than custom sets the table to
// that preset; Sizes sets it by hand. Nil reservation numbers are kept.
type ClassInput struct {
	Preset          string
	Sizes           *ClassSizes
	ReservePasses   *int
	ReserveMinutes  *int
	ConfirmLowering bool
}

func (s *ClassService) view(ctx context.Context, c ClassSizes) ClassView {
	passes, minutes := s.Store.ReserveLimits(ctx)
	v := ClassView{Preset: c.Preset(), Presets: PresetNames(), Sizes: c, Hints: c.Hints(), ReservePasses: passes, ReserveMinutes: minutes}
	if s.Facts != nil {
		if pools, err := s.Facts.Pools(ctx); err == nil && len(pools) > 0 {
			v.Unheld = c.UnheldSizes(pools)
		}
	}
	return v
}

// Get gives the saved settings.
func (s *ClassService) Get(ctx context.Context) ClassView {
	return s.view(ctx, s.Store.ClassSizes(ctx))
}

// Save checks and saves. A refusal is an *ErrClassTable (the owner's words) or
// ErrConfirmLowering, and nothing is saved.
func (s *ClassService) Save(ctx context.Context, in ClassInput, by string) (ClassView, error) {
	prev := s.Store.ClassSizes(ctx)
	next := prev
	switch {
	case in.Sizes != nil:
		next = *in.Sizes
	case in.Preset != "" && in.Preset != PresetCustom:
		p, ok := Preset(in.Preset)
		if !ok {
			return ClassView{}, &ErrClassTable{"Preset is not one of standard, compact, memory-heavy, custom."}
		}
		next = p
	}
	for _, n := range []*int{in.ReservePasses, in.ReserveMinutes} {
		if n != nil && *n < 1 {
			return ClassView{}, &ErrClassTable{MsgWholeNumbers}
		}
	}
	rs := map[string]SizeClass{}
	pools := map[string]PoolRoom{}
	inUse := map[SizeClass]int{}
	if s.Facts != nil {
		list, err := s.Facts.ClassRunners(ctx)
		if err != nil {
			return ClassView{}, err
		}
		for _, r := range list {
			rs[r.Name], pools[r.Name] = r.Class, r.Pool
			inUse[r.Class]++
		}
	}
	if err := CheckSave(prev, next, rs, pools, inUse, in.ConfirmLowering); err != nil {
		return ClassView{}, err
	}
	if next != prev {
		if err := s.Store.SaveClassSizes(ctx, next, by); err != nil {
			return ClassView{}, err
		}
	}
	if in.ReservePasses != nil || in.ReserveMinutes != nil {
		passes, minutes := s.Store.ReserveLimits(ctx)
		if in.ReservePasses != nil {
			passes = *in.ReservePasses
		}
		if in.ReserveMinutes != nil {
			minutes = *in.ReserveMinutes
		}
		if err := s.Store.SaveReserveLimits(ctx, passes, minutes, by); err != nil {
			return ClassView{}, err
		}
	}
	return s.view(ctx, next), nil
}

// ClassOf gives the class name of a runner on a machine, "" when it is not
// known (the gate reads it, scheduling R5.90).
func (s *Service) ClassOf(ctx context.Context, machineID, name string) string {
	r, err := s.findByName(ctx, machineID, name)
	if err != nil {
		return ""
	}
	return string(SizeOf(r))
}
