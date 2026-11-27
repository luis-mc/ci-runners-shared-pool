package runners

import "fmt"

// Queue is the queue a runner serves (runners R5.70).
type Queue string

// The queues. A runner serves exactly one.
const (
	QueueCI Queue = "ci" // the job slots
)

// Route says which pool and queue a runner serves. It is stored on the record
// and is the only thing routing reads; the runner name is for people (runners
// R5.70, handover lesson 3). A name may change or repeat without moving a
// runner to another queue.
type Route struct {
	// Pool is the pool name the runner serves; empty means the default pool.
	Pool  string
	Queue Queue
}

// Normal gives the route with an empty queue read as the CI queue.
func (r Route) Normal() Route {
	if r.Queue == "" {
		r.Queue = QueueCI
	}
	return r
}

// Validate refuses a queue that does not exist.
func (r Route) Validate() error {
	switch r.Normal().Queue {
	case QueueCI:
		return nil
	}
	return fmt.Errorf("route queue %q is not ci", r.Queue)
}

// RouteForLabels is the route a new runner gets: the CI queue. It reads no
// label and never the name.
func RouteForLabels(pool string, _ []string) Route { return Route{Pool: pool, Queue: QueueCI} }

// RouteOf is the route of a record, with an empty queue read as CI.
func RouteOf(r Record) Route { return r.Route.Normal() }
