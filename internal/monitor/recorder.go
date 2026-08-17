package monitor

import (
	"sync"
	"time"
)

const defaultEventLimit = 100

// Recorder keeps a bounded, in-memory history of semantic runtime events.
// It is an observability aid only; task correctness never depends on it.
type Recorder struct {
	mu     sync.RWMutex
	limit  int
	events []SystemEvent
}

func NewRecorder(limit int) *Recorder {
	if limit <= 0 {
		limit = defaultEventLimit
	}
	return &Recorder{limit: limit}
}

func (r *Recorder) Record(event SystemEvent) {
	if r == nil {
		return
	}
	if event.Time.IsZero() {
		event.Time = time.Now()
	}
	if event.Fields != nil {
		fields := make(map[string]string, len(event.Fields))
		for key, value := range event.Fields {
			fields[key] = value
		}
		event.Fields = fields
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == r.limit {
		copy(r.events, r.events[1:])
		r.events[len(r.events)-1] = event
		return
	}
	r.events = append(r.events, event)
}

func (r *Recorder) Snapshot() []SystemEvent {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	events := make([]SystemEvent, len(r.events))
	for i, event := range r.events {
		events[i] = event
		if event.Fields != nil {
			events[i].Fields = make(map[string]string, len(event.Fields))
			for key, value := range event.Fields {
				events[i].Fields[key] = value
			}
		}
	}
	return events
}
