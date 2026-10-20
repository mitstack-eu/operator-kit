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
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/delivery"
)

const token = "sekrit-token-xyz-999"

var sha = strings.Repeat("f", 40)

func readGolden(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/delivery.golden.json")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	return b
}

func goldenRecord(t *testing.T) delivery.Record {
	t.Helper()
	var rec delivery.Record
	if err := json.Unmarshal(readGolden(t), &rec); err != nil {
		t.Fatalf("unmarshal golden fixture into Record: %v", err)
	}
	return rec
}

func TestRecord_GoldenFileRoundTripsByteForByte(t *testing.T) {
	golden := readGolden(t)

	out, err := json.Marshal(goldenRecord(t))

	if err != nil {
		t.Fatalf("marshal Record: %v", err)
	}
	if want := bytes.TrimRight(golden, "\n"); !bytes.Equal(out, want) {
		t.Errorf("round trip = %s\nwant        = %s", out, want)
	}
}

func TestRecord_GoldenFieldsDecodeAsExpected(t *testing.T) {
	healthy := "healthy"
	want := delivery.Record{
		AppID:       "app_delivery_1",
		SHA:         sha,
		Repo:        "acme/delivery-app",
		Verdict:     json.RawMessage(`{"repo":"acme/delivery-app","sha":"` + sha + `","gates":"pass"}`),
		DeployState: &healthy,
		BuildResult: &delivery.BuildResult{Conclusion: delivery.ConclusionSuccess, RunURL: "https://github.com/acme/delivery-app-ci/actions/runs/1"},
		Phase:       delivery.PhaseHealthy,
	}

	got := goldenRecord(t)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("golden record = %+v (deployState %v, buildResult %+v)\nwant %+v", got, *got.DeployState, *got.BuildResult, want)
	}
}

type seenRequest struct{ path, auth, accept string }

func TestClient_Get(t *testing.T) {
	golden := readGolden(t)
	cases := []struct {
		name       string
		baseSuffix string
		nilHTTP    bool
		status     int
		body       []byte
		check      func(t *testing.T, rec *delivery.Record, err error, req seenRequest)
	}{
		{"it requests the record's path with the bearer token and decodes the golden record", "", false, http.StatusOK, golden,
			func(t *testing.T, rec *delivery.Record, err error, req seenRequest) {
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				want := seenRequest{path: "/delivery/app_delivery_1/" + sha, auth: "Bearer " + token, accept: "application/json"}
				if req != want {
					t.Errorf("request = %+v, want %+v", req, want)
				}
				if wantRec := goldenRecord(t); !reflect.DeepEqual(*rec, wantRec) {
					t.Errorf("record = %+v, want %+v", *rec, wantRec)
				}
			}},
		{"a base with a trailing slash still requests exactly one slash", "/", false, http.StatusOK, golden,
			func(t *testing.T, _ *delivery.Record, err error, req seenRequest) {
				if want := "/delivery/app_delivery_1/" + sha; err != nil || req.path != want {
					t.Errorf("Get = %v, request path %q; want %q", err, req.path, want)
				}
			}},
		{"a nil http.Client falls back to the default client", "", true, http.StatusOK, golden,
			func(t *testing.T, rec *delivery.Record, err error, _ seenRequest) {
				if err != nil || rec.Phase != delivery.PhaseHealthy {
					t.Errorf("Get = %+v, %v; want phase %q", rec, err, delivery.PhaseHealthy)
				}
			}},
		{"a 404 wraps ErrNotFound", "", false, http.StatusNotFound, []byte(`{"error":"no registered build for app_delivery_1"}`),
			func(t *testing.T, _ *delivery.Record, err error, _ seenRequest) {
				if !errors.Is(err, delivery.ErrNotFound) {
					t.Errorf("err = %v, want errors.Is(err, delivery.ErrNotFound)", err)
				}
			}},
		{"any other error status is a StatusError carrying the body", "", false, http.StatusInternalServerError, []byte("internal error, please retry later"),
			func(t *testing.T, _ *delivery.Record, err error, _ seenRequest) {
				var statusErr *delivery.StatusError
				if !errors.As(err, &statusErr) {
					t.Fatalf("err = %v (%T), want a *delivery.StatusError", err, err)
				}
				want := delivery.StatusError{StatusCode: http.StatusInternalServerError, Body: "internal error, please retry later"}
				if *statusErr != want {
					t.Errorf("StatusError = %+v, want %+v", *statusErr, want)
				}
			}},
		{"a 200 whose body is not JSON is a decode error", "", false, http.StatusOK, []byte("not json"),
			func(t *testing.T, rec *delivery.Record, err error, _ seenRequest) {
				if err == nil {
					t.Errorf("Get = %+v with no error, want a decode error", rec)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req seenRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				req = seenRequest{path: r.URL.Path, auth: r.Header.Get("Authorization"), accept: r.Header.Get("Accept")}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			}))
			defer srv.Close()
			httpClient := srv.Client()
			if tc.nilHTTP {
				httpClient = nil
			}

			rec, err := delivery.NewClient(srv.URL+tc.baseSuffix, token, httpClient).Get(context.Background(), "app_delivery_1", sha)

			tc.check(t, rec, err, req)
			if err != nil && strings.Contains(err.Error(), token) {
				t.Errorf("err.Error() leaked the bearer token: %q", err)
			}
		})
	}
}

func TestBuildResult_FieldsAndConstants(t *testing.T) {
	br := delivery.BuildResult{Conclusion: delivery.ConclusionFailure, RunURL: "https://example.test/run/9"}
	phases := []string{
		delivery.PhaseBuilding, delivery.PhaseBuildFailed, delivery.PhaseGatesFailed,
		delivery.PhaseDeploying, delivery.PhaseHealthy, delivery.PhaseRolledBack,
	}

	out, err := json.Marshal(br)

	if want := `{"conclusion":"failure","runUrl":"https://example.test/run/9"}`; err != nil || string(out) != want {
		t.Errorf("json.Marshal(%+v) = %s, %v; want %s", br, out, err, want)
	}
	seen := map[string]bool{}
	for _, p := range phases {
		if p == "" || seen[p] {
			t.Errorf("phase constant %q is empty or not unique among %q", p, phases)
		}
		seen[p] = true
	}
}
