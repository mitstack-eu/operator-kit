package gitops_test

// revert_test.go pins Revert: it commits, on top of the current head, each
// path the reverted revision changed restored to its content at that
// revision's parent, deleting a path absent there, leaves every other path
// alone, and commits nothing when head already matches.

import (
	"context"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/gitops"
	"github.com/mitstack-eu/operator-kit/v2/gitops/gitopstest"
)

func TestRevert_RestoresEachChangedPathToItsParentContentAndDeletesAnAddedOne(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	base, err := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a1\n"), "b.yaml": []byte("b1\n")}, "base")
	if err != nil {
		t.Fatalf("base commit: %v", err)
	}
	bad, err := repo.Commit(ctx, base, map[string][]byte{"a.yaml": []byte("a2\n"), "b.yaml": nil, "c.yaml": []byte("c2\n")}, "bad deploy")
	if err != nil {
		t.Fatalf("bad commit: %v", err)
	}

	rev, err := gitops.Revert(ctx, repo, bad, "revert bad deploy")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	commits := repo.Commits()
	if len(commits) != 3 {
		t.Fatalf("Commits = %d, want 3: base, bad, then the revert", len(commits))
	}
	last := commits[2]
	if last.Revision != rev || last.Parent != bad || last.Message != "revert bad deploy" {
		t.Errorf("revert commit %q on %q with message %q, want %q on %q with the message handed in", last.Revision, last.Parent, last.Message, rev, bad)
	}
	wantFilesAt(t, repo, rev, map[string][]byte{"a.yaml": []byte("a1\n"), "b.yaml": []byte("b1\n"), "c.yaml": nil})
}

func TestRevert_LandsOnTheCurrentHeadAndLeavesALaterUnrelatedCommitAlone(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	base, _ := repo.Commit(ctx, "", map[string][]byte{"apps/app-alpha/deploy.yaml": []byte("image: v1\n")}, "alpha v1")
	bad, _ := repo.Commit(ctx, base, map[string][]byte{"apps/app-alpha/deploy.yaml": []byte("image: v2\n")}, "alpha v2")
	later, err := repo.Commit(ctx, bad, map[string][]byte{"apps/app-beta/deploy.yaml": []byte("image: b1\n")}, "beta b1")
	if err != nil {
		t.Fatalf("later commit: %v", err)
	}

	rev, err := gitops.Revert(ctx, repo, bad, "revert alpha v2")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	commits := repo.Commits()
	if last := commits[len(commits)-1]; last.Revision != rev || last.Parent != later {
		t.Errorf("revert commit %q on %q, want it on the current head %q", last.Revision, last.Parent, later)
	}
	wantFilesAt(t, repo, rev, map[string][]byte{
		"apps/app-alpha/deploy.yaml": []byte("image: v1\n"),
		"apps/app-beta/deploy.yaml":  []byte("image: b1\n"),
	})
}

func TestRevert_OfTheFirstCommitDeletesEveryPathItAdded(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	first, _ := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a\n"), "b.yaml": []byte("b\n")}, "first")

	rev, err := gitops.Revert(ctx, repo, first, "revert first")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if rev == first {
		t.Fatal("Revert returned the reverted revision, want a new commit")
	}
	wantFilesAt(t, repo, rev, map[string][]byte{"a.yaml": nil, "b.yaml": nil})
}

func TestRevert_WhenHeadAlreadyMatchesTheParentCommitsNothingAndReturnsHead(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	base, _ := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a1\n")}, "base")
	bad, _ := repo.Commit(ctx, base, map[string][]byte{"a.yaml": []byte("a2\n")}, "bad")
	reverted, err := gitops.Revert(ctx, repo, bad, "revert bad")
	if err != nil {
		t.Fatalf("first Revert: %v", err)
	}

	again, err := gitops.Revert(ctx, repo, bad, "revert bad")
	if err != nil {
		t.Fatalf("second Revert: %v", err)
	}
	if again != reverted {
		t.Errorf("second Revert = %q, want head %q unchanged", again, reverted)
	}
	if got := len(repo.Commits()); got != 3 {
		t.Errorf("Commits = %d, want 3: a revert that finds head already matching lands nothing", got)
	}
}

func TestRevert_OfARevisionTheRepositoryNeverHeldFailsAndCommitsNothing(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	if _, err := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a\n")}, "base"); err != nil {
		t.Fatalf("base commit: %v", err)
	}

	if _, err := gitops.Revert(ctx, repo, "no-such-revision", "revert"); err == nil {
		t.Error("Revert of an unknown revision returned no error")
	}
	if got := len(repo.Commits()); got != 1 {
		t.Errorf("Commits = %d, want 1", got)
	}
}
