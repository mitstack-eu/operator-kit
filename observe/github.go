package observe

import (
	"context"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

type GitHubRepositoryAPI interface {
	RepositoryExists(ctx context.Context, d contract.Desired, fullName string) (bool, error)
}

// GitHubRepository observes a github.repository by its owner/name repo,
// reporting it as repo.
type GitHubRepository struct {
	API GitHubRepositoryAPI
}

func (o GitHubRepository) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "repo")
	if !ok {
		return obs, nil
	}
	exists, err := o.API.RepositoryExists(ctx, r.Desired, ids[0])
	return existence(exists, err, map[string]string{"repo": ids[0]})
}

type GitHubCommitAPI interface {
	CommitExists(ctx context.Context, d contract.Desired, repo, sha string) (bool, error)
}

// GitHubCommit observes a github.commit by its repo and sha, reporting
// sourceSha and, when the identity holds a branch, sourceBranch.
type GitHubCommit struct {
	API GitHubCommitAPI
}

func (o GitHubCommit) ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error) {
	ids, obs, ok := identityKeys(r, "repo", "sha")
	if !ok {
		return obs, nil
	}
	outputs := map[string]string{"sourceSha": ids[1]}
	if branch := r.Identity["branch"]; branch != "" {
		outputs["sourceBranch"] = branch
	}
	exists, err := o.API.CommitExists(ctx, r.Desired, ids[0], ids[1])
	return existence(exists, err, outputs)
}
