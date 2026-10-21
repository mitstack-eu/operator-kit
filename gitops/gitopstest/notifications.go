package gitopstest

import (
	"context"
	"sync"

	"github.com/mitstack-eu/operator-kit/v3/gitops"
)

type appRevision struct{ app, revision string }

type Notifications struct {
	mu        sync.Mutex
	sequences map[appRevision][]gitops.Notification
	newest    map[string]gitops.Notification
	succeed   map[string]*Repository
	reads     map[appRevision]int
	err       error
}

func NewNotifications() *Notifications {
	return &Notifications{
		sequences: map[appRevision][]gitops.Notification{},
		newest:    map[string]gitops.Notification{},
		succeed:   map[string]*Repository{},
		reads:     map[appRevision]int{},
	}
}

// Set makes n the answer for n.App at n.Revision and the app's newest
// notification, which is what revision "" answers.
func (s *Notifications) Set(n gitops.Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequences[appRevision{n.App, n.Revision}] = []gitops.Notification{n}
	s.newest[n.App] = n
}

// Sequence makes the reads of app at revision answer each of notes in turn
// and then repeat the last. A zero Notification answers none.
func (s *Notifications) Sequence(app, revision string, notes ...gitops.Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequences[appRevision{app, revision}] = append([]gitops.Notification(nil), notes...)
}

// SucceedCommits makes app answer a healthy success for every revision repo
// holds, and revision "" at its head, unless Set or Sequence seeded one.
func (s *Notifications) SucceedCommits(app string, repo *Repository) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.succeed[app] = repo
}

func (s *Notifications) FailWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *Notifications) Reads(app, revision string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads[appRevision{app, revision}]
}

func (s *Notifications) Latest(ctx context.Context, app, revision string) (gitops.Notification, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := appRevision{app, revision}
	s.reads[key]++
	if s.err != nil {
		return gitops.Notification{}, false, s.err
	}
	if revision == "" {
		return s.newestFor(ctx, app)
	}
	if seq := s.sequences[key]; len(seq) > 0 {
		n := seq[0]
		if len(seq) > 1 {
			s.sequences[key] = seq[1:]
		}
		return n, n != (gitops.Notification{}), nil
	}
	if repo, ok := s.succeed[app]; ok && repo.holds(revision) {
		return succeeded(app, revision), true, nil
	}
	return gitops.Notification{}, false, nil
}

// The caller holds mu.
func (s *Notifications) newestFor(ctx context.Context, app string) (gitops.Notification, bool, error) {
	if n, ok := s.newest[app]; ok {
		return n, true, nil
	}
	repo, ok := s.succeed[app]
	if !ok {
		return gitops.Notification{}, false, nil
	}
	head, err := repo.Head(ctx)
	if err != nil || head == "" {
		return gitops.Notification{}, false, err
	}
	return succeeded(app, head), true, nil
}

func succeeded(app, revision string) gitops.Notification {
	return gitops.Notification{
		App: app, Project: app, Sync: "Synced", Revision: revision,
		Health: "Healthy", Phase: "Succeeded", Message: "successfully synced (all tasks run)",
	}
}
