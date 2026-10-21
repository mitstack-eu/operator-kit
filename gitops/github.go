package gitops

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/ghapi"
)

type GitHubRepository struct {
	Client *ghapi.Client
	Repo   string
}

// No error NewGitHubRepository returns carries the token.
func NewGitHubRepository(d contract.Desired, repoFullName string) (*GitHubRepository, error) {
	repo, err := ghapi.SafeRepo(repoFullName)
	if err != nil {
		return nil, err
	}
	instance, ok := d.Instances["github"]
	if !ok || instance.Endpoint == "" {
		return nil, errors.New("gitops: the request carries no github instance endpoint")
	}
	base, err := ghapi.BaseFromEnv(instance.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("gitops: resolving the github endpoint: %w", err)
	}
	token := d.Secrets["github"]["token"]
	if token == "" {
		return nil, errors.New("gitops: the request carries no github token")
	}
	return &GitHubRepository{Client: ghapi.NewClient(base.String(), token, nil), Repo: repo}, nil
}

// Head returns the commit main points at, or "" before main exists.
func (r *GitHubRepository) Head(ctx context.Context) (string, error) {
	return r.Client.HeadCommit(ctx, r.Repo)
}

// Revision "" holds nothing, and any other must be a full lowercase hex SHA.
func (r *GitHubRepository) ReadFile(ctx context.Context, revision, path string) ([]byte, bool, error) {
	if revision == "" {
		return nil, false, nil
	}
	content, ok, err := r.Client.ReadFileAt(ctx, r.Repo, revision, path)
	if err != nil || ok {
		return content, ok, err
	}
	if _, err := r.Client.CommitTree(ctx, r.Repo, revision); err != nil {
		return nil, false, fmt.Errorf("gitops: confirming revision %s: %w", revision, err)
	}
	return nil, false, nil
}

// A parent no longer the tip yields an error wrapping ErrStaleBase.
func (r *GitHubRepository) Commit(ctx context.Context, parent string, changes map[string][]byte, message string) (string, error) {
	entries := make(map[string]*string, len(changes))
	for path, content := range changes {
		if content == nil {
			entries[path] = nil
			continue
		}
		if !utf8.Valid(content) {
			return "", fmt.Errorf("gitops: %s is not UTF-8 text", path)
		}
		s := string(content)
		entries[path] = &s
	}
	rev, err := r.Client.CommitChanges(ctx, r.Repo, parent, message, entries)
	if errors.Is(err, ghapi.ErrNotFastForward) {
		return "", fmt.Errorf("%w: %w", ErrStaleBase, err)
	}
	return rev, err
}

// Revision must be a full lowercase hex SHA.
func (r *GitHubRepository) Changed(ctx context.Context, revision string) (string, []string, error) {
	return r.Client.CommitParentAndPaths(ctx, r.Repo, revision)
}
