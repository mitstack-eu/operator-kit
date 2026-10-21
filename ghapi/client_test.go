package ghapi_test

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/ghapi"
)

func TestEnsureRepo_AndDeleteRepo(t *testing.T) {
	ensure := func(c *ghapi.Client) (any, error) { return c.EnsureRepo(context.Background(), "acme", "widget") }
	remove := func(c *ghapi.Client) (any, error) { return nil, c.DeleteRepo(context.Background(), "acme", "widget") }
	runAPICases(t, []apiCase{
		{name: "an existing repo makes no create call", routes: map[string][]reply{get(widget): {{}}},
			call: ensure, want: "acme/widget", requests: []string{get(widget)}},
		{name: "a missing repo is created once", routes: map[string][]reply{get(widget): {notFound}, post("/orgs/acme/repos"): {{http.StatusCreated, `{"full_name":"acme/widget"}`}}},
			call: ensure, want: "acme/widget", requests: []string{get(widget), post("/orgs/acme/repos")}},
		{name: "a create refused with a body echoing the token fails without carrying it",
			routes: map[string][]reply{get(widget): {notFound}, post("/orgs/acme/repos"): {{http.StatusUnprocessableEntity, `{"message":"bad credentials","auth":"{auth}"}`}}},
			call:   ensure, want: "", err: errAny, errHas: "returned 422", requests: []string{get(widget), post("/orgs/acme/repos")}},
		{name: "deleting an absent repo succeeds, since a 404 is a finished teardown", routes: map[string][]reply{"DELETE " + widget: {notFound}},
			call: remove, requests: []string{"DELETE " + widget}},
		{name: "a real delete failure is surfaced, never swallowed as a teardown",
			routes: map[string][]reply{"DELETE " + widget: {{http.StatusForbidden, `{"message":"insufficient permissions"}`}}},
			call:   remove, err: errAny, errHas: "returned 403", requests: []string{"DELETE " + widget}},
	})
}

func TestSetVariable(t *testing.T) {
	const variables = widget + "/actions/variables"
	set := func(name string) func(c *ghapi.Client) (any, error) {
		return func(c *ghapi.Client) (any, error) {
			return nil, c.SetVariable(context.Background(), "acme/widget", name, "value")
		}
	}
	cases := []apiCase{
		{name: "a second call updates the variable the first created", routes: map[string][]reply{
			post(variables): {{status: http.StatusCreated}, {status: http.StatusConflict}}, patch(variables + "/MITSTACK_DISPATCH_URL"): {{status: http.StatusNoContent}}},
			call: func(c *ghapi.Client) (any, error) {
				if err := c.SetVariable(context.Background(), "acme/widget", "MITSTACK_DISPATCH_URL", "https://cicd.mitstack.dev"); err != nil {
					return nil, err
				}
				return nil, c.SetVariable(context.Background(), "acme/widget", "MITSTACK_DISPATCH_URL", "https://cicd.mitstack.dev/v2")
			},
			requests: []string{post(variables), post(variables), patch(variables + "/MITSTACK_DISPATCH_URL")}},
		{name: "an already-exists 422 falls back to a PATCH", routes: map[string][]reply{
			post(variables): {{http.StatusUnprocessableEntity, `{"message":"variable already exists"}`}}, patch(variables + "/MITSTACK_VAR"): {{status: http.StatusNoContent}}},
			call: set("MITSTACK_VAR"), requests: []string{post(variables), patch(variables + "/MITSTACK_VAR")}},
		{name: "an invalid-name 422 fails without reaching the PATCH path", routes: map[string][]reply{
			post(variables): {{http.StatusUnprocessableEntity, `{"message":"name is not valid. It can only contain alphanumeric characters and underscores"}`}}},
			call: set("MITSTACK_VAR"), err: errAny, errHas: "returned 422", requests: []string{post(variables)}},
		{name: "an unparseable 422 fails safe rather than guessing already exists", routes: map[string][]reply{post(variables): {{http.StatusUnprocessableEntity, `not json at all`}}},
			call: set("MITSTACK_VAR"), err: errAny, errHas: "returned 422", requests: []string{post(variables)}},
		{name: "an upstream failure echoing the token fails without carrying it", routes: map[string][]reply{post(variables): {{http.StatusInternalServerError, `{"message":"upstream failure","request_token":"{auth}"}`}}},
			call: set("X"), err: errAny, errHas: "returned 500", requests: []string{post(variables)}},
	}
	for _, name := range []string{"MITSTACK_VAR", "_leading_underscore", "a1", "lower_case_123"} {
		cases = append(cases, apiCase{name: "the valid name " + name + " reaches the create endpoint",
			routes: map[string][]reply{post(variables): {{status: http.StatusCreated}}}, call: set(name), requests: []string{post(variables)}})
	}
	for _, name := range []string{"a/b", "../etc", "a%2fb", "1abc", ""} {
		cases = append(cases, apiCase{name: fmt.Sprintf("the malformed name %q is refused before any request", name),
			call: set(name), err: errAny, errHas: "invalid variable name"})
	}
	runAPICases(t, cases)
}

const fastForwardRefusal = `{"message":"Update is not a fast forward"}`

// attempt is the requests of one CommitFiles try onto main at generation gen, up to its ref update.
func attempt(gen int) []string {
	return []string{get(widget + "/git/ref/heads/main"), get(fmt.Sprintf(widget+"/git/commits/main-sha-gen-%d", gen)),
		post(widget + "/git/trees"), post(widget + "/git/commits"), patch(widget + "/git/refs/heads/main")}
}

// movingMain scripts a main that moves to the next generation each time a ref update is refused with refusal, for gens generations.
func movingMain(gens int, refusals []reply) map[string][]reply {
	routes := map[string][]reply{post(widget + "/git/commits"): {{http.StatusCreated, `{"sha":"new-commit-sha"}`}}}
	for gen := 0; gen < gens; gen++ {
		routes[get(widget+"/git/ref/heads/main")] = append(routes[get(widget+"/git/ref/heads/main")], reply{body: fmt.Sprintf(`{"object":{"sha":"main-sha-gen-%d"}}`, gen)})
		routes[get(fmt.Sprintf(widget+"/git/commits/main-sha-gen-%d", gen))] = []reply{{body: fmt.Sprintf(`{"tree":{"sha":"main-tree-gen-%d"}}`, gen)}}
		routes[post(widget+"/git/trees")] = append(routes[post(widget+"/git/trees")], reply{http.StatusCreated, fmt.Sprintf(`{"sha":"tree-over-main-tree-gen-%d"}`, gen)})
	}
	routes[patch(widget+"/git/refs/heads/main")] = append(refusals, reply{body: `{"ref":"refs/heads/main"}`})
	return routes
}

func repeat(r reply, n int) []reply {
	out := make([]reply, n)
	for i := range out {
		out[i] = r
	}
	return out
}

func TestCommitFiles(t *testing.T) {
	files := map[string]string{"a.txt": "alpha", "b/c.txt": "charlie"}
	commit := func(c *ghapi.Client) (any, error) {
		return nil, c.CommitFiles(context.Background(), "acme/widget", "seed", files)
	}
	race := reply{http.StatusUnprocessableEntity, fastForwardRefusal}
	ref, base, trees := get(widget+"/git/ref/heads/main"), get(widget+"/git/commits/base-sha"), post(widget+"/git/trees")
	onBase := map[string][]reply{ref: {{body: `{"object":{"sha":"base-sha"}}`}}, base: {{body: `{"tree":{"sha":"tree-sha"}}`}}}
	with := func(extra map[string][]reply) map[string][]reply {
		out := map[string][]reply{}
		for k, v := range onBase {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	twoAttempts := slices.Concat(attempt(0), attempt(1))
	cases := []apiCase{
		{name: "an unchanged tree makes no commit and moves no ref", routes: with(map[string][]reply{trees: {{body: `{"sha":"tree-sha"}`}}}),
			call: commit, requests: []string{ref, base, trees}},
		{name: "a changed tree commits and moves the ref once", routes: with(map[string][]reply{trees: {{body: `{"sha":"new-tree-sha"}`}},
			post(widget + "/git/commits"): {{body: `{"sha":"new-commit-sha"}`}}, patch(widget + "/git/refs/heads/main"): {{}}}),
			call: commit, requests: []string{ref, base, trees, post(widget + "/git/commits"), patch(widget + "/git/refs/heads/main")}},
		{name: "a refused tree on an empty repo echoing the token fails without carrying it",
			routes: map[string][]reply{ref: {notFound}, trees: {{http.StatusUnprocessableEntity, `{"message":"rejected for token {auth}"}`}}},
			call:   commit, err: errAny, errHas: "returned 422", requests: []string{ref, trees}},
		{name: "an oversized ref lookup is refused by a bounded decode before the commit lookup",
			routes: with(map[string][]reply{ref: {{body: huge(`"object":{"sha":"base-sha"}`)}}}), call: commit, err: errAny, errHas: "decode ref", requests: []string{ref}},
		{name: "an oversized ref lookup echoing the token fails without carrying it",
			routes: with(map[string][]reply{ref: {{body: huge(`"object":{"sha":"base-sha"},"auth":"{auth}"`)}}}), call: commit, err: errAny, errHas: "decode ref", requests: []string{ref}},
		{name: "an oversized commit lookup is refused by a bounded decode before any tree is built",
			routes: with(map[string][]reply{base: {{body: huge(`"tree":{"sha":"tree-sha"}`)}}}), call: commit, err: errAny, errHas: "decode commit", requests: []string{ref, base}},
		{name: "one lost fast forward race replays the same entries onto the new head", routes: movingMain(2, []reply{race}), call: commit, requests: twoAttempts,
			check: func(t *testing.T, f *fakeGitHub) {
				tb, cb := f.bodies(trees), f.bodies(post(widget+"/git/commits"))
				if tb[0]["base_tree"] != "main-tree-gen-0" || tb[1]["base_tree"] != "main-tree-gen-1" || !reflect.DeepEqual(tb[0]["tree"], tb[1]["tree"]) {
					t.Errorf("tree POSTs = %v, want the same entries over main-tree-gen-0 then main-tree-gen-1", tb)
				}
				if !reflect.DeepEqual(cb[0]["parents"], []any{"main-sha-gen-0"}) || !reflect.DeepEqual(cb[1]["parents"], []any{"main-sha-gen-1"}) || cb[1]["tree"] != "tree-over-main-tree-gen-1" {
					t.Errorf("commit POSTs = %v, want parents gen-0 then exactly the new head gen-1 over the rebuilt tree", cb)
				}
				for i, p := range f.bodies(patch(widget + "/git/refs/heads/main")) {
					if p["sha"] != "new-commit-sha" {
						t.Errorf("PATCH #%d = %v, want sha new-commit-sha", i+1, p)
					}
				}
			}},
		{name: "the fast forward match is case-insensitive", routes: movingMain(2, []reply{{http.StatusUnprocessableEntity, `{"message":"UPDATE IS NOT A FAST FORWARD"}`}}),
			call: commit, requests: twoAttempts},
		{name: "a main that never stops moving fails after three ref updates", routes: movingMain(3, repeat(race, 3)),
			call: commit, err: errAny, errHas: "fast forward", requests: slices.Concat(twoAttempts, attempt(2))},
		{name: "the exhausted-replay error never carries the token", routes: movingMain(3, repeat(reply{http.StatusUnprocessableEntity, `{"message":"Update is not a fast forward","auth":"{auth}"}`}, 3)),
			call: commit, err: errAny, errHas: "fast forward", requests: slices.Concat(twoAttempts, attempt(2))},
		{name: "a protected-branch 422 fails at once", routes: movingMain(1, []reply{{http.StatusUnprocessableEntity, `{"message":"Required status check \"build\" is expected.","auth":"{auth}"}`}}),
			call: commit, err: errAny, errHas: "returned 422", requests: attempt(0)},
		{name: "a validation-failed 422 fails at once", routes: movingMain(1, []reply{{http.StatusUnprocessableEntity, `{"message":"Validation Failed","errors":[{"resource":"Ref","code":"custom","field":"sha"}],"auth":"{auth}"}`}}),
			call: commit, err: errAny, errHas: "returned 422", requests: attempt(0)},
		{name: "a 500 fails at once", routes: movingMain(1, []reply{{http.StatusInternalServerError, `{"message":"Server Error","auth":"{auth}"}`}}),
			call: commit, err: errAny, errHas: "returned 500", requests: attempt(0)},
		{name: "an empty repo creates the ref once and never updates it", routes: map[string][]reply{ref: {notFound}, trees: {{http.StatusCreated, `{"sha":"t"}`}},
			post(widget + "/git/commits"): {{http.StatusCreated, `{"sha":"new-commit-sha"}`}}, post(widget + "/git/refs"): {{status: http.StatusCreated}}},
			call: commit, requests: []string{ref, trees, post(widget + "/git/commits"), post(widget + "/git/refs")},
			check: func(t *testing.T, f *fakeGitHub) {
				_, hasBase := f.bodies(trees)[0]["base_tree"]
				_, hasParents := f.bodies(post(widget + "/git/commits"))[0]["parents"]
				if refs := f.bodies(post(widget + "/git/refs")); hasBase || hasParents || !reflect.DeepEqual(refs[0], jsonBody(t, `{"ref":"refs/heads/main","sha":"new-commit-sha"}`)) {
					t.Errorf("base_tree %v, parents %v, ref POST %v; want neither and refs/heads/main at new-commit-sha", hasBase, hasParents, refs)
				}
			}},
		{name: "main vanishing mid-replay errors without creating a ref", routes: func() map[string][]reply {
			r := movingMain(1, []reply{race})
			r[ref] = append(r[ref], notFound)
			return r
		}(), call: commit, err: errAny, errHas: "disappeared", requests: slices.Concat(attempt(0), []string{ref})},
		{name: "a replay whose rebuilt tree equals the new head's tree is a no-op", routes: func() map[string][]reply {
			r := movingMain(2, []reply{race})
			r[trees] = []reply{{http.StatusCreated, `{"sha":"tree-over-main-tree-gen-0"}`}, {http.StatusCreated, `{"sha":"main-tree-gen-1"}`}}
			return r
		}(), call: commit, requests: slices.Concat(attempt(0), attempt(1)[:3]),
			check: func(t *testing.T, f *fakeGitHub) {
				if got := f.bodies(trees)[1]["base_tree"]; got != "main-tree-gen-1" {
					t.Errorf("replayed tree base_tree = %v, want main-tree-gen-1: the replay rebuilds before deciding it is a no-op", got)
				}
			}},
		{name: "a replay never touches another repository", routes: func() map[string][]reply {
			r := movingMain(2, []reply{race})
			r[get("/repos/acme/other/git/ref/heads/main")] = []reply{{body: `{"object":{"sha":"main-sha-gen-7"}}`}}
			return r
		}(), call: commit, requests: twoAttempts},
	}
	runAPICases(t, cases)
}
