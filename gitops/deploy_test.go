package gitops_test

// Deploy commits a block's whole desired state once and waits for Argo CD's verdict on that revision, or on head when nothing changed.

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/gitops"
	"github.com/mitstack-eu/operator-kit/v2/gitops/gitopstest"
)

// failingNotes answers a failed sync for any revision, since gitopstest seeds a verdict by a revision a test cannot know before Deploy commits.
type failingNotes struct{}

func (failingNotes) Latest(_ context.Context, app, revision string) (gitops.Notification, bool, error) {
	return gitops.Notification{App: app, Revision: revision, Phase: "Failed", Health: "Degraded", Message: "one or more objects failed to apply"}, true, nil
}

func alphaDeploy() gitops.BlockDeploy {
	return gitops.BlockDeploy{App: "app-alpha", Files: renderApp("app-alpha", "k3s-fsn1").Files, Message: "deploy app-alpha", Secrets: seededSecrets, Poll: time.Millisecond}
}

func TestDeploy(t *testing.T) {
	cases := []struct {
		name      string
		notes     func(repo *gitopstest.Repository) gitops.Notifications
		deploys   int
		in        func() gitops.BlockDeploy
		wantErr   error
		wantVerd  gitops.Verdict
		wantCount int
		check     func(t *testing.T, repo *gitopstest.Repository, first, last gitops.Deployed)
	}{
		{"it commits every file once under the message and answers the verdict on that revision", succeeding, 1, alphaDeploy, nil, gitops.VerdictSucceeded, 1,
			func(t *testing.T, repo *gitopstest.Repository, _, got gitops.Deployed) {
				c := repo.Commits()[0]
				if !got.Changed || got.Revision != c.Revision || got.Outcome.Notification.Revision != got.Revision {
					t.Errorf("Deployed = %+v, want changed on revision %q with its notification", got, c.Revision)
				}
				if !reflect.DeepEqual(c.Changes, alphaDeploy().Files) || c.Message != "deploy app-alpha" {
					t.Errorf("commit = %+v, want every file under the message %q", c, "deploy app-alpha")
				}
			}},
		{"an unchanged desired state lands nothing and waits on head", succeeding, 2, alphaDeploy, nil, gitops.VerdictSucceeded, 1,
			func(t *testing.T, _ *gitopstest.Repository, first, again gitops.Deployed) {
				if again.Changed || again.Revision != first.Revision {
					t.Errorf("second Deployed changed, revision = %v, %q; want false and head %q", again.Changed, again.Revision, first.Revision)
				}
			}},
		{"a failed verdict comes back as the outcome", func(*gitopstest.Repository) gitops.Notifications { return failingNotes{} }, 1, alphaDeploy, nil, gitops.VerdictFailed, 1,
			func(t *testing.T, _ *gitopstest.Repository, _, got gitops.Deployed) {
				if got.Outcome.Notification.Phase != "Failed" {
					t.Errorf("outcome notification = %+v, want the failed sync Argo CD reported", got.Outcome.Notification)
				}
			}},
		{"a file carrying a secret is refused and nothing is committed", succeeding, 1, func() gitops.BlockDeploy {
			in := alphaDeploy()
			in.Files["apps/app-alpha/leak.yaml"] = []byte("token: " + githubToken + "\n")
			return in
		}, gitops.ErrSecretInManifest, "", 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := gitopstest.NewRepository()
			notes := tc.notes(repo)
			var first, last gitops.Deployed
			var err error

			for i := range tc.deploys {
				last, err = gitops.Deploy(context.Background(), repo, notes, tc.in())
				if i == 0 {
					first = last
				}
			}

			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) || last.Outcome.Verdict != tc.wantVerd || len(repo.Commits()) != tc.wantCount {
				t.Fatalf("Deploy = %+v, %v with %d commits; want verdict %q, error %v, %d commits", last, err, len(repo.Commits()), tc.wantVerd, tc.wantErr, tc.wantCount)
			}
			if tc.check != nil {
				tc.check(t, repo, first, last)
			}
		})
	}
}

func succeeding(repo *gitopstest.Repository) gitops.Notifications {
	notes := gitopstest.NewNotifications()
	notes.SucceedCommits("app-alpha", repo)
	return notes
}
