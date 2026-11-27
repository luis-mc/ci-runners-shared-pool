package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

func cmdJob() *command {
	return &command{
		name: "job", summary: "queue and history",
		subs: []*command{
			{name: "list", summary: "list the queue and history", args: "[--repo OWNER/NAME] [--machine M] [--backend B] [--result R] [--since 24h] [--state S] [--limit N]",
				flags: func(c *call, fs *flag.FlagSet) {
					jobFilterFlags(fs)
					fs.String("state", "", "queued, running or done")
				},
				run: func(c *call, _ []string) error { return c.jobList() }},
			{name: "watch", summary: "follow jobs live until Ctrl+C", args: "[--repo OWNER/NAME]",
				flags: func(c *call, fs *flag.FlagSet) { fs.String("repo", "", "only this repository") },
				run:   func(c *call, _ []string) error { return c.jobWatch() }},
		},
	}
}

// jobFilterFlags registers the filters job list and history share (observability R5.11).
func jobFilterFlags(fs *flag.FlagSet) {
	fs.String("repo", "", "only this repository")
	fs.String("machine", "", "only this machine")
	fs.String("backend", "", "only this backend, for example docker")
	fs.String("result", "", "success, failure, cancelled, timed_out or lost")
	fs.Duration("since", 0, "only jobs newer than this, for example 24h")
	fs.Int("limit", 50, "most rows")
}

func (c *call) jobList() error { return c.listJobs("") }

// listJobs lists the rows of the listJobs route. A non-empty state is fixed (history passes done); otherwise the
// --state flag decides.
func (c *call) listJobs(state string) error {
	s, err := c.open()
	if err != nil {
		return err
	}
	f := func(n string) string {
		if n == "state" && state != "" {
			return state
		}
		if fl := c.fs.Lookup(n); fl != nil {
			return fl.Value.String()
		}
		return ""
	}
	limit := mustInt(f("limit"))
	if limit < 1 || limit > 500 {
		return fail(ExitUsage, "--limit must be between 1 and 500.")
	}
	var p client.ListJobsParams
	if v := f("repo"); v != "" {
		r, err := c.findRepo(s, v)
		if err != nil {
			return err
		}
		p.RepositoryId = &r.Id
	}
	if v := f("machine"); v != "" {
		m, err := c.findMachine(s, v)
		if err != nil {
			return err
		}
		p.MachineId = &m.Id
	}
	if v := f("backend"); v != "" {
		p.Backend = &v
	}
	if v := f("result"); v != "" {
		r := client.JobResult(v)
		p.Result = &r
	}
	if v := f("state"); v != "" {
		st := client.ListJobsParamsState(v)
		p.State = &st
	}
	if d, _ := time.ParseDuration(f("since")); d > 0 {
		t := c.env.Now().Add(-d)
		p.From = &t
	}
	var jobs []client.Job
	for len(jobs) < limit {
		lim := min(limit-len(jobs), 200)
		p.Limit = &lim
		r, err := s.client.ListJobsWithResponse(c.env.Context, &p)
		if err != nil {
			return s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return err
		}
		if r.JSON200 == nil {
			break
		}
		jobs = append(jobs, r.JSON200.Items...)
		if r.JSON200.NextCursor == nil || *r.JSON200.NextCursor == "" {
			break
		}
		p.Cursor = r.JSON200.NextCursor
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, client.JobPage{Items: nonNil(jobs)})
	}
	if len(jobs) == 0 {
		_, _ = fmt.Fprintln(c.env.Stderr, "No jobs match.")
		return nil
	}
	var rows [][]string
	for _, j := range jobs {
		res := string(j.State)
		if j.Result != nil {
			res = string(*j.Result)
		}
		rows = append(rows, []string{j.Repository, j.JobName, string(j.State), res, output.Time(j.QueuedAt)})
	}
	output.Table(c.env.Stdout, output.MaxWidth, []string{"REPOSITORY", "JOB", "STATE", "RESULT", "QUEUED"}, rows)
	return nil
}

// jobWatch streams job events and reconnects after a dropped stream (R5.20).
func (c *call) jobWatch() error {
	s, err := c.open()
	if err != nil {
		return err
	}
	repoFilter := c.fs.Lookup("repo").Value.String()
	lastID := ""
	for {
		err := c.watchOnce(s, &lastID, repoFilter)
		if c.env.Context.Err() != nil {
			return nil // Ctrl+C is the normal way out.
		}
		var ee *exitError
		if asExit(err, &ee) && ee.Code != ExitUnreachable {
			return err
		}
		_, _ = fmt.Fprintln(c.env.Stderr, "Reconnecting...")
		if err := c.sleep(2 * time.Second); err != nil {
			return nil
		}
	}
}

func (c *call) watchOnce(s *session, lastID *string, repo string) error {
	var p client.StreamEventsParams
	if *lastID != "" {
		p.LastEventID = lastID
	}
	resp, err := s.raw.StreamEvents(c.env.Context, &p)
	if err != nil {
		return s.transport(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		var buf [4096]byte
		n, _ := resp.Body.Read(buf[:])
		return apiError(resp.StatusCode, buf[:n])
	}
	return c.readEvents(resp, lastID, repo)
}

func (c *call) readEvents(resp *http.Response, lastID *string, repo string) error {
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "id:"); ok {
			*lastID = strings.TrimSpace(v)
			continue
		}
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		var ev liveEvent
		if json.Unmarshal([]byte(strings.TrimSpace(data)), &ev) != nil {
			continue
		}
		if ev.ID != "" {
			*lastID = ev.ID
		}
		if !strings.HasPrefix(ev.Type, "job.") || ev.Job == nil {
			continue
		}
		if repo != "" && !strings.EqualFold(ev.Job.Repository, repo) {
			continue
		}
		if c.g.json {
			_, _ = fmt.Fprintln(c.env.Stdout, strings.TrimSpace(data))
			continue
		}
		where := ""
		if ev.Job.RunnerName != nil {
			where = " on " + *ev.Job.RunnerName
		}
		_, _ = fmt.Fprintf(c.env.Stdout, "%s  %-12s %s %s%s\n", ev.At.UTC().Format("15:04:05"), ev.Type, ev.Job.Repository, ev.Job.JobName, where)
	}
	return sc.Err()
}

// liveEvent is the part of the contract's LiveEvent that job watch reads. The
// generated client leaves the schema out because no response names it.
type liveEvent struct {
	ID   string      `json:"id"`
	Type string      `json:"type"`
	At   time.Time   `json:"at"`
	Job  *client.Job `json:"job,omitempty"`
}
