package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// Rendered is one resource's Argo CD app, its files by repository path and its outputs, none of them a secret.
type Rendered struct {
	App     string
	Files   map[string][]byte
	Outputs map[string]string
}

// Resource is the handler.Observer of one declared resource whose files a block-level handler commits through Deploy, and every field is required.
type Resource struct {
	Repository func(ctx context.Context, d contract.Desired) (Repository, error)

	// Notifications usually returns NotificationsFromContext.
	Notifications func(ctx context.Context, d contract.Desired) (Notifications, error)

	Render func(d contract.Desired) (Rendered, error)
}

// ObserveResource judges the app's newest notification, whatever its revision, once the files at that revision stand as they do at head, since Argo CD may sync a later revision than the one committed.
// A block reads only the notifications of the cluster its kubernetes-cluster requirement binds, so binding none or several stays unknown.
func (r *Resource) ObserveResource(ctx context.Context, ref contract.ResourceRef) (contract.Observation, error) {
	d := ref.Desired
	out, err := r.render(d)
	if err != nil {
		return contract.Observation{}, err
	}
	repo, err := r.repository(ctx, d)
	if err != nil {
		return contract.Observation{}, err
	}
	head, err := repo.Head(ctx)
	if err != nil {
		return contract.Observation{}, fmt.Errorf("gitops: reading head: %w", err)
	}
	if head == "" {
		return contract.Observation{State: contract.Absent}, nil
	}
	atHead, err := compareAt(ctx, repo, head, out.Files)
	if err != nil {
		return contract.Observation{}, err
	}
	var want contract.State
	switch {
	case atHead.all():
		want = contract.Present
	case atHead.none():
		want = contract.Absent
	default:
		return unknown("head %s holds the rendered manifests only in part: %s", head, atHead.describe()), nil
	}

	src, err := r.Notifications(ctx, d)
	if err != nil {
		return contract.Observation{}, fmt.Errorf("gitops: reaching the notifications for %s: %w", out.App, err)
	}
	n, ok, err := src.Latest(ctx, out.App, "")
	if err != nil {
		return contract.Observation{}, fmt.Errorf("gitops: reading the newest notification for %s: %w", out.App, err)
	}
	if !ok {
		return unknown("Argo CD has reported no deploy of %s yet", out.App), nil
	}
	atNotified, err := compareAt(ctx, repo, n.Revision, out.Files)
	if err != nil {
		return contract.Observation{}, err
	}
	if want == contract.Present && !atNotified.all() {
		return unknown("Argo CD last reported %s at %s, which does not yet carry the change at head %s", out.App, n.Revision, head), nil
	}
	if want == contract.Absent && !atNotified.none() {
		return unknown("Argo CD last reported %s at %s, which does not yet carry the removal at head %s", out.App, n.Revision, head), nil
	}

	switch Judge(n, n.Revision) {
	case VerdictSucceeded:
		if want == contract.Present {
			return contract.Observation{State: contract.Present, Outputs: out.Outputs}, nil
		}
		return contract.Observation{State: contract.Absent}, nil
	case VerdictFailed:
		return contract.Observation{}, fmt.Errorf("gitops: Argo CD reports the deploy of %s at %s failed: phase %s, health %s: %s",
			out.App, n.Revision, n.Phase, n.Health, n.Message)
	default:
		return unknown("Argo CD reports %s at %s with phase %q and health %q", out.App, n.Revision, n.Phase, n.Health), nil
	}
}

func (r *Resource) render(d contract.Desired) (Rendered, error) {
	out, err := r.Render(d)
	if err != nil {
		return Rendered{}, fmt.Errorf("gitops: rendering: %w", err)
	}
	if out.App == "" {
		return Rendered{}, errors.New("gitops: the render names no app")
	}
	if len(out.Files) == 0 {
		return Rendered{}, fmt.Errorf("gitops: the render for %s holds no file", out.App)
	}
	for _, path := range sortedPaths(out.Files) {
		if out.Files[path] == nil {
			return Rendered{}, fmt.Errorf("gitops: the render for %s gives %s no content", out.App, path)
		}
	}
	return out, nil
}

func (r *Resource) repository(ctx context.Context, d contract.Desired) (Repository, error) {
	repo, err := r.Repository(ctx, d)
	if err != nil {
		return nil, fmt.Errorf("gitops: reaching the repository: %w", err)
	}
	return repo, nil
}

type standing struct {
	total   int
	missing []string
	differ  []string
}

func (s standing) all() bool { return len(s.missing) == 0 && len(s.differ) == 0 }

func (s standing) none() bool { return len(s.missing) == s.total }

func (s standing) describe() string {
	var parts []string
	for _, path := range s.missing {
		parts = append(parts, path+" is missing")
	}
	for _, path := range s.differ {
		parts = append(parts, path+" differs")
	}
	return strings.Join(parts, ", ")
}

func compareAt(ctx context.Context, repo Repository, revision string, files map[string][]byte) (standing, error) {
	s := standing{total: len(files)}
	for _, path := range sortedPaths(files) {
		got, ok, err := readAt(ctx, repo, revision, path)
		if err != nil {
			return standing{}, err
		}
		switch {
		case !ok:
			s.missing = append(s.missing, path)
		case !bytes.Equal(got, files[path]):
			s.differ = append(s.differ, path)
		}
	}
	return s, nil
}

func unknown(format string, args ...any) contract.Observation {
	return contract.Observation{State: contract.Unknown, StatusDetail: fmt.Sprintf(format, args...)}
}
