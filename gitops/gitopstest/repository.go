package gitopstest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/mitstack-eu/operator-kit/v2/gitops"
)

// Commit is one commit a Repository landed, with the changes it was handed,
// where a nil content deleted its path.
type Commit struct {
	Revision string
	Parent   string
	Message  string
	Changes  map[string][]byte
}

// Repository is an in-memory gitops.Repository, safe for concurrent use,
// that keeps every commit's tree so any revision it landed can be read.
type Repository struct {
	mu      sync.Mutex
	commits []Commit
	trees   map[string]map[string][]byte

	foreignLeft    int
	foreignChanges map[string][]byte
}

// NewRepository returns an empty Repository, whose head is "".
func NewRepository() *Repository {
	return &Repository{trees: map[string]map[string][]byte{}}
}

// Head returns the revision of the last commit landed, or "" when none has.
func (r *Repository) Head(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.head(), nil
}

// ReadFile returns a copy of path's content at revision and whether it
// exists there. An unknown revision is an error.
func (r *Repository) ReadFile(_ context.Context, revision, path string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if revision == "" {
		return nil, false, nil
	}
	tree, ok := r.trees[revision]
	if !ok {
		return nil, false, fmt.Errorf("gitopstest: no revision %s", revision)
	}
	content, ok := tree[path]
	if !ok {
		return nil, false, nil
	}
	return clone(content), true, nil
}

// Commit lands a copy of changes on parent as gitops.Repository requires,
// first landing another writer's commit while a MoveTip count remains.
func (r *Repository) Commit(_ context.Context, parent string, changes map[string][]byte, message string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.foreignLeft > 0 {
		r.foreignLeft--
		r.land(r.head(), r.foreignChanges, "another writer's commit")
	}
	if head := r.head(); parent != head {
		return "", fmt.Errorf("%w: parent %q, tip %q", gitops.ErrStaleBase, parent, head)
	}
	return r.land(parent, changes, message), nil
}

// Changed returns revision's parent and the sorted paths its commit changed.
func (r *Repository) Changed(_ context.Context, revision string) (string, []string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.commits {
		if c.Revision != revision {
			continue
		}
		paths := make([]string, 0, len(c.Changes))
		for path := range c.Changes {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		return c.Parent, paths, nil
	}
	return "", nil, fmt.Errorf("gitopstest: no revision %s", revision)
}

// Commits returns a copy of every commit landed, another writer's included.
func (r *Repository) Commits() []Commit {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Commit, len(r.commits))
	for i, c := range r.commits {
		c.Changes = cloneChanges(c.Changes)
		out[i] = c
	}
	return out
}

// MoveTip makes each of the next n calls to Commit find another writer's
// commit, carrying changes, landed on the tip just before it.
func (r *Repository) MoveTip(n int, changes map[string][]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.foreignLeft, r.foreignChanges = n, cloneChanges(changes)
}

func (r *Repository) holds(revision string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.trees[revision]
	return ok
}

// head returns the tip's revision. The caller holds mu.
func (r *Repository) head() string {
	if len(r.commits) == 0 {
		return ""
	}
	return r.commits[len(r.commits)-1].Revision
}

// land records a commit with a 40-hex-digit revision. The caller holds mu.
func (r *Repository) land(parent string, changes map[string][]byte, message string) string {
	tree := make(map[string][]byte, len(r.trees[parent])+len(changes))
	for path, content := range r.trees[parent] {
		tree[path] = content
	}
	for path, content := range changes {
		if content == nil {
			delete(tree, path)
			continue
		}
		tree[path] = clone(content)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", len(r.commits), parent, message)))
	revision := hex.EncodeToString(sum[:20])
	r.trees[revision] = tree
	r.commits = append(r.commits, Commit{Revision: revision, Parent: parent, Message: message, Changes: cloneChanges(changes)})
	return revision
}

func clone(content []byte) []byte {
	if content == nil {
		return nil
	}
	return append([]byte{}, content...)
}

func cloneChanges(changes map[string][]byte) map[string][]byte {
	if changes == nil {
		return nil
	}
	out := make(map[string][]byte, len(changes))
	for path, content := range changes {
		out[path] = clone(content)
	}
	return out
}
