package compare

import (
	"fmt"
	"io"
	"text/tabwriter"
)

// WriteTable prints a report as a plain table.
func WriteTable(w io.Writer, r Report) error {
	if r.Excluded {
		_, err := fmt.Fprintf(w, "%s: %s.\n", r.Repository, r.Reason)
		return err
	}
	if len(r.Rows) == 0 {
		_, err := fmt.Fprintf(w, "%s: no job ran on both crsp and a hosted runner in the last %d days (%d jobs ran on one kind only).\n",
			r.Repository, r.Days, r.Unmatched)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "JOB\tRUNS crsp/hosted\tQUEUE med crsp\tQUEUE med hosted\tRUN med crsp\tRUN med hosted\tRUN p95 crsp\tRUN p95 hosted\tSAVED min\tCOST AVOIDED"); err != nil {
		return err
	}
	for _, x := range r.Rows {
		if _, err := fmt.Fprintf(tw, "%s\t%d/%d\t%s\t%s\t%s\t%s\t%s\t%s\t%.1f\t$%.2f\n", x.Job,
			x.Crsp.Run.Count, x.Hosted.Run.Count,
			dur(x.Crsp.Queue.Median), dur(x.Hosted.Queue.Median),
			dur(x.Crsp.Run.Median), dur(x.Hosted.Run.Median),
			dur(x.Crsp.Run.P95), dur(x.Hosted.Run.P95),
			x.MinutesSaved, x.CostAvoided); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%s, last %d days: %.1f minutes saved, $%.2f hosted cost avoided.\n",
		r.Repository, r.Days, r.TotalMinutesSaved, r.TotalCostAvoided)
	return err
}

func dur(sec float64) string {
	s := int(sec + 0.5)
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm%02ds", s/60, s%60)
}
