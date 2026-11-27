package events

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// KeepAlive is the gap between comment lines that hold a stream open (R6.3).
const KeepAlive = 15 * time.Second

// field is the LiveEvent field that carries the data of each type.
func field(t Type) string {
	switch t {
	case Snapshot:
		return "snapshot"
	case JobQueued, JobWaiting, JobStarted, JobFinished:
		return "job"
	case MachineLoad:
		return "load"
	case MachineState:
		return "machine"
	case RunnerState:
		return "runner"
	case SavedToday:
		return "minutes_saved_today"
	case DoctorProgress:
		return "doctor_run"
	case PlanProgress:
		return "plan"
	case ChangeProgress:
		return "change"
	case ImageBuild:
		return "image_build"
	}
	return "data"
}

// LiveEventJSON renders an event as the LiveEvent schema of the contract.
func LiveEventJSON(e Event) []byte {
	var b bytes.Buffer
	b.WriteString(`{"id":`)
	id, _ := json.Marshal(strconv.FormatUint(e.ID, 10))
	b.Write(id)
	b.WriteString(`,"type":`)
	typ, _ := json.Marshal(string(e.Type))
	b.Write(typ)
	b.WriteString(`,"at":`)
	at, _ := json.Marshal(e.At.UTC().Format(time.RFC3339Nano))
	b.Write(at)
	if e.ReplayGap {
		b.WriteString(`,"replay_gap":true`)
	}
	if len(e.Data) > 0 {
		b.WriteString(`,"` + field(e.Type) + `":`)
		b.Write(e.Data)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// WriteSSE writes one event in the text/event-stream format.
func WriteSSE(w io.Writer, e Event) error {
	_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, LiveEventJSON(e))
	return err
}

// ParseLastEventID reads the Last-Event-ID header; empty or bad gives 0, which
// sends a snapshot.
func ParseLastEventID(v string) uint64 {
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// Serve writes the subscription to w until the client leaves, the stream is
// closed or an error happens. keepAlive is the comment-line gap (R6.3); zero
// means KeepAlive. It flushes after every event.
func Serve(w http.ResponseWriter, r *http.Request, s Subscription, keepAlive time.Duration) {
	defer s.Close()
	if keepAlive <= 0 {
		keepAlive = KeepAlive
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	flush := func() {
		if fl != nil {
			fl.Flush()
		}
	}
	flush()
	tick := time.NewTicker(keepAlive)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-s.Events():
			if !ok {
				return
			}
			if WriteSSE(w, e) != nil {
				return
			}
			flush()
		case <-tick.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flush()
		}
	}
}
