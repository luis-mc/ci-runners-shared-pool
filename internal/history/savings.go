package history

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// Job is one finished job that ran on the pool.
type Job struct {
	Repository string
	// Public is true for a public repository: GitHub-hosted standard runners are free there (R5.18).
	Public  bool
	OS      string
	Machine string
	Run     time.Duration
}

// Source lists the jobs that finished in [from, to) on the pool.
type Source interface {
	FinishedPoolJobs(ctx context.Context, from, to time.Time) ([]Job, error)
}

// Range names of a report (R5.19).
const (
	RangeToday  = "today"
	Range7Days  = "7d"
	Range30Days = "30d"
	RangeCustom = "custom"
)

// Row is one line of a report.
type Row struct {
	Key     string  `json:"key"`
	Jobs    int     `json:"jobs"`
	Minutes int     `json:"minutes"`
	Money   float64 `json:"money"`
	// PublicMinutes are the minutes of public repositories: counted, but they save no money.
	PublicMinutes int `json:"public_minutes"`
}

// Report is minutes and money saved over a range.
type Report struct {
	Range        string    `json:"range"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	Total        Row       `json:"total"`
	ByRepository []Row     `json:"by_repository"`
	ByOS         []Row     `json:"by_os"`
	ByMachine    []Row     `json:"by_machine"`
	// NotPriced is how many jobs had no rate for their system, so they added no money.
	NotPriced int `json:"not_priced"`
	// Note says in words why money may be lower than minutes times a rate (R5.18).
	Note      string `json:"note"`
	SourceURL string `json:"source_url"`
	CopiedOn  string `json:"copied_on"`
	Changed   bool   `json:"rates_changed"`
}

// Service builds reports.
type Service struct {
	Source Source
	Rates  interface {
		Get(ctx context.Context) (Table, error)
	}
	Now func() time.Time
	Loc *time.Location
}

// RoundUpMinutes is how GitHub bills a hosted job: the next whole minute (R5.15).
func RoundUpMinutes(run time.Duration) int {
	if run <= 0 {
		return 0
	}
	return int((run + time.Minute - 1) / time.Minute)
}

// Window turns a range name into [from, to). A custom range needs both ends and is at most 366 days.
func (s *Service) Window(name string, from, to time.Time) (time.Time, time.Time, error) {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	loc := s.Loc
	if loc == nil {
		loc = time.Local
	}
	start := StartOfDay(now, loc)
	switch name {
	case RangeToday, "":
		return start, now, nil
	case Range7Days:
		return start.AddDate(0, 0, -6), now, nil
	case Range30Days:
		return start.AddDate(0, 0, -29), now, nil
	case RangeCustom:
		if from.IsZero() || to.IsZero() {
			return from, to, fmt.Errorf("a custom range needs a start day and an end day")
		}
		if to.Before(from) {
			return from, to, fmt.Errorf("the end day is before the start day")
		}
		if to.Sub(from) > 366*24*time.Hour {
			return from, to, fmt.Errorf("a range is at most 366 days")
		}
		return StartOfDay(from, loc), StartOfDay(to, loc).AddDate(0, 0, 1), nil
	}
	return from, to, fmt.Errorf("the range must be today, 7d, 30d or custom")
}

// StartOfDay is midnight of t's day in loc.
func StartOfDay(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// Report counts every job of the window: minutes rounded up per job (R5.15), money at the rate of the
// job's system and machine size (R5.16), none for a public repository (R5.18).
func (s *Service) Report(ctx context.Context, name string, from, to time.Time) (Report, error) {
	f, t, err := s.Window(name, from, to)
	if err != nil {
		return Report{}, err
	}
	tab, err := s.Rates.Get(ctx)
	if err != nil {
		return Report{}, err
	}
	jobs, err := s.Source.FinishedPoolJobs(ctx, f, t)
	if err != nil {
		return Report{}, fmt.Errorf("savings jobs: %w", err)
	}
	if name == "" {
		name = RangeToday
	}
	rep := Report{Range: name, From: f, To: t, SourceURL: tab.SourceURL, CopiedOn: tab.CopiedOn, Changed: tab.Changed,
		Note: "Estimate. Jobs of public repositories count their minutes but save no money: GitHub-hosted standard runners are free there."}
	repo, osr, mach := map[string]*Row{}, map[string]*Row{}, map[string]*Row{}
	add := func(m map[string]*Row, key string, minutes int, money float64, public bool) {
		if key == "" {
			key = "unknown"
		}
		r := m[key]
		if r == nil {
			r = &Row{Key: key}
			m[key] = r
		}
		r.Jobs++
		r.Minutes += minutes
		r.Money += money
		if public {
			r.PublicMinutes += minutes
		}
	}
	for _, j := range jobs {
		min := RoundUpMinutes(j.Run)
		if min == 0 {
			continue
		}
		money := 0.0
		if !j.Public {
			if rate, ok := tab.PerMinute(strings.ToLower(j.OS), j.Machine); ok {
				money = float64(min) * rate
			} else {
				rep.NotPriced++
			}
		}
		add(repo, j.Repository, min, money, j.Public)
		add(osr, strings.ToLower(j.OS), min, money, j.Public)
		add(mach, j.Machine, min, money, j.Public)
		rep.Total.Jobs++
		rep.Total.Minutes += min
		rep.Total.Money = roundCents(rep.Total.Money + money)
		if j.Public {
			rep.Total.PublicMinutes += min
		}
	}
	rep.Total.Key = "all"
	rep.ByRepository, rep.ByOS, rep.ByMachine = rows(repo), rows(osr), rows(mach)
	return rep, nil
}

func rows(m map[string]*Row) []Row {
	out := make([]Row, 0, len(m))
	for _, r := range m {
		r.Money = roundCents(r.Money)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Minutes != out[j].Minutes {
			return out[i].Minutes > out[j].Minutes
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func roundCents(v float64) float64 { return float64(int64(v*10000+0.5)) / 10000 }

// DBSource reads the finished jobs of the pool from the store.
type DBSource struct {
	DB interface {
		FinishedPoolJobs(ctx context.Context, from, to time.Time) ([]store.PoolJobRow, error)
	}
}

// FinishedPoolJobs implements Source.
func (d DBSource) FinishedPoolJobs(ctx context.Context, from, to time.Time) ([]Job, error) {
	rows, err := d.DB.FinishedPoolJobs(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, Job{Repository: r.Repository, Public: r.Public, OS: r.OS, Machine: r.Machine, Run: r.Run})
	}
	return out, nil
}
