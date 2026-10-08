package gitops

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"

	"github.com/mitstack-eu/operator-kit/contract"
)

// ErrStaleBase is wrapped by a Repository's Commit when parent is no longer
// the tip.
var ErrStaleBase = errors.New("gitops: the parent is no longer the tip")

// ErrSecretInManifest is wrapped by CommitManifests, naming the path but
// never the value, when a file carries a secret.
var ErrSecretInManifest = errors.New("gitops: a manifest carries a secret value")

const maxCommitTries = 5

// Repository is a GitOps repository read and written by revision, a commit
// id, where "" names the empty repository.
type Repository interface {
	// Head returns the tip, or "" when there is no commit yet.
	Head(ctx context.Context) (string, error)

	// ReadFile returns path's content at revision and whether it exists.
	ReadFile(ctx context.Context, revision, path string) ([]byte, bool, error)

	// Commit lands changes on parent, a nil content deleting its path. When
	// parent is not the tip it lands nothing and wraps ErrStaleBase.
	Commit(ctx context.Context, parent string, changes map[string][]byte, message string) (string, error)

	// Changed returns revision's parent, "" for the first commit, and the
	// sorted paths it changed.
	Changed(ctx context.Context, revision string) (string, []string, error)
}

// CommitManifests commits the files that differ from head, a nil content
// removing its path, and returns the resulting revision and whether it
// committed. It retries a moved tip a bounded number of times.
//
// It first refuses a file carrying any non-empty secret, wrapping
// ErrSecretInManifest. The guard matches only a value verbatim or as that
// value's own standard base64, so it misses a secret inside a larger encoded
// value or in another encoding: a block must never render secret material.
func CommitManifests(ctx context.Context, repo Repository, files map[string][]byte, message string, secrets []string) (string, bool, error) {
	if err := refuseSecrets(files, secrets); err != nil {
		return "", false, err
	}
	return commitFiles(ctx, repo, files, message)
}

// Revert restores, on head, each path revision changed to its content at
// revision's parent, and returns the revision holding the result.
func Revert(ctx context.Context, repo Repository, revision, message string) (string, error) {
	parent, paths, err := repo.Changed(ctx, revision)
	if err != nil {
		return "", fmt.Errorf("gitops: reading what %s changed: %w", revision, err)
	}
	restore := make(map[string][]byte, len(paths))
	for _, path := range paths {
		content, ok, err := readAt(ctx, repo, parent, path)
		if err != nil {
			return "", err
		}
		if !ok {
			content = nil
		}
		restore[path] = content
	}
	rev, _, err := commitFiles(ctx, repo, restore, message)
	return rev, err
}

// secretGroupKey names one key inside one group of a Desired's Secrets.
type secretGroupKey struct {
	group string
	key   string
}

// secretGroupIdentifiers lists the keys in a secret group that hold an
// identifier rather than a credential.
var secretGroupIdentifiers = map[secretGroupKey]bool{
	{group: "github", key: "owner"}: true,
}

// SecretValues returns every value in d's SecretInputs, AppSecrets and
// Secrets except the identifiers in secretGroupIdentifiers, for CommitManifests
// to refuse. An identifier such as the github owner legitimately appears in a
// manifest, for example in a repository URL, so refusing it would block a
// correct commit.
func SecretValues(d contract.Desired) []string {
	var values []string
	for _, v := range d.SecretInputs {
		values = append(values, v)
	}
	for _, v := range d.AppSecrets {
		values = append(values, v)
	}
	for groupName, group := range d.Secrets {
		for key, v := range group {
			if secretGroupIdentifiers[secretGroupKey{group: groupName, key: key}] {
				continue
			}
			values = append(values, v)
		}
	}
	return values
}

func refuseSecrets(files map[string][]byte, secrets []string) error {
	var needles [][]byte
	for _, s := range secrets {
		if s == "" {
			continue
		}
		needles = append(needles, []byte(s), []byte(base64.StdEncoding.EncodeToString([]byte(s))))
	}
	for _, path := range sortedPaths(files) {
		for _, needle := range needles {
			if bytes.Contains(files[path], needle) {
				return fmt.Errorf("%w: %s", ErrSecretInManifest, path)
			}
		}
	}
	return nil
}

func commitFiles(ctx context.Context, repo Repository, files map[string][]byte, message string) (string, bool, error) {
	var err error
	for range maxCommitTries {
		var head string
		head, err = repo.Head(ctx)
		if err != nil {
			return "", false, fmt.Errorf("gitops: reading head: %w", err)
		}
		changes, cerr := changesAt(ctx, repo, head, files)
		if cerr != nil {
			return "", false, cerr
		}
		if len(changes) == 0 {
			return head, false, nil
		}
		var rev string
		rev, err = repo.Commit(ctx, head, changes, message)
		if err == nil {
			return rev, true, nil
		}
		if !errors.Is(err, ErrStaleBase) {
			return "", false, fmt.Errorf("gitops: committing: %w", err)
		}
	}
	return "", false, fmt.Errorf("gitops: the tip moved on each of %d tries: %w", maxCommitTries, err)
}

func changesAt(ctx context.Context, repo Repository, revision string, files map[string][]byte) (map[string][]byte, error) {
	changes := map[string][]byte{}
	for path, want := range files {
		got, ok, err := readAt(ctx, repo, revision, path)
		if err != nil {
			return nil, err
		}
		switch {
		case want == nil && ok:
			changes[path] = nil
		case want != nil && (!ok || !bytes.Equal(got, want)):
			changes[path] = want
		}
	}
	return changes, nil
}

func readAt(ctx context.Context, repo Repository, revision, path string) ([]byte, bool, error) {
	if revision == "" {
		return nil, false, nil
	}
	content, ok, err := repo.ReadFile(ctx, revision, path)
	if err != nil {
		return nil, false, fmt.Errorf("gitops: reading %s at %s: %w", path, revision, err)
	}
	return content, ok, nil
}

func sortedPaths[V any](m map[string]V) []string {
	paths := make([]string, 0, len(m))
	for path := range m {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
