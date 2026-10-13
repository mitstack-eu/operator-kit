package observe_test

// tagged_test.go pins the tag listers blockverify's tagging guard reads:
// each lists everything carrying the subscription's id, as AWS tags it
// (mitstack:subscription, the key aws-app-infra writes), Hetzner labels it
// (mitstack.dev/subscription) or GitHub marks it with a topic and a commit
// trailer, as the type it is. An object the lister cannot type is still
// listed, with no type, so the guard reports it rather than missing it.

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/observe"
)

var (
	_ observe.TaggedLister = observe.AWSTagged{}
	_ observe.TaggedLister = observe.HetznerLabelled{}
	_ observe.TaggedLister = observe.GitHubTagged{}
)

type fakeTagging struct {
	arns          []string
	key, value    string
	err           error
	subscriptions []string
}

func (f *fakeTagging) ResourceARNs(_ context.Context, d contract.Desired, tagKey, tagValue string) ([]string, error) {
	f.key, f.value = tagKey, tagValue
	f.subscriptions = append(f.subscriptions, d.SubscriptionID)
	return f.arns, f.err
}

func TestAWSTagged_ListsEachTaggedARNAsItsType(t *testing.T) {
	api := &fakeTagging{arns: []string{
		"arn:aws:ec2:eu-central-1:123456789012:instance/i-0abc",
		"arn:aws:ec2:eu-central-1:123456789012:vpc/vpc-0abc",
		"arn:aws:ec2:eu-central-1:123456789012:security-group/sg-0abc",
		"arn:aws:ec2:eu-central-1:123456789012:volume/vol-0abc",
		"arn:aws:s3:::app-state",
		"arn:aws:iam::123456789012:role/deploy",
		"arn:aws:ecr:eu-central-1:123456789012:repository/app",
		"arn:aws:sqs:eu-central-1:123456789012:stray-queue",
	}}
	d := contract.Desired{Identity: contract.Identity{SubscriptionID: "sub-1"}}
	got, err := observe.AWSTagged{API: api}.ListTagged(context.Background(), d, "sub-1")
	if err != nil {
		t.Fatalf("ListTagged: %v", err)
	}
	want := []observe.Tagged{
		{Type: "aws.ec2-instance", ID: "i-0abc"},
		{Type: "aws.vpc", ID: "vpc-0abc"},
		{Type: "aws.security-group", ID: "sg-0abc"},
		{Type: "aws.ebs-volume", ID: "vol-0abc"},
		{Type: "aws.s3-bucket", ID: "app-state"},
		{Type: "aws.iam-role", ID: "deploy"},
		{Type: "aws.ecr-repository", ID: "app"},
		{Type: "", ID: "arn:aws:sqs:eu-central-1:123456789012:stray-queue"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListTagged =\n%+v\nwant\n%+v", got, want)
	}
	if api.key != "mitstack:subscription" || api.value != "sub-1" {
		t.Errorf("asked for tag %q = %q, want mitstack:subscription = sub-1, the key aws-app-infra tags with", api.key, api.value)
	}
}

func TestAWSTagged_ListsEachPartARNAsItsCatalogueType(t *testing.T) {
	api := &fakeTagging{arns: []string{
		"arn:aws:iam::123456789012:instance-profile/mitstack/mitstack-app-myapp-profile",
		"arn:aws:ec2:eu-central-1:123456789012:elastic-ip/eipalloc-0abc",
		"arn:aws:ec2:eu-central-1:123456789012:security-group-rule/sgr-0abc",
		"arn:aws:dlm:eu-central-1:123456789012:policy/policy-0abc",
		"arn:aws:ssm:eu-central-1:123456789012:document/mitstack-app-myapp-edge-config",
	}}

	got, err := observe.AWSTagged{API: api}.ListTagged(context.Background(), contract.Desired{}, "sub-1")

	if err != nil {
		t.Fatalf("ListTagged: %v", err)
	}
	want := []observe.Tagged{
		{Type: "aws.iam-instance-profile", ID: "mitstack-app-myapp-profile"},
		{Type: "aws.elastic-ip", ID: "eipalloc-0abc"},
		{Type: "aws.security-group-rule", ID: "sgr-0abc"},
		{Type: "aws.dlm-lifecycle-policy", ID: "policy-0abc"},
		{Type: "aws.ssm-document", ID: "mitstack-app-myapp-edge-config"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListTagged(%q) =\n%+v\nwant\n%+v", api.arns, got, want)
	}
}

func TestAWSTagged_ReturnsTheAPIsError(t *testing.T) {
	boom := errors.New("tagging: throttled")
	_, err := observe.AWSTagged{API: &fakeTagging{err: boom}}.ListTagged(context.Background(), contract.Desired{}, "sub-1")
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the API's error", err)
	}
}

type fakeLabels struct {
	objects  []observe.HetznerObject
	selector string
}

func (f *fakeLabels) ListByLabel(_ context.Context, _ contract.Desired, selector string) ([]observe.HetznerObject, error) {
	f.selector = selector
	return f.objects, nil
}

func TestHetznerLabelled_ListsEachLabelledObjectAsItsType(t *testing.T) {
	api := &fakeLabels{objects: []observe.HetznerObject{
		{Kind: "server", ID: "4711"},
		{Kind: "primary_ip", ID: "99"},
		{Kind: "firewall", ID: "12"},
		{Kind: "volume", ID: "55"},
		{Kind: "ssh_key", ID: "8"},
		{Kind: "network", ID: "77"},
		{Kind: "load_balancer", ID: "31"},
	}}
	got, err := observe.HetznerLabelled{API: api}.ListTagged(context.Background(), contract.Desired{}, "sub-1")
	if err != nil {
		t.Fatalf("ListTagged: %v", err)
	}
	want := []observe.Tagged{
		{Type: "hetzner.server", ID: "4711"},
		{Type: "hetzner.primary-ip", ID: "99"},
		{Type: "hetzner.firewall", ID: "12"},
		{Type: "hetzner.volume", ID: "55"},
		{Type: "hetzner.ssh-key", ID: "8"},
		{Type: "hetzner.network", ID: "77"},
		{Type: "", ID: "load_balancer/31"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListTagged =\n%+v\nwant\n%+v", got, want)
	}
	if want := "mitstack.dev/subscription=sub-1"; api.selector != want {
		t.Errorf("label selector = %q, want %q", api.selector, want)
	}
}

const (
	ghSub       = "sub_3e304e324312"
	ghProcessor = "https://api.mitstack.dev/cicd/hook"
	ghTrailer   = "\n\nMitstack-Subscription: " + ghSub
)

// fakeGitHub answers RepositoriesWithTopic with every repository it holds, so the lister must keep only those carrying the topic itself.
type fakeGitHub struct {
	topics    map[string][]string
	hooks     map[string][]observe.GitHubWebhook
	secrets   map[string][]string
	variables map[string][]string
	commits   map[string][]observe.GitHubCommit
	err       error
	asked     []string
}

func (f *fakeGitHub) RepositoriesWithTopic(_ context.Context, _ contract.Desired, topic string) ([]observe.GitHubRepository, error) {
	f.asked = append(f.asked, topic)
	if f.err != nil {
		return nil, f.err
	}
	names := make([]string, 0, len(f.topics))
	for name := range f.topics {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]observe.GitHubRepository, 0, len(names))
	for _, name := range names {
		out = append(out, observe.GitHubRepository{FullName: name, Topics: f.topics[name]})
	}
	return out, nil
}

func (f *fakeGitHub) Webhooks(_ context.Context, _ contract.Desired, repo string) ([]observe.GitHubWebhook, error) {
	return f.hooks[repo], f.err
}

func (f *fakeGitHub) ActionsSecretNames(_ context.Context, _ contract.Desired, repo string) ([]string, error) {
	return f.secrets[repo], f.err
}

func (f *fakeGitHub) ActionsVariableNames(_ context.Context, _ contract.Desired, repo string) ([]string, error) {
	return f.variables[repo], f.err
}

func (f *fakeGitHub) Commits(_ context.Context, _ contract.Desired, repo string) ([]observe.GitHubCommit, error) {
	return f.commits[repo], f.err
}

// ghWorld holds a CI repository the subscription created and tagged, the app repository its github.repository requirement binds, and the repositories and commits of everyone else.
func ghWorld() *fakeGitHub {
	return &fakeGitHub{
		topics: map[string][]string{
			"acme/app-ci":    {"team-ci", "mitstack-sub-3e304e324312"},
			"acme/app":       {"production"},
			"acme/untagged":  nil,
			"acme/other-ci":  {"mitstack-sub-9f9f9f9f9f9f"},
			"acme/longer-ci": {"mitstack-sub-3e304e3243129"},
			"acme/elsewhere": nil,
		},
		hooks: map[string][]observe.GitHubWebhook{
			"acme/app-ci":    {{ID: "11", URL: "https://example.com/any-hook"}},
			"acme/app":       {{ID: "21", URL: ghProcessor}, {ID: "22", URL: "https://elsewhere.example.com/hook"}},
			"acme/other-ci":  {{ID: "31", URL: ghProcessor}},
			"acme/elsewhere": {{ID: "41", URL: ghProcessor}},
		},
		secrets: map[string][]string{
			"acme/app-ci": {"DEPLOY_KEY", "TRACKER_TOKEN"},
			"acme/app":    {"DEPLOY_KEY"},
		},
		variables: map[string][]string{
			"acme/app-ci":   {"MITSTACK_APP_ID", "TRACKER_BASE_URL"},
			"acme/app":      {"MITSTACK_APP_REPO", "UNHANDED_VARIABLE"},
			"acme/other-ci": {"MITSTACK_APP_ID"},
		},
		commits: map[string][]observe.GitHubCommit{
			"acme/app-ci": {
				{Message: "mitstack: seed managed CI pipeline" + ghTrailer, Paths: []string{".github/workflows/build.yml", ".github/workflows/dispatch.yml"}},
				{Message: "docs: a person's commit", Paths: []string{"README.md"}},
				{Message: "mitstack: reseed" + ghTrailer + "\n", Paths: []string{".github/workflows/build.yml"}},
				{Message: "mitstack: another subscription's seed\n\nMitstack-Subscription: sub_9f9f9f9f9f9f", Paths: []string{"other.yml"}},
				{Message: "mitstack: a longer id's seed\n\nMitstack-Subscription: sub_3e304e3243129", Paths: []string{"longer.yml"}},
				{Message: "Mitstack-Subscription: " + ghSub + "\n\nnames the trailer in its subject only", Paths: []string{"subject.yml"}},
			},
			"acme/app": {
				{Message: "mitstack: scaffold app source" + ghTrailer, Paths: []string{"Dockerfile", "main.go"}},
			},
			"acme/untagged": {
				{Message: "mitstack: seed" + ghTrailer, Paths: []string{"stray.yml"}},
			},
		},
	}
}

func ghDesired() contract.Desired {
	return contract.Desired{
		Identity:         contract.Identity{SubscriptionID: ghSub},
		RequirementTypes: map[string]string{"app-repo": "github.repository", "runner": "github.self-hosted-runner"},
		Bindings: map[string]map[string]string{
			"app-repo": {"repo": "acme/app"},
			"runner":   {"repo": "acme/elsewhere"},
		},
	}
}

func ghLister(api *fakeGitHub) observe.GitHubTagged {
	return observe.GitHubTagged{
		API:          api,
		ProcessorURL: ghProcessor,
		Secrets:      []string{"DEPLOY_KEY"},
		Variables:    []string{"MITSTACK_APP_ID", "MITSTACK_APP_REPO"},
	}
}

func countTagged(tagged []observe.Tagged, want observe.Tagged) int {
	n := 0
	for _, tg := range tagged {
		if tg == want {
			n++
		}
	}
	return n
}

func TestGitHubTagged_ListsWhatCountsAsTheSubscriptions(t *testing.T) {
	type row struct {
		name   string
		tagged observe.Tagged
		listed bool
	}
	cases := []struct {
		name       string
		commitType string
		rows       []row
	}{
		{
			name: "files committed under the trailer count one by one as workflow files",
			rows: []row{
				{"a repository carrying the topic", observe.Tagged{Type: "github.repository", ID: "acme/app-ci"}, true},
				{"any webhook on a repository carrying the topic", observe.Tagged{Type: "github.repository-webhook", ID: "acme/app-ci#hooks/11"}, true},
				{"a handed secret on a repository carrying the topic", observe.Tagged{Type: "github.actions-secret", ID: "acme/app-ci#DEPLOY_KEY"}, true},
				{"a handed variable on a repository carrying the topic", observe.Tagged{Type: "github.actions-variable", ID: "acme/app-ci#MITSTACK_APP_ID"}, true},
				{"a file a trailer commit wrote on a repository carrying the topic", observe.Tagged{Type: "github.workflow-file", ID: "acme/app-ci/.github/workflows/build.yml"}, true},
				{"a second file the same trailer commit wrote", observe.Tagged{Type: "github.workflow-file", ID: "acme/app-ci/.github/workflows/dispatch.yml"}, true},
				{"a webhook pointing at the processor URL on the bound repository", observe.Tagged{Type: "github.repository-webhook", ID: "acme/app#hooks/21"}, true},
				{"a handed secret on the bound repository", observe.Tagged{Type: "github.actions-secret", ID: "acme/app#DEPLOY_KEY"}, true},
				{"a handed variable on the bound repository", observe.Tagged{Type: "github.actions-variable", ID: "acme/app#MITSTACK_APP_REPO"}, true},
				{"a file a trailer commit wrote on the bound repository", observe.Tagged{Type: "github.workflow-file", ID: "acme/app/Dockerfile"}, true},
				{"a second file that commit wrote on the bound repository", observe.Tagged{Type: "github.workflow-file", ID: "acme/app/main.go"}, true},
				{"the bound repository, which carries no topic", observe.Tagged{Type: "github.repository", ID: "acme/app"}, false},
				{"a repository with no topic", observe.Tagged{Type: "github.repository", ID: "acme/untagged"}, false},
				{"a repository carrying another subscription's topic", observe.Tagged{Type: "github.repository", ID: "acme/other-ci"}, false},
				{"a repository carrying the topic of an id this one prefixes", observe.Tagged{Type: "github.repository", ID: "acme/longer-ci"}, false},
				{"a webhook with another URL on the bound repository", observe.Tagged{Type: "github.repository-webhook", ID: "acme/app#hooks/22"}, false},
				{"a webhook with the processor URL on another requirement's repository", observe.Tagged{Type: "github.repository-webhook", ID: "acme/elsewhere#hooks/41"}, false},
				{"a webhook on another subscription's repository", observe.Tagged{Type: "github.repository-webhook", ID: "acme/other-ci#hooks/31"}, false},
				{"a secret under a name the block did not hand the lister", observe.Tagged{Type: "github.actions-secret", ID: "acme/app-ci#TRACKER_TOKEN"}, false},
				{"a variable under a name the block did not hand the lister on a repository carrying the topic", observe.Tagged{Type: "github.actions-variable", ID: "acme/app-ci#TRACKER_BASE_URL"}, false},
				{"a variable under a name the block did not hand the lister on the bound repository", observe.Tagged{Type: "github.actions-variable", ID: "acme/app#UNHANDED_VARIABLE"}, false},
				{"a handed variable on another subscription's repository", observe.Tagged{Type: "github.actions-variable", ID: "acme/other-ci#MITSTACK_APP_ID"}, false},
				{"a file a person committed with no trailer", observe.Tagged{Type: "github.workflow-file", ID: "acme/app-ci/README.md"}, false},
				{"a file committed under another subscription's trailer", observe.Tagged{Type: "github.workflow-file", ID: "acme/app-ci/other.yml"}, false},
				{"a file committed under the trailer of an id this one prefixes", observe.Tagged{Type: "github.workflow-file", ID: "acme/app-ci/longer.yml"}, false},
				{"a file whose commit names the trailer in its subject, not its closing trailer block", observe.Tagged{Type: "github.workflow-file", ID: "acme/app-ci/subject.yml"}, false},
				{"a file committed under the trailer on a repository neither tagged nor bound", observe.Tagged{Type: "github.workflow-file", ID: "acme/untagged/stray.yml"}, false},
			},
		},
		{
			name:       "commits under the trailer count once per repository as the type the block names",
			commitType: "github.scaffold-commit",
			rows: []row{
				{"a repository carrying the topic", observe.Tagged{Type: "github.repository", ID: "acme/app-ci"}, true},
				{"any webhook on a repository carrying the topic", observe.Tagged{Type: "github.repository-webhook", ID: "acme/app-ci#hooks/11"}, true},
				{"a handed secret on a repository carrying the topic", observe.Tagged{Type: "github.actions-secret", ID: "acme/app-ci#DEPLOY_KEY"}, true},
				{"a handed variable on a repository carrying the topic", observe.Tagged{Type: "github.actions-variable", ID: "acme/app-ci#MITSTACK_APP_ID"}, true},
				{"the trailer commits on a repository carrying the topic", observe.Tagged{Type: "github.scaffold-commit", ID: "acme/app-ci"}, true},
				{"a webhook pointing at the processor URL on the bound repository", observe.Tagged{Type: "github.repository-webhook", ID: "acme/app#hooks/21"}, true},
				{"a handed secret on the bound repository", observe.Tagged{Type: "github.actions-secret", ID: "acme/app#DEPLOY_KEY"}, true},
				{"a handed variable on the bound repository", observe.Tagged{Type: "github.actions-variable", ID: "acme/app#MITSTACK_APP_REPO"}, true},
				{"the trailer commit on the bound repository", observe.Tagged{Type: "github.scaffold-commit", ID: "acme/app"}, true},
				{"a file a trailer commit wrote, which the commit type stands for", observe.Tagged{Type: "github.workflow-file", ID: "acme/app-ci/.github/workflows/build.yml"}, false},
				{"a trailer commit on a repository neither tagged nor bound", observe.Tagged{Type: "github.scaffold-commit", ID: "acme/untagged"}, false},
				{"a commit on another subscription's repository", observe.Tagged{Type: "github.scaffold-commit", ID: "acme/other-ci"}, false},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lister := ghLister(ghWorld())
			lister.CommitType = tc.commitType

			got, err := lister.ListTagged(context.Background(), ghDesired(), ghSub)

			if err != nil {
				t.Fatalf("ListTagged(%s): %v", ghSub, err)
			}
			wantListed := 0
			for _, r := range tc.rows {
				if r.listed {
					wantListed++
				}
				if n := countTagged(got, r.tagged); (r.listed && n != 1) || (!r.listed && n != 0) {
					t.Errorf("%s: %+v listed %d times, want listed=%v; ListTagged returned %+v", r.name, r.tagged, n, r.listed, got)
				}
			}
			if len(got) != wantListed {
				t.Errorf("ListTagged listed %d objects; want exactly the %d rows marked listed: %+v", len(got), wantListed, got)
			}
		})
	}
}

func TestGitHubTagged_AsksForTheSubscriptionsTopic(t *testing.T) {
	api := ghWorld()

	_, err := ghLister(api).ListTagged(context.Background(), ghDesired(), ghSub)

	if err != nil {
		t.Fatalf("ListTagged: %v", err)
	}
	if want := []string{"mitstack-sub-3e304e324312"}; !reflect.DeepEqual(api.asked, want) {
		t.Errorf("asked for repositories with topics %q; want %q", api.asked, want)
	}
}

func TestGitHubTagged_ReportsATaggedRepositoryAsTheTypeTheBlockNames(t *testing.T) {
	lister := ghLister(ghWorld())
	lister.RepositoryType = "github.gitops-repository"

	got, err := lister.ListTagged(context.Background(), ghDesired(), ghSub)

	if err != nil {
		t.Fatalf("ListTagged: %v", err)
	}
	if countTagged(got, observe.Tagged{Type: "github.gitops-repository", ID: "acme/app-ci"}) != 1 || countTagged(got, observe.Tagged{Type: "github.repository", ID: "acme/app-ci"}) != 0 {
		t.Errorf("with RepositoryType github.gitops-repository, ListTagged = %+v; want acme/app-ci reported once, as that type alone", got)
	}
}

func TestGitHubTagged_CountsNoBoundWebhookWithoutAProcessorURL(t *testing.T) {
	api := ghWorld()
	api.hooks["acme/app"] = append(api.hooks["acme/app"], observe.GitHubWebhook{ID: "23", URL: ""})
	lister := ghLister(api)
	lister.ProcessorURL = ""

	got, err := lister.ListTagged(context.Background(), ghDesired(), ghSub)

	if err != nil {
		t.Fatalf("ListTagged: %v", err)
	}
	for _, tg := range got {
		if tg.Type == "github.repository-webhook" && strings.HasPrefix(tg.ID, "acme/app#") {
			t.Errorf("with no ProcessorURL, ListTagged counted %+v on the bound repository; want no webhook there counted", tg)
		}
	}
}

func TestGitHubTagged_ReadsOnlyTaggedRepositoriesWithNoGitHubRepositoryRequirement(t *testing.T) {
	d := ghDesired()
	d.RequirementTypes = map[string]string{"runner": "github.self-hosted-runner"}

	got, err := ghLister(ghWorld()).ListTagged(context.Background(), d, ghSub)

	if err != nil {
		t.Fatalf("ListTagged: %v", err)
	}
	for _, tg := range got {
		if !strings.HasPrefix(tg.ID, "acme/app-ci") {
			t.Errorf("with no github.repository requirement, ListTagged listed %+v; want only what sits on acme/app-ci", tg)
		}
	}
}

func TestGitHubTagged_ReturnsTheAPIsError(t *testing.T) {
	boom := errors.New("github: rate limited")
	api := ghWorld()
	api.err = boom

	_, err := ghLister(api).ListTagged(context.Background(), ghDesired(), ghSub)

	if !errors.Is(err, boom) {
		t.Errorf("ListTagged error = %v; want the API's error", err)
	}
}

func TestGitHubTagged_RefusesAnEmptySubscriptionID(t *testing.T) {
	_, err := ghLister(ghWorld()).ListTagged(context.Background(), ghDesired(), "")

	if err == nil {
		t.Error(`ListTagged with subscription id "" returned no error; an empty id names no topic or trailer of its own`)
	}
}

func TestGitHubSubscriptionHelpers_FollowADR0085(t *testing.T) {
	cases := []struct {
		name, got, want string
	}{
		{"the topic turns each underscore into a hyphen", observe.GitHubSubscriptionTopic("sub_3e304e324312"), "mitstack-sub-3e304e324312"},
		{"the topic keeps the id's case", observe.GitHubSubscriptionTopic("sub_AB"), "mitstack-sub-AB"},
		{"the trailer names the id as issued", observe.GitHubSubscriptionTrailer("sub_3e304e324312"), "Mitstack-Subscription: sub_3e304e324312"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q; want %q", tc.name, tc.got, tc.want)
		}
	}
}
