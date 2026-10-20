package ghapi_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/ghapi"
)

const (
	revRepo    = "acme/gitops"
	shaRev1    = "ef663767a3d60390831d0518763e341592dcbcb3"
	shaRev2    = "ec31b1e77e397c40f195af031457e9f5ac78ddb4"
	shaParent  = "edfab415e1ed0dd187f07d1ba43f677bdbbbc67e"
	shaUnknown = "4a88c839bf17a3e423b10408b6c056b2dd61d950"
	sha256Rev  = "f807e527f163f8bc44c4b002454a7f0236e520c97386e8c31a2924c81cc26d22"
)

type file struct {
	content []byte
	ok      bool
}

type commitInfo struct {
	parent string
	paths  []string
}

func TestHeadCommit_AndReadFileAt(t *testing.T) {
	const content = "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: app-alpha\n  annotations:\n    mitstack.dev/cluster: k3s-fsn1\n"
	head := func(c *ghapi.Client) (any, error) { return c.HeadCommit(context.Background(), revRepo) }
	read := func(path string) func(c *ghapi.Client) (any, error) {
		return func(c *ghapi.Client) (any, error) {
			b, ok, err := c.ReadFileAt(context.Background(), revRepo, shaRev1, path)
			return file{b, ok}, err
		}
	}
	mainRef := get(gitops + "/git/ref/heads/main")
	contents := func(escaped string) string { return get(gitops + "/contents/" + escaped + "?ref=" + shaRev1) }
	runAPICases(t, []apiCase{
		{name: "HeadCommit returns the commit main points at", routes: map[string][]reply{mainRef: {{body: `{"ref":"refs/heads/main","object":{"sha":"tip-sha","type":"commit"}}`}}},
			call: head, want: "tip-sha", requests: []string{mainRef}},
		{name: "HeadCommit is empty when the repository has no main yet", routes: map[string][]reply{mainRef: {notFound}},
			call: head, want: "", requests: []string{mainRef}},
		{name: "HeadCommit fails on any other status", routes: map[string][]reply{mainRef: {{http.StatusInternalServerError, `{"message":"failure for {auth}"}`}}},
			call: head, want: "", err: errAny, errHas: "returned 500", requests: []string{mainRef}},
		{name: "ReadFileAt returns the decoded content at the revision",
			routes: map[string][]reply{contents("apps/app-alpha/namespace.yaml"): {{body: `{"type":"file","encoding":"base64","content":` + quote(wrappedBase64(content)) + `}`}}},
			call:   read("apps/app-alpha/namespace.yaml"), want: file{[]byte(content), true}, requests: []string{contents("apps/app-alpha/namespace.yaml")}},
		{name: "ReadFileAt reads a 404 as an absent path and no error", routes: map[string][]reply{},
			call: read("apps/app-alpha/gone.yaml"), want: file{}, requests: []string{contents("apps/app-alpha/gone.yaml")}},
		{name: "ReadFileAt fails on any other status", routes: map[string][]reply{contents("a.yaml"): {{http.StatusForbidden, `{"message":"failure for {auth}"}`}}},
			call: read("a.yaml"), want: file{}, err: errAny, errHas: "returned 403", requests: []string{contents("a.yaml")}},
		{name: "ReadFileAt escapes each path segment so none is cut off or turned into a query",
			routes: map[string][]reply{contents("apps/my%20app/a%23b%3Fc=d.yaml"): {{body: `{"type":"file","encoding":"base64","content":"eA=="}`}}},
			call:   read("apps/my app/a#b?c=d.yaml"), want: file{[]byte("x"), true}, requests: []string{contents("apps/my%20app/a%23b%3Fc=d.yaml")}},
	})
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, "\n", `\n`) + `"` }

func TestCommitChanges(t *testing.T) {
	parentCommit, trees, commits := get(gitops+"/git/commits/"+shaParent), post(gitops+"/git/trees"), post(gitops+"/git/commits")
	update, create := patch(gitops+"/git/refs/heads/main"), post(gitops+"/git/refs")
	onParent := func(extra map[string][]reply) map[string][]reply {
		routes := map[string][]reply{parentCommit: {{body: `{"tree":{"sha":"parent-tree"}}`}}, trees: {{http.StatusCreated, `{"sha":"new-tree-sha"}`}}, commits: {{http.StatusCreated, `{"sha":"new-commit-sha"}`}}}
		for k, v := range extra {
			routes[k] = v
		}
		return routes
	}
	onto := func(parent string, changes map[string]*string) func(c *ghapi.Client) (any, error) {
		return func(c *ghapi.Client) (any, error) {
			return c.CommitChanges(context.Background(), revRepo, parent, "app-alpha: update", changes)
		}
	}
	one := map[string]*string{"a.yaml": strp("a\n")}
	notFastForward := reply{http.StatusUnprocessableEntity, `{"message":"Update is not a fast forward","documentation_url":"https://docs.github.com/rest/git/refs#update-a-reference"}`}
	wantBodies := func(want map[string]string) func(t *testing.T, f *fakeGitHub) {
		return func(t *testing.T, f *fakeGitHub) {
			for key, raw := range want {
				if got := f.bodies(key); len(got) != 1 || !reflect.DeepEqual(got[0], jsonBody(t, raw)) {
					t.Errorf("%s bodies = %v, want once %s", key, got, raw)
				}
			}
		}
	}
	runAPICases(t, []apiCase{
		{name: "it lands one fast-forward commit on the parent, writing and deleting paths", routes: onParent(map[string][]reply{update: {{}}}),
			call: onto(shaParent, map[string]*string{"apps/app-alpha/deploy.yaml": strp("image: v2\n"), "apps/app-alpha/old.yaml": nil}),
			want: "new-commit-sha", requests: []string{parentCommit, trees, commits, update},
			check: wantBodies(map[string]string{
				trees: `{"base_tree":"parent-tree","tree":[{"path":"apps/app-alpha/deploy.yaml","mode":"100644","type":"blob","content":"image: v2\n"},` +
					`{"path":"apps/app-alpha/old.yaml","mode":"100644","type":"blob","sha":null}]}`,
				commits: `{"message":"app-alpha: update","tree":"new-tree-sha","parents":["` + shaParent + `"]}`,
				update:  `{"sha":"new-commit-sha"}`,
			})},
		{name: "an empty parent creates main", routes: onParent(map[string][]reply{create: {{status: http.StatusCreated}}}),
			call: onto("", one), want: "new-commit-sha", requests: []string{trees, commits, create},
			check: wantBodies(map[string]string{
				trees:   `{"tree":[{"path":"a.yaml","mode":"100644","type":"blob","content":"a\n"}]}`,
				commits: `{"message":"app-alpha: update","tree":"new-tree-sha","parents":[]}`,
				create:  `{"ref":"refs/heads/main","sha":"new-commit-sha"}`,
			})},
		{name: "an unchanged tree makes no commit and returns the parent", routes: onParent(map[string][]reply{trees: {{http.StatusCreated, `{"sha":"parent-tree"}`}}}),
			call: onto(shaParent, one), want: shaParent, requests: []string{parentCommit, trees}},
		{name: "a parent that is no longer the tip wraps ErrNotFastForward and does not replay", routes: onParent(map[string][]reply{update: {notFastForward}}),
			call: onto(shaParent, one), want: "new-commit-sha", err: ghapi.ErrNotFastForward, requests: []string{parentCommit, trees, commits, update}},
		{name: "an empty parent when main exists wraps ErrNotFastForward and never moves main",
			routes: onParent(map[string][]reply{create: {{http.StatusUnprocessableEntity, `{"message":"Reference already exists"}`}}}),
			call:   onto("", one), want: "new-commit-sha", err: ghapi.ErrNotFastForward, requests: []string{trees, commits, create}},
		{name: "another refusal is an error but not ErrNotFastForward", routes: onParent(map[string][]reply{update: {{http.StatusUnprocessableEntity, `{"message":"Object does not exist"}`}}}),
			call: onto(shaParent, one), want: "new-commit-sha", err: errAny, notErr: ghapi.ErrNotFastForward, requests: []string{parentCommit, trees, commits, update}},
		{name: "an unknown parent is an error and moves no ref", routes: onParent(nil),
			call: onto(shaUnknown, one), want: "", err: errAny, errHas: "returned 404", requests: []string{get(gitops + "/git/commits/" + shaUnknown)}},
		{name: "a fast forward refusal echoing the token never carries it", routes: onParent(map[string][]reply{update: {{http.StatusUnprocessableEntity, `{"message":"Update is not a fast forward for {auth}"}`}}}),
			call: onto(shaParent, one), want: "new-commit-sha", err: ghapi.ErrNotFastForward, requests: []string{parentCommit, trees, commits, update}},
		{name: "a server error echoing the token never carries it", routes: onParent(map[string][]reply{update: {{http.StatusInternalServerError, `{"message":"boom {auth}"}`}}}),
			call: onto(shaParent, one), want: "new-commit-sha", err: errAny, errHas: "returned 500", requests: []string{parentCommit, trees, commits, update}},
		{name: "a tree refusal echoing the token never carries it", routes: onParent(map[string][]reply{trees: {{http.StatusUnprocessableEntity, `{"message":"failure for {auth}"}`}}}),
			call: onto(shaParent, one), want: "", err: errAny, errHas: "returned 422", requests: []string{parentCommit, trees}},
	})
}

func TestCommitParentAndPaths(t *testing.T) {
	commitOf := func(rev string) string { return get(gitops + "/commits/" + rev) }
	of := func(rev string) func(c *ghapi.Client) (any, error) {
		return func(c *ghapi.Client) (any, error) {
			parent, paths, err := c.CommitParentAndPaths(context.Background(), revRepo, rev)
			return commitInfo{parent, paths}, err
		}
	}
	runAPICases(t, []apiCase{
		{name: "it returns the first parent and the sorted paths", routes: map[string][]reply{commitOf(shaRev2): {{body: `{"parents":[{"sha":"rev-1"},{"sha":"merged-sha"}],` +
			`"files":[{"filename":"b.yaml","status":"modified"},{"filename":"apps/app-alpha/z.yaml","status":"added"},{"filename":"a.yaml","status":"removed"}]}`}}},
			call: of(shaRev2), want: commitInfo{"rev-1", []string{"a.yaml", "apps/app-alpha/z.yaml", "b.yaml"}}, requests: []string{commitOf(shaRev2)}},
		{name: "the parent of a root commit is empty", routes: map[string][]reply{commitOf(shaRev1): {{body: `{"parents":[],"files":[{"filename":"a.yaml","status":"added"}]}`}}},
			call: of(shaRev1), want: commitInfo{"", []string{"a.yaml"}}, requests: []string{commitOf(shaRev1)}},
		{name: "a rename lists both its paths", routes: map[string][]reply{commitOf(shaRev2): {{body: `{"parents":[{"sha":"rev-1"}],` +
			`"files":[{"filename":"apps/app-alpha/new.yaml","status":"renamed","previous_filename":"apps/app-alpha/old.yaml"}]}`}}},
			call: of(shaRev2), want: commitInfo{"rev-1", []string{"apps/app-alpha/new.yaml", "apps/app-alpha/old.yaml"}}, requests: []string{commitOf(shaRev2)}},
		{name: "a revision the repository never held is an error on a 422", routes: map[string][]reply{commitOf(shaUnknown): {{http.StatusUnprocessableEntity, `{"message":"No commit found for SHA: no-such-sha"}`}}},
			call: of(shaUnknown), want: commitInfo{}, err: errAny, errHas: "returned 422", requests: []string{commitOf(shaUnknown)}},
		{name: "a revision the repository never held is an error on a 404", routes: map[string][]reply{commitOf(shaUnknown): {notFound}},
			call: of(shaUnknown), want: commitInfo{}, err: errAny, errHas: "returned 404", requests: []string{commitOf(shaUnknown)}},
		{name: "a server error echoing the token never carries it", routes: map[string][]reply{commitOf(shaRev2): {{http.StatusInternalServerError, `{"message":"failure for {auth}"}`}}},
			call: of(shaRev2), want: commitInfo{}, err: errAny, errHas: "returned 500", requests: []string{commitOf(shaRev2)}},
	})
}

// revisionCalls are the calls taking a repository and a revision, each called with them.
var revisionCalls = map[string]func(c *ghapi.Client, repo, rev string) error{
	"ReadFileAt": func(c *ghapi.Client, repo, rev string) error {
		_, _, err := c.ReadFileAt(context.Background(), repo, rev, "a.yaml")
		return err
	},
	"CommitTree": func(c *ghapi.Client, repo, rev string) error {
		_, err := c.CommitTree(context.Background(), repo, rev)
		return err
	},
	"CommitParentAndPaths": func(c *ghapi.Client, repo, rev string) error {
		_, _, err := c.CommitParentAndPaths(context.Background(), repo, rev)
		return err
	},
	"CommitChanges": func(c *ghapi.Client, repo, rev string) error {
		_, err := c.CommitChanges(context.Background(), repo, rev, "m", map[string]*string{"a.yaml": strp("a\n")})
		return err
	},
	"HeadCommit": func(c *ghapi.Client, repo, _ string) error {
		_, err := c.HeadCommit(context.Background(), repo)
		return err
	},
}

// url.PathEscape leaves ".." as it is, so a call refuses anything but a safe repository and a full lowercase hex SHA before any request.
func TestRevisionCalls_RefuseAnUnsafeRepositoryOrRevisionBeforeAnyRequest(t *testing.T) {
	anything := &reply{body: `{"sha":"` + shaRev2 + `","tree":{"sha":"` + shaRev2 + `"},"object":{"sha":"` + shaRev2 + `"},"parents":[],"files":[],"type":"file","encoding":"base64","content":""}`}
	invalidRevisions := map[string]string{
		"dot-dot": "..", "dot": ".", "empty": "", "a branch name": "main", "a short hex prefix": "abc",
		"39 hex digits": shaRev1[:39], "41 hex digits": shaRev1 + "a", "an uppercase SHA": strings.ToUpper(shaRev1),
		"40 digits with a non-hex letter": "g" + shaRev1[1:], "a query injection": "rev-1&ref=evil",
		"a path climb": shaRev1 + "/../../orgs", "a climb with a query": "x/../../orgs?y",
	}
	type probe struct {
		name, repo, rev string
		refused         bool
	}
	var probes []probe
	for name := range revisionCalls {
		for _, repo := range []string{"acme", "acme/gitops/extra", "../gitops", "acme/..", "evil.com/x?y"} {
			if name != "CommitTree" {
				probes = append(probes, probe{name, repo, shaRev1, true})
			}
		}
		for label, rev := range invalidRevisions {
			if name != "HeadCommit" && !(name == "CommitChanges" && rev == "") {
				probes = append(probes, probe{name + "/" + label, revRepo, rev, true})
			}
		}
		for _, rev := range []string{shaRev1, sha256Rev} {
			if name != "HeadCommit" {
				probes = append(probes, probe{name + "/" + rev, revRepo, rev, false})
			}
		}
	}
	for _, p := range probes {
		t.Run(p.name+" "+p.repo, func(t *testing.T) {
			f := &fakeGitHub{fallback: anything}
			call := revisionCalls[strings.SplitN(p.name, "/", 2)[0]]
			if call == nil {
				call = revisionCalls[p.name]
			}

			err := call(serve(t, f), p.repo, p.rev)

			if n := len(f.keys()); p.refused && (err == nil || n != 0) {
				t.Errorf("%s(%q, %q) = %v after %d request(s), want refused before any request", p.name, p.repo, p.rev, err, n)
			} else if !p.refused && n == 0 {
				t.Errorf("%s(%q, %q) made no request, want a full SHA accepted", p.name, p.repo, p.rev)
			}
		})
	}
}
