// Package delivery_test pins the contract against the golden file shared with the dispatcher's tests, so editing either side alone breaks a test.
package delivery_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/delivery"
)

func readGolden(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/delivery.golden.json")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	return b
}

func compactJSON(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	return buf.Bytes()
}

func TestRecord_GoldenFileRoundTripsByteForByte(t *testing.T) {
	golden := readGolden(t)

	var rec delivery.Record
	if err := json.Unmarshal(golden, &rec); err != nil {
		t.Fatalf("unmarshal golden fixture into Record: %v", err)
	}

	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal Record: %v", err)
	}

	want := bytes.TrimRight(golden, "\n")
	if !bytes.Equal(out, want) {
		t.Errorf("round trip = %s\nwant        = %s", out, want)
	}
}

func TestRecord_GoldenFieldsDecodeAsExpected(t *testing.T) {
	golden := readGolden(t)
	var rec delivery.Record
	if err := json.Unmarshal(golden, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if rec.AppID != "app_delivery_1" {
		t.Errorf("AppID = %q, want app_delivery_1", rec.AppID)
	}
	if rec.SHA != strings.Repeat("f", 40) {
		t.Errorf("SHA = %q, want 40 f's", rec.SHA)
	}
	if rec.Repo != "acme/delivery-app" {
		t.Errorf("Repo = %q", rec.Repo)
	}
	if rec.DeployState == nil || *rec.DeployState != "healthy" {
		t.Errorf("DeployState = %v, want a non-nil pointer to \"healthy\"", rec.DeployState)
	}
	if rec.BuildResult == nil {
		t.Fatal("BuildResult = nil, want a populated *BuildResult")
	}
	if rec.BuildResult.Conclusion != delivery.ConclusionSuccess {
		t.Errorf("BuildResult.Conclusion = %q, want %q", rec.BuildResult.Conclusion, delivery.ConclusionSuccess)
	}
	if rec.BuildResult.RunURL != "https://github.com/acme/delivery-app-ci/actions/runs/1" {
		t.Errorf("BuildResult.RunURL = %q", rec.BuildResult.RunURL)
	}
	if rec.Phase != delivery.PhaseHealthy {
		t.Errorf("Phase = %q, want %q", rec.Phase, delivery.PhaseHealthy)
	}

	wantVerdict := compactJSON(t, `{"repo":"acme/delivery-app","sha":"ffffffffffffffffffffffffffffffffffffffff","gates":"pass"}`)
	if !bytes.Equal([]byte(rec.Verdict), wantVerdict) {
		t.Errorf("Verdict = %s, want %s", rec.Verdict, wantVerdict)
	}
}

func TestClient_Get_RequestsTheRightPathAndHeaders(t *testing.T) {
	golden := readGolden(t)
	const token = "sekrit-token-xyz-999"

	var gotPath, gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(golden)
	}))
	defer srv.Close()

	c := delivery.NewClient(srv.URL, token, srv.Client())
	rec, err := c.Get(context.Background(), "app_delivery_1", strings.Repeat("f", 40))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if gotPath != "/delivery/app_delivery_1/"+strings.Repeat("f", 40) {
		t.Errorf("request path = %q", gotPath)
	}
	if gotAuth != "Bearer "+token {
		t.Errorf("Authorization header = %q, want Bearer %s", gotAuth, token)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept header = %q, want application/json", gotAccept)
	}

	if rec.Phase != delivery.PhaseHealthy {
		t.Errorf("Phase = %q, want %q", rec.Phase, delivery.PhaseHealthy)
	}
	if rec.DeployState == nil || *rec.DeployState != "healthy" {
		t.Errorf("DeployState = %v, want \"healthy\"", rec.DeployState)
	}
	if rec.BuildResult == nil || rec.BuildResult.Conclusion != delivery.ConclusionSuccess {
		t.Errorf("BuildResult = %+v, want Conclusion %q", rec.BuildResult, delivery.ConclusionSuccess)
	}
	wantVerdict := compactJSON(t, `{"repo":"acme/delivery-app","sha":"ffffffffffffffffffffffffffffffffffffffff","gates":"pass"}`)
	if !bytes.Equal([]byte(rec.Verdict), wantVerdict) {
		t.Errorf("Verdict = %s, want %s", rec.Verdict, wantVerdict)
	}
}

func TestClient_Get_ABaseWithATrailingSlashStillRequestsExactlyOneSlash(t *testing.T) {
	golden := readGolden(t)
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(golden)
	}))
	defer srv.Close()

	c := delivery.NewClient(srv.URL+"/", "tok", srv.Client())
	if _, err := c.Get(context.Background(), "app_delivery_1", strings.Repeat("f", 40)); err != nil {
		t.Fatalf("Get: %v", err)
	}

	want := "/delivery/app_delivery_1/" + strings.Repeat("f", 40)
	if gotPath != want {
		t.Errorf("request path = %q, want %q (no double slash from a trailing-slash Base)", gotPath, want)
	}
}

func TestClient_Get_ANilHTTPClientFallsBackToDefaultClient(t *testing.T) {
	golden := readGolden(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(golden)
	}))
	defer srv.Close()

	c := delivery.NewClient(srv.URL, "tok", nil)
	rec, err := c.Get(context.Background(), "app_delivery_1", strings.Repeat("f", 40))
	if err != nil {
		t.Fatalf("Get with a nil http.Client (must fall back to http.DefaultClient): %v", err)
	}
	if rec.Phase != delivery.PhaseHealthy {
		t.Errorf("Phase = %q, want %q", rec.Phase, delivery.PhaseHealthy)
	}
}

func TestClient_Get_404ReturnsAnErrorSatisfyingErrNotFound(t *testing.T) {
	const token = "sekrit-token-xyz-404"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no registered build for app_never_registered"}`))
	}))
	defer srv.Close()

	c := delivery.NewClient(srv.URL, token, srv.Client())
	_, err := c.Get(context.Background(), "app_never_registered", strings.Repeat("f", 40))
	if err == nil {
		t.Fatal("Get on a 404 returned a nil error")
	}
	if !errors.Is(err, delivery.ErrNotFound) {
		t.Errorf("err = %v, want errors.Is(err, delivery.ErrNotFound)", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("err.Error() leaked the bearer token: %q", err.Error())
	}
}

func TestClient_Get_ANon404ErrorStatusReturnsAStatusErrorCarryingTheBody(t *testing.T) {
	const token = "sekrit-token-xyz-500"
	const body = "internal error, please retry later"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := delivery.NewClient(srv.URL, token, srv.Client())
	_, err := c.Get(context.Background(), "app_delivery_1", strings.Repeat("f", 40))
	if err == nil {
		t.Fatal("Get on a 500 returned a nil error")
	}

	var statusErr *delivery.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err = %v (%T), want errors.As to find a *delivery.StatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusError.StatusCode = %d, want %d", statusErr.StatusCode, http.StatusInternalServerError)
	}
	if statusErr.Body != body {
		t.Errorf("StatusError.Body = %q, want %q", statusErr.Body, body)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("err.Error() leaked the bearer token: %q", err.Error())
	}
}

func TestClient_Get_A200WithANonJSONBodyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := delivery.NewClient(srv.URL, "tok", srv.Client())
	_, err := c.Get(context.Background(), "app_delivery_1", strings.Repeat("f", 40))
	if err == nil {
		t.Fatal("Get with a 200 non-JSON body returned a nil error, want a decode error")
	}
}

func TestBuildResult_FieldsAndConstants(t *testing.T) {
	br := delivery.BuildResult{Conclusion: delivery.ConclusionFailure, RunURL: "https://example.test/run/9"}
	out, err := json.Marshal(br)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"conclusion":"failure","runUrl":"https://example.test/run/9"}`
	if string(out) != want {
		t.Errorf("BuildResult json = %s, want %s", out, want)
	}

	phases := []string{
		delivery.PhaseBuilding, delivery.PhaseBuildFailed, delivery.PhaseGatesFailed,
		delivery.PhaseDeploying, delivery.PhaseHealthy, delivery.PhaseRolledBack,
	}
	seen := map[string]bool{}
	for _, p := range phases {
		if p == "" {
			t.Errorf("a phase constant is empty")
		}
		if seen[p] {
			t.Errorf("phase constant %q is not unique", p)
		}
		seen[p] = true
	}
}
