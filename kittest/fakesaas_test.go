package kittest_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// The 418 for an unmatched request must never echo a header value it received, since that could leak a bearer token.

// fakeTB is a minimal testing.TB double that lets a test assert LoadSaaSFixtures
// fails loudly (t.Fatalf) without taking the whole test binary down. It embeds
// the (nil) testing.TB interface purely so the unexported private() method is
// promoted and the struct compiles as a testing.TB; every method the loader is
// allowed to call is overridden below.
type fakeTB struct {
	testing.TB

	mu     sync.Mutex
	failed bool
	logs   []string
}

func (f *fakeTB) Helper()                         {}
func (f *fakeTB) Name() string                    { return "fakeTB" }
func (f *fakeTB) Cleanup(func())                  {}
func (f *fakeTB) Log(args ...any)                 { f.record(fmt.Sprint(args...)) }
func (f *fakeTB) Logf(format string, args ...any) { f.record(fmt.Sprintf(format, args...)) }

func (f *fakeTB) Error(args ...any)                 { f.fail(fmt.Sprint(args...)) }
func (f *fakeTB) Errorf(format string, args ...any) { f.fail(fmt.Sprintf(format, args...)) }

func (f *fakeTB) Fatal(args ...any) {
	f.fail(fmt.Sprint(args...))
	runtime.Goexit()
}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.fail(fmt.Sprintf(format, args...))
	runtime.Goexit()
}

func (f *fakeTB) fail(msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = true
	f.logs = append(f.logs, msg)
}
func (f *fakeTB) record(msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, msg)
}

// runAndCollect runs fn against a fresh fakeTB on its own goroutine (so a
// Fatalf-triggered runtime.Goexit only unwinds that goroutine) and returns the
// fakeTB once fn has returned or exited.
func runAndCollect(fn func(tb testing.TB)) *fakeTB {
	f := &fakeTB{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(f)
	}()
	<-done
	return f
}

// --- Fixture loading ---------------------------------------------------

func TestLoadSaaSFixtures_LoadsInFilenameOrder(t *testing.T) {
	fixtures := kittest.LoadSaaSFixtures(t, "testdata/fakesaas/loadorder")

	if len(fixtures) != 3 {
		t.Fatalf("got %d fixtures, want 3: %+v", len(fixtures), fixtures)
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, name := range want {
		if fixtures[i].Name != name {
			t.Errorf("fixtures[%d].Name = %q, want %q — filenames are 01-alpha.json, 02-bravo.json, "+
				"03-charlie.json, so loading must be stable filename order", i, fixtures[i].Name, name)
		}
	}
}

func TestLoadSaaSFixtures_UnknownFieldFails(t *testing.T) {
	got := runAndCollect(func(tb testing.TB) {
		kittest.LoadSaaSFixtures(tb, "testdata/fakesaas/unknown-field")
	})
	if !got.failed {
		t.Fatal("LoadSaaSFixtures did not fail on a fixture with an unrecognised top-level field — " +
			"the loader must strict-decode (unknown fields = fatal), or a typo'd fixture silently no-ops forever")
	}
}

func TestLoadSaaSFixtures_MissingRequiredFieldsFail(t *testing.T) {
	cases := []string{
		"testdata/fakesaas/missing-method",
		"testdata/fakesaas/missing-path",
		"testdata/fakesaas/missing-status",
	}
	for _, dir := range cases {
		dir := dir
		t.Run(dir, func(t *testing.T) {
			got := runAndCollect(func(tb testing.TB) {
				kittest.LoadSaaSFixtures(tb, dir)
			})
			if !got.failed {
				t.Fatalf("LoadSaaSFixtures did not fail loading %s, which is missing a required field", dir)
			}
		})
	}
}

func TestLoadSaaSFixtures_EmptyDirFails(t *testing.T) {
	dir := t.TempDir()
	got := runAndCollect(func(tb testing.TB) {
		kittest.LoadSaaSFixtures(tb, dir)
	})
	if !got.failed {
		t.Fatal("LoadSaaSFixtures did not fail on an empty directory — a provider with zero fixtures is " +
			"exactly the condition the upstream lint gate treats as no-fake-coverage, so the loader must refuse it")
	}
}

// --- Matching ------------------------------------------------------------

func TestFakeSaaS_MatchesMethodAndPath(t *testing.T) {
	f := kittest.NewFakeSaaS(t, []kittest.SaaSFixture{
		{
			Name:     "get-status",
			Request:  kittest.SaaSFixtureRequest{Method: http.MethodGet, Path: "/v1/status"},
			Response: kittest.SaaSFixtureResponse{Status: http.StatusOK, Body: json.RawMessage(`{"ok":true}`)},
		},
	})

	resp, err := http.Get(f.URL + "/v1/status")
	if err != nil {
		t.Fatalf("GET /v1/status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 for a matching method+path", resp.StatusCode)
	}

	// Same path, wrong method: must NOT match.
	resp2, err := http.Post(f.URL+"/v1/status", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST /v1/status: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusTeapot {
		t.Errorf("POST to a GET-only fixture's path: status = %d, want 418 (no match)", resp2.StatusCode)
	}

	// Same method, wrong path: must NOT match.
	resp3, err := http.Get(f.URL + "/v1/other")
	if err != nil {
		t.Fatalf("GET /v1/other: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusTeapot {
		t.Errorf("GET to an unmodelled path: status = %d, want 418 (no match)", resp3.StatusCode)
	}
}

func TestFakeSaaS_MatchesExactHeaderValue(t *testing.T) {
	f := kittest.NewFakeSaaS(t, []kittest.SaaSFixture{
		{
			Name: "with-api-key",
			Request: kittest.SaaSFixtureRequest{
				Method:  http.MethodGet,
				Path:    "/v1/data",
				Headers: map[string]string{"X-Api-Key": "correct-key"},
			},
			Response: kittest.SaaSFixtureResponse{Status: http.StatusOK},
		},
	})

	do := func(headerVal string, setHeader bool) int {
		req, _ := http.NewRequest(http.MethodGet, f.URL+"/v1/data", nil)
		if setHeader {
			req.Header.Set("X-Api-Key", headerVal)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if got := do("correct-key", true); got != http.StatusOK {
		t.Errorf("exact matching header value: status = %d, want 200", got)
	}
	if got := do("wrong-key", true); got != http.StatusTeapot {
		t.Errorf("wrong header value: status = %d, want 418 (no match)", got)
	}
	if got := do("", false); got != http.StatusTeapot {
		t.Errorf("missing required header: status = %d, want 418 (no match)", got)
	}
}

func TestFakeSaaS_MatchesHeaderPresenceWildcard(t *testing.T) {
	f := kittest.NewFakeSaaS(t, []kittest.SaaSFixture{
		{
			Name: "any-bearer",
			Request: kittest.SaaSFixtureRequest{
				Method:  http.MethodGet,
				Path:    "/v1/whoami",
				Headers: map[string]string{"Authorization": "*"},
			},
			Response: kittest.SaaSFixtureResponse{Status: http.StatusOK},
		},
	})

	req, _ := http.NewRequest(http.MethodGet, f.URL+"/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer literally-anything-nonempty")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("wildcard header present: status = %d, want 200 — \"*\" means presence-only, any value matches", resp.StatusCode)
	}

	req2, _ := http.NewRequest(http.MethodGet, f.URL+"/v1/whoami", nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusTeapot {
		t.Errorf("wildcard header absent: status = %d, want 418 (no match)", resp2.StatusCode)
	}
}

func TestFakeSaaS_FirstMatchWins(t *testing.T) {
	// Two fixtures share method+path. The first requires nothing and so
	// matches unconditionally; the second requires a header that this test's
	// request WILL carry. First-match-wins must still pick the first one.
	f := kittest.NewFakeSaaS(t, []kittest.SaaSFixture{
		{
			Name:     "broad-first",
			Request:  kittest.SaaSFixtureRequest{Method: http.MethodGet, Path: "/v1/thing"},
			Response: kittest.SaaSFixtureResponse{Status: http.StatusOK, Body: json.RawMessage(`{"which":"broad"}`)},
		},
		{
			Name: "narrow-second",
			Request: kittest.SaaSFixtureRequest{
				Method:  http.MethodGet,
				Path:    "/v1/thing",
				Headers: map[string]string{"X-Flag": "on"},
			},
			Response: kittest.SaaSFixtureResponse{Status: http.StatusCreated, Body: json.RawMessage(`{"which":"narrow"}`)},
		},
	})

	req, _ := http.NewRequest(http.MethodGet, f.URL+"/v1/thing", nil)
	req.Header.Set("X-Flag", "on") // would ALSO satisfy the second fixture
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (the first-listed fixture) — first-match-wins, not best-match", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "broad") {
		t.Errorf("body = %s, want the broad-first fixture's body — a request satisfying both fixtures must "+
			"still be served by whichever is listed first", body)
	}
}

func TestFakeSaaS_QueryStringIsSignificant(t *testing.T) {
	f := kittest.NewFakeSaaS(t, []kittest.SaaSFixture{
		{
			Name:     "page-2",
			Request:  kittest.SaaSFixtureRequest{Method: http.MethodGet, Path: "/v1/items?page=2"},
			Response: kittest.SaaSFixtureResponse{Status: http.StatusOK},
		},
	})

	if resp, err := http.Get(f.URL + "/v1/items?page=2"); err != nil {
		t.Fatalf("get: %v", err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("exact query string: status = %d, want 200", resp.StatusCode)
		}
	}

	if resp, err := http.Get(f.URL + "/v1/items"); err != nil {
		t.Fatalf("get: %v", err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTeapot {
			t.Errorf("missing query string: status = %d, want 418 (no match) — the query string is significant", resp.StatusCode)
		}
	}

	if resp, err := http.Get(f.URL + "/v1/items?page=3"); err != nil {
		t.Fatalf("get: %v", err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTeapot {
			t.Errorf("different query string: status = %d, want 418 (no match)", resp.StatusCode)
		}
	}
}

// --- Responses -------------------------------------------------------------

func TestFakeSaaS_ServesResponseVerbatim(t *testing.T) {
	f := kittest.NewFakeSaaS(t, []kittest.SaaSFixture{
		{
			Name:    "created",
			Request: kittest.SaaSFixtureRequest{Method: http.MethodPost, Path: "/v1/widgets"},
			Response: kittest.SaaSFixtureResponse{
				Status:  http.StatusCreated,
				Headers: map[string]string{"X-Custom-Header": "custom-value"},
				Body:    json.RawMessage(`{"id":"abc123","nested":{"a":1}}`),
			},
		},
	})

	resp, err := http.Post(f.URL+"/v1/widgets", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Custom-Header"); got != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want %q", got, "custom-value")
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var got, want any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("response body is not valid JSON: %v\nbody: %s", err, raw)
	}
	_ = json.Unmarshal([]byte(`{"id":"abc123","nested":{"a":1}}`), &want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v (fixture body must be served verbatim)", got, want)
	}
}

// --- 418 no-match: named + leak-safe ---------------------------------------

func TestFakeSaaS_NoMatchNames418Body(t *testing.T) {
	f := kittest.NewFakeSaaS(t, []kittest.SaaSFixture{
		{
			Name:     "list-repos",
			Request:  kittest.SaaSFixtureRequest{Method: http.MethodGet, Path: "/repos"},
			Response: kittest.SaaSFixtureResponse{Status: http.StatusOK},
		},
		{
			Name:     "get-repo",
			Request:  kittest.SaaSFixtureRequest{Method: http.MethodGet, Path: "/repos/acme"},
			Response: kittest.SaaSFixtureResponse{Status: http.StatusOK},
		},
	})

	resp, err := http.Get(f.URL + "/totally/unmodelled")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", resp.StatusCode)
	}

	var got struct {
		Method        string   `json:"method"`
		Path          string   `json:"path"`
		FixturesTried []string `json:"fixturesTried"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("418 body is not the expected JSON shape: %v\nbody: %s", err, raw)
	}
	if got.Method != http.MethodGet {
		t.Errorf("body.method = %q, want GET", got.Method)
	}
	if got.Path != "/totally/unmodelled" {
		t.Errorf("body.path = %q, want /totally/unmodelled", got.Path)
	}
	want := []string{"list-repos", "get-repo"}
	sort.Strings(got.FixturesTried)
	sort.Strings(want)
	if !reflect.DeepEqual(got.FixturesTried, want) {
		t.Errorf("body.fixturesTried = %v, want %v (every fixture name tried)", got.FixturesTried, want)
	}
}

func TestFakeSaaS_NoMatchNeverLeaksHeaderValues(t *testing.T) {
	f := kittest.NewFakeSaaS(t, []kittest.SaaSFixture{
		{
			Name:     "unrelated",
			Request:  kittest.SaaSFixtureRequest{Method: http.MethodGet, Path: "/v1/other"},
			Response: kittest.SaaSFixtureResponse{Status: http.StatusOK},
		},
	})

	const secretToken = "super-secret-value"
	const canaryKey = "sk-canary"

	req, _ := http.NewRequest(http.MethodGet, f.URL+"/does/not/match", nil)
	req.Header.Set("Authorization", "Bearer "+secretToken)
	req.Header.Set("DD-API-KEY", canaryKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	kittest.AssertNoSecret(t, "418 response body", string(body), secretToken, canaryKey)

	var headerDump strings.Builder
	for k, vs := range resp.Header {
		headerDump.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
	}
	kittest.AssertNoSecret(t, "418 response headers", headerDump.String(), secretToken, canaryKey)

	callsDump := fmt.Sprintf("%+v", f.Calls())
	kittest.AssertNoSecret(t, "recorded Calls()", callsDump, secretToken, canaryKey)
}

// --- Call recording ----------------------------------------------------

func TestFakeSaaS_CallsRecordsSequenceWithHeaderNamesOnly(t *testing.T) {
	f := kittest.NewFakeSaaS(t, []kittest.SaaSFixture{
		{
			Name:     "a",
			Request:  kittest.SaaSFixtureRequest{Method: http.MethodGet, Path: "/v1/a"},
			Response: kittest.SaaSFixtureResponse{Status: http.StatusOK},
		},
		{
			Name:     "b",
			Request:  kittest.SaaSFixtureRequest{Method: http.MethodPost, Path: "/v1/b"},
			Response: kittest.SaaSFixtureResponse{Status: http.StatusOK},
		},
	})

	req1, _ := http.NewRequest(http.MethodGet, f.URL+"/v1/a", nil)
	req1.Header.Set("X-One", "value-one")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp1.Body.Close()

	req2, _ := http.NewRequest(http.MethodPost, f.URL+"/v1/b", strings.NewReader("{}"))
	req2.Header.Set("X-Two", "value-two")
	req2.Header.Set("X-Three", "value-three")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp2.Body.Close()

	calls := f.Calls()
	if len(calls) != 2 {
		t.Fatalf("len(Calls()) = %d, want 2: %+v", len(calls), calls)
	}

	if calls[0].Method != http.MethodGet || calls[0].Path != "/v1/a" {
		t.Errorf("calls[0] = %+v, want Method=GET Path=/v1/a", calls[0])
	}
	if calls[1].Method != http.MethodPost || calls[1].Path != "/v1/b" {
		t.Errorf("calls[1] = %+v, want Method=POST Path=/v1/b", calls[1])
	}

	if !containsHeaderName(calls[0].HeaderNames, "X-One") {
		t.Errorf("calls[0].HeaderNames = %v, want to contain X-One", calls[0].HeaderNames)
	}
	if !containsHeaderName(calls[1].HeaderNames, "X-Two") || !containsHeaderName(calls[1].HeaderNames, "X-Three") {
		t.Errorf("calls[1].HeaderNames = %v, want to contain X-Two and X-Three", calls[1].HeaderNames)
	}

	// The value must never ride along — only the NAME. Guard against a
	// RecordedSaaSCall that starts carrying values later.
	dump := fmt.Sprintf("%+v", calls)
	kittest.AssertNoSecret(t, "Calls()", dump, "value-one", "value-two", "value-three")
}

func containsHeaderName(names []string, want string) bool {
	for _, n := range names {
		if strings.EqualFold(n, want) {
			return true
		}
	}
	return false
}

// --- Realistic end-to-end: a provider verify probe -------------------------

func TestFakeSaaS_EndToEndVerifyProbe_DistinguishesVerifiedFromRejected(t *testing.T) {
	// Fixtures model a typical SaaS "verify credential" endpoint: the SAME
	// method+path, but the response differs by the bearer token presented —
	// exactly the shape an operator's TestConnect_Verified /
	// TestConnect_Rejected pair needs to drive against a real client.
	fixtures := kittest.LoadSaaSFixtures(t, "testdata/fakesaas/verify-probe")
	f := kittest.NewFakeSaaS(t, fixtures)

	verify := func(token string) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost, f.URL+"/v1/verify", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("verify(%q): %v", token, err)
		}
		defer resp.Body.Close()
		var body map[string]any
		raw, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(raw, &body)
		return resp.StatusCode, body
	}

	status, body := verify("good-token")
	if status != http.StatusOK {
		t.Errorf("verify(good-token): status = %d, want 200", status)
	}
	if body["status"] != "verified" {
		t.Errorf("verify(good-token): body = %v, want status=verified", body)
	}

	status2, body2 := verify("bad-token")
	if status2 != http.StatusUnauthorized {
		t.Errorf("verify(bad-token): status = %d, want 401", status2)
	}
	if body2["status"] != "rejected" {
		t.Errorf("verify(bad-token): body = %v, want status=rejected", body2)
	}

	calls := f.Calls()
	if len(calls) != 2 {
		t.Fatalf("len(Calls()) = %d, want 2 (both probes recorded): %+v", len(calls), calls)
	}
}
