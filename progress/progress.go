// Package progress, an optional helper, narrates a long provision on the advisory event channel, swallowing send failures so narration never changes a lifecycle outcome.
package progress

import (
	"context"
	"fmt"
	"sync"

	"github.com/mitstack-eu/operator-kit/v2/events"
	"github.com/mitstack-eu/operator-kit/v2/safe"
)

type Event struct {
	Action   string
	Resource string
	Detail   string
}

type Narrator struct {
	mu       sync.Mutex
	emit     func(ctx context.Context, detail string)
	secrets  []string
	done     int
	total    int
	lastSent string
}

// New returns a Narrator that pushes to the event channel carried on ctx, scrubbing secrets from every line.
func New(secrets ...string) *Narrator {
	return &Narrator{
		emit: func(ctx context.Context, detail string) {
			// The error is dropped deliberately; narration is advisory.
			_ = events.EmitProgress(ctx, detail)
		},
		secrets: secrets,
	}
}

func NewWithSink(sink func(detail string), secrets ...string) *Narrator {
	return &Narrator{
		emit:    func(_ context.Context, detail string) { sink(detail) },
		secrets: secrets,
	}
}

// SetTotal declares how many resources are expected so lines carry "n/m", and an unset total leaves the count off.
func (n *Narrator) SetTotal(total int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.total = total
}

// Observe narrates one event and never returns an error, so narration cannot become load-bearing.
func (n *Narrator) Observe(ctx context.Context, ev Event) {
	line := n.render(ev)
	if line == "" {
		return
	}
	n.emit(ctx, line)
}

// Secret scrubbing and log sanitising are independent steps that compose and are never substituted for one another.
func (n *Narrator) render(ev Event) string {
	if ev.Action == "" && ev.Resource == "" {
		return ""
	}

	n.mu.Lock()
	if isTerminal(ev.Action) {
		n.done++
	}
	suffix := ""
	if n.total > 0 {
		suffix = fmt.Sprintf(" (%d/%d)", n.done, n.total)
	}
	n.mu.Unlock()

	line := ev.Action
	if ev.Resource != "" {
		line += " " + ev.Resource
	}
	if ev.Detail != "" {
		line += ": " + ev.Detail
	}
	line += suffix

	return safe.LogSafe(line, n.secrets...)
}

func isTerminal(action string) bool {
	switch action {
	case "created", "destroyed", "modified", "imported":
		return true
	}
	return false
}

func (n *Narrator) Done() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.done
}
