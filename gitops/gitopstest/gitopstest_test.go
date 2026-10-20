package gitopstest_test

// Repository and Notifications behave as the gitops.Repository and gitops.Notifications a block author tests against, as their doc comments say.

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/gitops"
	"github.com/mitstack-eu/operator-kit/v2/gitops/gitopstest"
)

var (
	_ gitops.Repository    = (*gitopstest.Repository)(nil)
	_ gitops.Notifications = (*gitopstest.Notifications)(nil)
)

func TestRepository_AnEmptyRepositoryHasHeadEmptyAndHoldsNoFile(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()

	head, err := repo.Head(ctx)
	if err != nil || head != "" {
		t.Fatalf("Head = %q, %v, want \"\", nil for an empty repository", head, err)
	}
	content, ok, err := repo.ReadFile(ctx, "", "apps/app-alpha/namespace.yaml")
	if err != nil || ok || content != nil {
		t.Errorf("ReadFile at revision \"\" = %q, %v, %v, want nil, false, nil", content, ok, err)
	}
	if got := repo.Commits(); len(got) != 0 {
		t.Errorf("Commits = %+v, want none", got)
	}
}

func TestRepository_ACommitOnTheTipLandsAndMovesHead(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()

	rev, err := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a: 1\n")}, "add a")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if rev == "" {
		t.Fatal("Commit returned an empty revision")
	}
	head, err := repo.Head(ctx)
	if err != nil || head != rev {
		t.Errorf("Head = %q, %v, want the new revision %q", head, err, rev)
	}
	content, ok, err := repo.ReadFile(ctx, rev, "a.yaml")
	if err != nil || !ok || string(content) != "a: 1\n" {
		t.Errorf("ReadFile(a.yaml) = %q, %v, %v, want the committed content", content, ok, err)
	}
	_, ok, err = repo.ReadFile(ctx, rev, "b.yaml")
	if err != nil || ok {
		t.Errorf("ReadFile(b.yaml) = %v, %v, want false, nil for a path never written", ok, err)
	}
}

func TestRepository_ACommitOnAParentThatIsNotTheTipFailsWithErrStaleBaseAndLandsNothing(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	first, err := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a: 1\n")}, "add a")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	_, err = repo.Commit(ctx, "", map[string][]byte{"b.yaml": []byte("b: 1\n")}, "add b on a stale base")
	if !errors.Is(err, gitops.ErrStaleBase) {
		t.Fatalf("Commit on parent \"\" with tip %q: err = %v, want gitops.ErrStaleBase", first, err)
	}
	if got := len(repo.Commits()); got != 1 {
		t.Errorf("Commits = %d, want 1: the stale commit must land nothing", got)
	}
	if head, _ := repo.Head(ctx); head != first {
		t.Errorf("Head = %q, want %q unchanged", head, first)
	}
}

func TestRepository_ANilChangeDeletesItsPathAndOtherPathsCarryOver(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	first, err := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a\n"), "b.yaml": []byte("b\n")}, "add a and b")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	second, err := repo.Commit(ctx, first, map[string][]byte{"a.yaml": nil}, "delete a")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if _, ok, err := repo.ReadFile(ctx, second, "a.yaml"); err != nil || ok {
		t.Errorf("ReadFile(a.yaml) at the deleting commit = %v, %v, want false, nil", ok, err)
	}
	if content, ok, err := repo.ReadFile(ctx, second, "b.yaml"); err != nil || !ok || string(content) != "b\n" {
		t.Errorf("ReadFile(b.yaml) at the deleting commit = %q, %v, %v, want it carried over", content, ok, err)
	}
	if content, ok, err := repo.ReadFile(ctx, first, "a.yaml"); err != nil || !ok || string(content) != "a\n" {
		t.Errorf("ReadFile(a.yaml) at the first commit = %q, %v, %v, want history kept", content, ok, err)
	}
}

func TestRepository_ReadFileAndChangedRefuseARevisionTheRepositoryNeverHeld(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()

	if _, _, err := repo.ReadFile(ctx, "no-such-revision", "a.yaml"); err == nil {
		t.Error("ReadFile at an unknown revision returned no error")
	}
	if _, _, err := repo.Changed(ctx, "no-such-revision"); err == nil {
		t.Error("Changed of an unknown revision returned no error")
	}
}

func TestRepository_ChangedReportsTheParentAndTheSortedPathsACommitTouched(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	first, err := repo.Commit(ctx, "", map[string][]byte{"z.yaml": []byte("z\n"), "a.yaml": []byte("a\n")}, "add")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	second, err := repo.Commit(ctx, first, map[string][]byte{"z.yaml": nil, "m.yaml": []byte("m\n")}, "change")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	parent, paths, err := repo.Changed(ctx, first)
	if err != nil || parent != "" || !reflect.DeepEqual(paths, []string{"a.yaml", "z.yaml"}) {
		t.Errorf("Changed(first) = %q, %q, %v, want \"\", [a.yaml z.yaml], nil", parent, paths, err)
	}
	parent, paths, err = repo.Changed(ctx, second)
	if err != nil || parent != first || !reflect.DeepEqual(paths, []string{"m.yaml", "z.yaml"}) {
		t.Errorf("Changed(second) = %q, %q, %v, want %q, [m.yaml z.yaml], nil", parent, paths, err, first)
	}
}

func TestRepository_CommitsRecordsEveryLandedCommitInOrder(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	first, _ := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a\n")}, "add a")
	second, _ := repo.Commit(ctx, first, map[string][]byte{"a.yaml": nil}, "delete a")

	want := []gitopstest.Commit{
		{Revision: first, Parent: "", Message: "add a", Changes: map[string][]byte{"a.yaml": []byte("a\n")}},
		{Revision: second, Parent: first, Message: "delete a", Changes: map[string][]byte{"a.yaml": nil}},
	}
	if got := repo.Commits(); !reflect.DeepEqual(got, want) {
		t.Errorf("Commits = %+v, want %+v", got, want)
	}
	if first == second {
		t.Errorf("two commits share revision %q", first)
	}
}

func TestRepository_ACommitKeepsItsOwnCopyOfTheContentItWasHanded(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	content := []byte("a: 1\n")
	changes := map[string][]byte{"a.yaml": content}
	rev, err := repo.Commit(ctx, "", changes, "add a")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	content[0] = 'X'
	changes["b.yaml"] = []byte("b\n")

	got, ok, err := repo.ReadFile(ctx, rev, "a.yaml")
	if err != nil || !ok || string(got) != "a: 1\n" {
		t.Errorf("ReadFile(a.yaml) = %q, %v, %v after the caller changed its bytes, want the committed %q", got, ok, err, "a: 1\n")
	}
	if _, ok, _ := repo.ReadFile(ctx, rev, "b.yaml"); ok {
		t.Error("a path the caller added to its map after the commit appeared in the repository")
	}
}

func TestRepository_MoveTipMakesEachOfTheNextNCommitsFindAnotherWritersCommitFirst(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	foreign := map[string][]byte{"apps/app-beta/config.yaml": []byte("beta\n")}
	repo.MoveTip(2, foreign)

	for i := 1; i <= 2; i++ {
		head, _ := repo.Head(ctx)
		_, err := repo.Commit(ctx, head, map[string][]byte{"a.yaml": []byte("a\n")}, "add a")
		if !errors.Is(err, gitops.ErrStaleBase) {
			t.Fatalf("commit %d after MoveTip(2): err = %v, want gitops.ErrStaleBase", i, err)
		}
	}
	commits := repo.Commits()
	if len(commits) != 2 {
		t.Fatalf("Commits = %d after two stale commits, want the 2 foreign commits that moved the tip", len(commits))
	}
	for i, c := range commits {
		if !reflect.DeepEqual(c.Changes, foreign) {
			t.Errorf("foreign commit %d changes = %q, want the changes MoveTip was handed", i, c.Changes)
		}
	}

	head, _ := repo.Head(ctx)
	if head != commits[1].Revision {
		t.Errorf("Head = %q, want the second foreign commit %q", head, commits[1].Revision)
	}
	rev, err := repo.Commit(ctx, head, map[string][]byte{"a.yaml": []byte("a\n")}, "add a")
	if err != nil {
		t.Fatalf("the third commit after MoveTip(2): %v, want it to land", err)
	}
	if content, ok, _ := repo.ReadFile(ctx, rev, "apps/app-beta/config.yaml"); !ok || string(content) != "beta\n" {
		t.Errorf("the foreign file at the landed commit = %q, %v, want it kept", content, ok)
	}
}

func adrNotification(revision string) gitops.Notification {
	return gitops.Notification{
		App: "app-alpha", Project: "app-alpha", Sync: "Synced", Revision: revision,
		Health: "Healthy", Phase: "Succeeded", Message: "successfully synced (all tasks run)",
	}
}

func TestNotifications_LatestAnswersWhatSetSeededAndNothingElse(t *testing.T) {
	ctx := context.Background()
	notes := gitopstest.NewNotifications()
	seeded := adrNotification("rev-1")
	notes.Set(seeded)

	got, ok, err := notes.Latest(ctx, "app-alpha", "rev-1")

	if err != nil || !ok || !reflect.DeepEqual(got, seeded) {
		t.Errorf("Latest(app-alpha, rev-1) = %+v, %v, %v, want the seeded notification", got, ok, err)
	}
	for _, q := range [][2]string{{"app-alpha", "rev-2"}, {"app-beta", "rev-1"}} {
		if _, ok, err := notes.Latest(ctx, q[0], q[1]); err != nil || ok {
			t.Errorf("Latest(%s, %s) = %v, %v, want none for what was never seeded", q[0], q[1], ok, err)
		}
	}
}

func TestNotifications_SequenceAnswersEachInTurnThenRepeatsTheLastAndAZeroNotificationIsNone(t *testing.T) {
	ctx := context.Background()
	notes := gitopstest.NewNotifications()
	running := gitops.Notification{App: "app-alpha", Revision: "rev-1", Phase: "Running", Health: "Progressing"}
	done := adrNotification("rev-1")
	notes.Sequence("app-alpha", "rev-1", gitops.Notification{}, running, done)

	if _, ok, err := notes.Latest(ctx, "app-alpha", "rev-1"); err != nil || ok {
		t.Errorf("read 1 = %v, %v, want none: the zero notification answers none", ok, err)
	}
	if got, ok, _ := notes.Latest(ctx, "app-alpha", "rev-1"); !ok || !reflect.DeepEqual(got, running) {
		t.Errorf("read 2 = %+v, %v, want the running notification", got, ok)
	}
	for i := 3; i <= 4; i++ {
		if got, ok, _ := notes.Latest(ctx, "app-alpha", "rev-1"); !ok || !reflect.DeepEqual(got, done) {
			t.Errorf("read %d = %+v, %v, want the last notification, repeated", i, got, ok)
		}
	}
	if got := notes.Reads("app-alpha", "rev-1"); got != 4 {
		t.Errorf("Reads = %d, want 4", got)
	}
}

func TestNotifications_SucceedCommitsAnswersSucceededAndHealthyForEveryRevisionTheRepositoryHolds(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	notes := gitopstest.NewNotifications()
	first, _ := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a\n")}, "add a")
	notes.SucceedCommits("app-alpha", repo)
	second, _ := repo.Commit(ctx, first, map[string][]byte{"a.yaml": nil}, "delete a")

	for _, rev := range []string{first, second} {
		got, ok, err := notes.Latest(ctx, "app-alpha", rev)
		if err != nil || !ok {
			t.Errorf("Latest(app-alpha, %s) = %v, %v, want a notification for a revision the repository holds", rev, ok, err)
			continue
		}
		if got.App != "app-alpha" || got.Revision != rev || got.Phase != "Succeeded" || got.Health != "Healthy" {
			t.Errorf("Latest(app-alpha, %s) = %+v, want app-alpha at that revision, phase Succeeded, health Healthy", rev, got)
		}
		if gitops.Judge(got, rev) != gitops.VerdictSucceeded {
			t.Errorf("Judge of the auto answer for %s = %v, want succeeded", rev, gitops.Judge(got, rev))
		}
	}
	if _, ok, _ := notes.Latest(ctx, "app-alpha", "never-committed"); ok {
		t.Error("SucceedCommits answered for a revision the repository never held")
	}
	if _, ok, _ := notes.Latest(ctx, "app-beta", first); ok {
		t.Error("SucceedCommits for app-alpha answered for app-beta")
	}
}

func TestNotifications_LatestForRevisionEmptyAnswersTheNotificationMostRecentlySetForTheApp(t *testing.T) {
	ctx := context.Background()
	notes := gitopstest.NewNotifications()

	if _, ok, err := notes.Latest(ctx, "app-alpha", ""); err != nil || ok {
		t.Errorf("Latest(app-alpha, \"\") with nothing set = %v, %v, want none", ok, err)
	}

	older := adrNotification("rev-2")
	newer := gitops.Notification{App: "app-alpha", Revision: "rev-1", Phase: "Running", Health: "Progressing"}
	notes.Set(older)
	notes.Set(gitops.Notification{App: "app-beta", Revision: "rev-9", Phase: "Succeeded", Health: "Healthy"})
	notes.Set(newer)

	got, ok, err := notes.Latest(ctx, "app-alpha", "")
	if err != nil || !ok || !reflect.DeepEqual(got, newer) {
		t.Errorf("Latest(app-alpha, \"\") = %+v, %v, %v, want the notification set last for app-alpha, whatever its revision", got, ok, err)
	}
	if got, ok, _ := notes.Latest(ctx, "app-alpha", "rev-2"); !ok || !reflect.DeepEqual(got, older) {
		t.Errorf("Latest(app-alpha, rev-2) = %+v, %v, want the exact-revision answer kept", got, ok)
	}
	if got := notes.Reads("app-alpha", ""); got != 2 {
		t.Errorf("Reads(app-alpha, \"\") = %d, want 2", got)
	}
}

func TestNotifications_SucceedCommitsAnswersRevisionEmptyWithTheRepositorysCurrentHead(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	notes := gitopstest.NewNotifications()
	notes.SucceedCommits("app-alpha", repo)

	if _, ok, err := notes.Latest(ctx, "app-alpha", ""); err != nil || ok {
		t.Errorf("Latest(app-alpha, \"\") on an empty repository = %v, %v, want none", ok, err)
	}

	first, _ := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a\n")}, "add a")
	second, _ := repo.Commit(ctx, first, map[string][]byte{"b.yaml": []byte("b\n")}, "add b")

	got, ok, err := notes.Latest(ctx, "app-alpha", "")
	if err != nil || !ok {
		t.Fatalf("Latest(app-alpha, \"\") = %v, %v, want a notification", ok, err)
	}
	if got.App != "app-alpha" || got.Revision != second || got.Phase != "Succeeded" || got.Health != "Healthy" {
		t.Errorf("Latest(app-alpha, \"\") = %+v, want app-alpha at head %s, phase Succeeded, health Healthy", got, second)
	}
	if _, ok, _ := notes.Latest(ctx, "app-beta", ""); ok {
		t.Error("SucceedCommits for app-alpha answered Latest(app-beta, \"\")")
	}
}

func TestNotifications_ANotificationSetForTheAppAnswersRevisionEmptyOverTheAutoAnswer(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	notes := gitopstest.NewNotifications()
	notes.SucceedCommits("app-alpha", repo)
	first, _ := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a\n")}, "add a")
	if _, err := repo.Commit(ctx, first, map[string][]byte{"b.yaml": []byte("b\n")}, "add b"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	pinned := gitops.Notification{App: "app-alpha", Revision: first, Phase: "Running", Health: "Progressing"}
	notes.Set(pinned)

	if got, ok, _ := notes.Latest(ctx, "app-alpha", ""); !ok || !reflect.DeepEqual(got, pinned) {
		t.Errorf("Latest(app-alpha, \"\") = %+v, %v, want the notification set for the app, not the auto answer for head", got, ok)
	}
}

func TestNotifications_SetOverridesTheAutoAnswerForItsRevision(t *testing.T) {
	ctx := context.Background()
	repo := gitopstest.NewRepository()
	notes := gitopstest.NewNotifications()
	notes.SucceedCommits("app-alpha", repo)
	rev, _ := repo.Commit(ctx, "", map[string][]byte{"a.yaml": []byte("a\n")}, "add a")
	failed := gitops.Notification{App: "app-alpha", Revision: rev, Phase: "Failed", Health: "Degraded", Message: "ImagePullBackOff"}
	notes.Set(failed)

	if got, ok, _ := notes.Latest(ctx, "app-alpha", rev); !ok || !reflect.DeepEqual(got, failed) {
		t.Errorf("Latest = %+v, %v, want the seeded failure over the auto answer", got, ok)
	}
}

func TestNotifications_FailWithMakesEveryReadReturnTheError(t *testing.T) {
	ctx := context.Background()
	notes := gitopstest.NewNotifications()
	notes.Set(adrNotification("rev-1"))
	boom := errors.New("notification store unreachable")
	notes.FailWith(boom)

	if _, _, err := notes.Latest(ctx, "app-alpha", "rev-1"); !errors.Is(err, boom) {
		t.Errorf("Latest err = %v, want the FailWith error", err)
	}
}
