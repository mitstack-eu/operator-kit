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

// fakeTB records failures and logs without ending the test binary; it embeds a nil testing.TB only to satisfy the interface's unexported method.
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

// runAndCollect runs fn on its own goroutine, so a Fatalf's runtime.Goexit unwinds only that goroutine.
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

func TestLoadSaaSFixtures_LoadsInFilenameOrder(t *testing.T) {
	fixtures := kittest.LoadSaaSFixtures(t, "testdata/fakesaas/loadorder")

	var names []string
	for _, f := range fixtures {
		names = append(names, f.Name)
	}
	if want := []string{"alpha", "bravo", "charlie"}; !reflect.DeepEqual(names, want) {
		t.Errorf("fixture names = %q, want %q, the stable order of 01-alpha.json, 02-bravo.json and 03-charlie.json", names, want)
	}
}

func TestLoadSaaSFixtures_FailsLoudly(t *testing.T) {
	for _, tc := range []struct{ name, dir string }{
		{"an unrecognised top-level field, so a typo'd fixture never silently no-ops", "testdata/fakesaas/unknown-field"},
		{"a fixture missing its method", "testdata/fakesaas/missing-method"},
		{"a fixture missing its path", "testdata/fakesaas/missing-path"},
		{"a fixture missing its status", "testdata/fakesaas/missing-status"},
		{"an empty directory, which the lint gate treats as no fake coverage", t.TempDir()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runAndCollect(func(tb testing.TB) { kittest.LoadSaaSFixtures(tb, tc.dir) })

			if !got.failed {
				t.Errorf("LoadSaaSFixtures(%s) did not fail", tc.dir)
			}
		})
	}
}

func fixture(name, method, path string, headers map[string]string, resp kittest.SaaSFixtureResponse) kittest.SaaSFixture {
	return kittest.SaaSFixture{Name: name, Request: kittest.SaaSFixtureRequest{Method: method, Path: path, Headers: headers}, Response: resp}
}

// send makes one request to f and returns its status, headers and body.
func send(t *testing.T, f *kittest.FakeSaaS, method, path string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, f.URL+path, strings.NewReader("{}"))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

func TestFakeSaaS_Matching(t *testing.T) {
	ok := kittest.SaaSFixtureResponse{Status: http.StatusOK}
	status := []kittest.SaaSFixture{fixture("get-status", http.MethodGet, "/v1/status", nil, kittest.SaaSFixtureResponse{Status: http.StatusOK, Body: json.RawMessage(`{"ok":true}`)})}
	keyed := []kittest.SaaSFixture{fixture("with-api-key", http.MethodGet, "/v1/data", map[string]string{"X-Api-Key": "correct-key"}, ok)}
	anyBearer := []kittest.SaaSFixture{fixture("any-bearer", http.MethodGet, "/v1/whoami", map[string]string{"Authorization": "*"}, ok)}
	// The broad fixture matches unconditionally and the narrow one also matches the request below.
	overlapping := []kittest.SaaSFixture{
		fixture("broad-first", http.MethodGet, "/v1/thing", nil, kittest.SaaSFixtureResponse{Status: http.StatusOK, Body: json.RawMessage(`{"which":"broad"}`)}),
		fixture("narrow-second", http.MethodGet, "/v1/thing", map[string]string{"X-Flag": "on"}, kittest.SaaSFixtureResponse{Status: http.StatusCreated, Body: json.RawMessage(`{"which":"narrow"}`)}),
	}
	page2 := []kittest.SaaSFixture{fixture("page-2", http.MethodGet, "/v1/items?page=2", nil, ok)}
	created := []kittest.SaaSFixture{fixture("created", http.MethodPost, "/v1/widgets", nil, kittest.SaaSFixtureResponse{
		Status: http.StatusCreated, Headers: map[string]string{"X-Custom-Header": "custom-value"}, Body: json.RawMessage(`{"id":"abc123","nested":{"a":1}}`),
	})}
	cases := []struct {
		name         string
		fixtures     []kittest.SaaSFixture
		method, path string
		headers      map[string]string
		wantStatus   int
		wantBody     string
		wantHeader   [2]string
	}{
		{"a matching method and path is served", status, http.MethodGet, "/v1/status", nil, 200, `{"ok":true}`, [2]string{}},
		{"the same path under another method is no match", status, http.MethodPost, "/v1/status", nil, 418, "", [2]string{}},
		{"the same method on an unmodelled path is no match", status, http.MethodGet, "/v1/other", nil, 418, "", [2]string{}},
		{"an exactly matching header value is served", keyed, http.MethodGet, "/v1/data", map[string]string{"X-Api-Key": "correct-key"}, 200, "", [2]string{}},
		{"a wrong header value is no match", keyed, http.MethodGet, "/v1/data", map[string]string{"X-Api-Key": "wrong-key"}, 418, "", [2]string{}},
		{"a missing required header is no match", keyed, http.MethodGet, "/v1/data", nil, 418, "", [2]string{}},
		{"a wildcard header matches any present value", anyBearer, http.MethodGet, "/v1/whoami", map[string]string{"Authorization": "Bearer literally-anything-nonempty"}, 200, "", [2]string{}},
		{"a wildcard header absent is no match", anyBearer, http.MethodGet, "/v1/whoami", nil, 418, "", [2]string{}},
		{"the first listed fixture wins, not the best match", overlapping, http.MethodGet, "/v1/thing", map[string]string{"X-Flag": "on"}, 200, `{"which":"broad"}`, [2]string{}},
		{"an exact query string is served", page2, http.MethodGet, "/v1/items?page=2", nil, 200, "", [2]string{}},
		{"a missing query string is no match, since it is significant", page2, http.MethodGet, "/v1/items", nil, 418, "", [2]string{}},
		{"a different query string is no match", page2, http.MethodGet, "/v1/items?page=3", nil, 418, "", [2]string{}},
		{"the response's status, headers and body are served verbatim", created, http.MethodPost, "/v1/widgets", nil, 201, `{"id":"abc123","nested":{"a":1}}`,
			[2]string{"X-Custom-Header", "custom-value"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := kittest.NewFakeSaaS(t, tc.fixtures)

			status, header, body := send(t, f, tc.method, tc.path, tc.headers)

			if status != tc.wantStatus {
				t.Errorf("%s %s = %d, want %d", tc.method, tc.path, status, tc.wantStatus)
			}
			if tc.wantBody != "" {
				var got, want any
				if err := json.Unmarshal(body, &got); err != nil || json.Unmarshal([]byte(tc.wantBody), &want) != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("body = %s (%v), want %s", body, err, tc.wantBody)
				}
			}
			if tc.wantHeader[0] != "" && header.Get(tc.wantHeader[0]) != tc.wantHeader[1] {
				t.Errorf("%s = %q, want %q", tc.wantHeader[0], header.Get(tc.wantHeader[0]), tc.wantHeader[1])
			}
		})
	}
}

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

func TestFakeSaaS_EndToEndVerifyProbe_DistinguishesVerifiedFromRejected(t *testing.T) {
	// One method and path whose answer differs by the bearer token, the shape a verified and rejected connect pair drives.
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
