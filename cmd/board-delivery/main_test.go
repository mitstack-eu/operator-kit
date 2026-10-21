package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// k writes each "#" in s as the board's key prefix, so "#12: fix" is a subject delivering key 12.
func k(s string) string { return strings.ReplaceAll(s, "#", "MIT-") }

func keys(ns ...int) []string {
	var out []string
	for _, n := range ns {
		out = append(out, fmt.Sprintf("MIT-%d", n))
	}
	return out
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// tagSHA returns what the board-synced tag points at in dir, or "" when it is unset.
func tagSHA(dir string) string {
	cmd := exec.Command("git", "rev-parse", "-q", "--verify", "refs/tags/"+anchorTag+"^{commit}")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// testRepo is a working copy plus a bare origin, since the board-synced tag only persists across CI runs once it reaches the remote.
type testRepo struct {
	dir, origin, seed string
	shas              []string
}

// newTestRepo commits a seed and then each "subject\x00body" message, pushing the seed to origin.
func newTestRepo(t *testing.T, messages ...string) *testRepo {
	t.Helper()
	r := &testRepo{dir: t.TempDir(), origin: t.TempDir()}
	runGit(t, r.origin, "init", "-q", "--bare")
	runGit(t, r.dir, "init", "-q")
	runGit(t, r.dir, "remote", "add", "origin", r.origin)
	for i, msg := range append([]string{"seed"}, messages...) {
		args := []string{"-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "--allow-empty", "-q"}
		for _, part := range strings.Split(k(msg), "\x00") {
			args = append(args, "-m", part)
		}
		runGit(t, r.dir, args...)
		sha := runGit(t, r.dir, "rev-parse", "HEAD")
		if i == 0 {
			r.seed = sha
			runGit(t, r.dir, "push", "-q", "origin", "HEAD:refs/heads/main")
			continue
		}
		r.shas = append(r.shas, sha)
	}
	return r
}

type capturedRequest struct {
	method, path string
	headers      http.Header
	body         []byte
}

type outcome struct {
	repo *testRepo
	reqs []capturedRequest
	out  string
	tip  string
}

const (
	noBoard     = 0
	unreachable = -1
)

func TestRun(t *testing.T) {
	const token = "super-secret-value-xyz"
	cases := []struct {
		name      string
		messages  []string
		tagAt     int
		before    func(r *testRepo) string
		status    int
		body      string
		token     string
		wantCode  int
		wantKeys  []string
		wantInOut string
		check     func(t *testing.T, o outcome)
	}{
		{"it posts exactly one JSON delivery of the tip with the bearer token", []string{"#42: fix the thing"}, 0, nil, 200, "ok", token, 0, keys(42), "",
			func(t *testing.T, o outcome) {
				r := o.reqs[0]
				got := []string{r.method, r.path, r.headers.Get("Content-Type"), r.headers.Get("Authorization")}
				if want := []string{http.MethodPost, "/v1/delivered", "application/json", "Bearer " + token}; !slices.Equal(got, want) {
					t.Errorf("method, path, content type, authorization = %q, want %q", got, want)
				}
				if d := delivered(t, r.body).Delivery; d != o.tip {
					t.Errorf("delivery = %q, want the tip %q", d, o.tip)
				}
			}},
		{"an empty token sends no Authorization header at all", []string{"#1: fix"}, 0, nil, 200, "ok", "", 0, keys(1), "",
			func(t *testing.T, o outcome) {
				if vals := o.reqs[0].headers.Values("Authorization"); len(vals) != 0 {
					t.Errorf("Authorization = %v, want no header when the token is empty", vals)
				}
			}},
		{"a push delivering no key records nothing and succeeds", []string{"chore: bump platform submodule to abc (#1092 automation)"}, 0, nil, 200, "ok", "", 0, nil, "recorded nothing", nil},
		{"a subject naming a key only in a refs group records nothing", []string{"operator-kit exports the host-cannot-run refusal class (refs #5141)"}, 0, nil, 200, "ok", "", 0, nil, "recorded nothing", nil},
		{"an unset TRACKER_BASE_URL never silently skips a real delivery", []string{"#1: fix"}, 0, nil, noBoard, "", "", 1, nil, "no board to record on", nil},
		{"an unset TRACKER_BASE_URL is moot when nothing would be recorded", []string{"chore: bump platform submodule to abc (#1092 automation)"}, 0, nil, noBoard, "", "", 0, nil, "recorded nothing", nil},
		{"a rejected record fails carrying the status and the body", []string{"#1: fix"}, 0, nil, 500, "boom-details", "", 1, keys(1), "board returned 500: boom-details", nil},
		{"a transport failure is a hard failure", []string{"#1: fix"}, 0, nil, unreachable, "", "", 1, nil, "posting delivery", nil},
		{"the token reaches the request but never the output", []string{"#1: fix"}, 0, nil, 401, "unauthorized", token, 1, keys(1), "",
			func(t *testing.T, o outcome) {
				if strings.Contains(o.out, token) {
					t.Errorf("output carries the bearer token: %q", o.out)
				}
				if auth := o.reqs[0].headers.Get("Authorization"); auth != "Bearer "+token {
					t.Errorf("Authorization = %q, want %q", auth, "Bearer "+token)
				}
			}},
		{"the range excludes the before commit itself", []string{"#1: one", "#2: two", "#3: three"}, 0,
			func(r *testRepo) string { return r.shas[0] }, 200, "ok", "", 0, keys(2, 3), "", nil},
		{"no before and no anchor deliver only the tip, never full history", []string{"#1: one", "#2: two"}, 0, nil, 200, "ok", "", 0, keys(2), "", nil},
		{"an all-zero before and no anchor deliver only the tip", []string{"#1: one", "#2: two"}, 0,
			func(*testRepo) string { return testZeroSHA }, 200, "ok", "", 0, keys(2), "", nil},
		{"keys merge across commits in numeric order", []string{"#20: two", "#9: nine"}, 0,
			func(r *testRepo) string { return r.seed }, 200, "ok", "", 0, keys(9, 20), "", nil},
		{"a key in one commit's subject and trailer is deduplicated", []string{"#5: fix\x00Jira: #5 #6"}, 0, nil, 200, "ok", "", 0, keys(5, 6), "", nil},
		{"the board-synced anchor wins over --before", []string{"#1: one", "#2: two", "#3: three"}, 1,
			func(r *testRepo) string { return r.seed }, 200, "ok", "", 0, keys(2, 3), "", nil},
		{"a successful write advances the anchor and pushes it to origin", []string{"#1: one"}, 0, nil, 200, "ok", "", 0, keys(1), "",
			func(t *testing.T, o outcome) {
				if local, origin := tagSHA(o.repo.dir), tagSHA(o.repo.origin); local != o.tip || origin != o.tip {
					t.Errorf("board-synced tag local %q, origin %q; want both at %q, since a run's own checkout is discarded", local, origin, o.tip)
				}
			}},
		{"a failed write leaves the anchor unset", []string{"#1: one"}, 0, nil, 500, "boom", "", 1, keys(1), "",
			func(t *testing.T, o outcome) {
				if local, origin := tagSHA(o.repo.dir), tagSHA(o.repo.origin); local != "" || origin != "" {
					t.Errorf("board-synced tag local %q, origin %q; want both unset after a failed write", local, origin)
				}
			}},
		{"a run with no board configured leaves the anchor unset", []string{"#1: one"}, 0, nil, noBoard, "", "", 1, nil, "",
			func(t *testing.T, o outcome) {
				if local := tagSHA(o.repo.dir); local != "" {
					t.Errorf("local board-synced tag = %q, want it unset when the run never wrote", local)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRepo(t, tc.messages...)
			if tc.tagAt > 0 {
				runGit(t, r.dir, "tag", "-f", anchorTag, r.shas[tc.tagAt-1])
			}
			o := outcome{repo: r, tip: r.shas[len(r.shas)-1]}
			baseURL := ""
			if tc.status != noBoard {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					raw, _ := io.ReadAll(req.Body)
					o.reqs = append(o.reqs, capturedRequest{req.Method, req.URL.Path, req.Header.Clone(), raw})
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.body)
				}))
				baseURL = srv.URL
				if tc.status == unreachable {
					srv.Close()
				} else {
					t.Cleanup(srv.Close)
				}
			}
			t.Setenv("TRACKER_BASE_URL", baseURL)
			t.Setenv("TRACKER_API_TOKEN", tc.token)
			args := []string{"--repo", r.dir, "--after", o.tip}
			if tc.before != nil {
				args = append(args, "--before", tc.before(r))
			}
			var buf bytes.Buffer

			code := Run(args, &buf)

			o.out = buf.String()
			if code != tc.wantCode || !strings.Contains(o.out, tc.wantInOut) {
				t.Fatalf("Run(%q) = %d, output %q; want %d, output carrying %q", args, code, o.out, tc.wantCode, tc.wantInOut)
			}
			wantReqs := 0
			if tc.wantKeys != nil {
				wantReqs = 1
			}
			if len(o.reqs) != wantReqs {
				t.Fatalf("server received %d requests, want %d", len(o.reqs), wantReqs)
			}
			if tc.wantKeys != nil {
				if got := delivered(t, o.reqs[0].body).Keys; !slices.Equal(got, tc.wantKeys) {
					t.Errorf("delivered keys = %v, want %v", got, tc.wantKeys)
				}
			}
			if tc.check != nil {
				tc.check(t, o)
			}
		})
	}
}

// deliveredPayload decodes the body the board receives independently of the type that encodes it.
type deliveredPayload struct {
	Delivery string   `json:"delivery"`
	Keys     []string `json:"keys"`
}

func delivered(t *testing.T, raw []byte) deliveredPayload {
	t.Helper()
	var p deliveredPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decoding delivered payload %s: %v", raw, err)
	}
	return p
}
