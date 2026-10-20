package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/provenance"
)

func runBlockprov(t *testing.T, args ...string) (stdout string, exitCode int) {
	t.Helper()
	var buf bytes.Buffer
	exitCode = Run(args, &buf)
	return buf.String(), exitCode
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

// signTestToken duplicates provenance_test.go's helper because a test in package main cannot import provenance_test.
func signTestToken(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]interface{}) string {
	t.Helper()
	header := map[string]interface{}{"alg": "RS256", "kid": kid, "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshalling header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	hashed := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func mintServer(t *testing.T, repository, sha string) (url string, resolve provenance.KeyResolver) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test RSA key: %v", err)
	}
	const kid = "test-blockprov-mint-kid"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aud := r.URL.Query().Get("audience")
		if aud == "" {
			http.Error(w, "missing audience", http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		token := signTestToken(t, priv, kid, map[string]interface{}{
			"iss":        provenance.Issuer,
			"aud":        aud,
			"repository": repository,
			"sha":        sha,
			"iat":        now.Unix(),
			"exp":        now.Add(10 * time.Minute).Unix(),
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"value": token})
	}))
	t.Cleanup(srv.Close)

	return srv.URL, func(k string) (*rsa.PublicKey, bool) {
		if k == kid {
			return &priv.PublicKey, true
		}
		return nil, false
	}
}

const (
	testRepository = "mitstack-eu/sample-block"
	testCommit     = "0123456789abcdef0123456789abcdef01234567"
)

func buildArgs(t *testing.T, dir, tokenURL, outPath string) []string {
	t.Helper()
	manifestPath := writeFile(t, dir, "manifest.yaml", "apiVersion: mitstack/v1alpha\nkind: Block\nmetadata:\n  id: sample-block\n")
	return []string{
		"build",
		"--manifest", manifestPath,
		"--block-id", "sample-block",
		"--block-version", "1.0.0",
		"--image-ref", "123456789012.dkr.ecr.eu-central-1.amazonaws.com/mitstack-sample-block@sha256:" + strings.Repeat("a", 64),
		"--image-digest", "sha256:" + strings.Repeat("a", 64),
		"--kit-version", "v0.1.0",
		"--repository", testRepository,
		"--commit", testCommit,
		"--run-url", "https://github.com/mitstack-eu/sample-block/actions/runs/1",
		"--token-url", tokenURL,
		"--token-request-token", "test-request-token",
		"--out", outPath,
	}
}

const (
	passReport = `{"suiteVersion":"1.0.0","kitVersion":"v0.1.0","mode":"standalone","manifestDigest":"sha256:aa","verdict":"pass","resultsDigest":"sha256:bb","suites":[]}`
	failReport = `{"suiteVersion":"1.0.0","kitVersion":"v0.1.0","mode":"standalone","manifestDigest":"sha256:aa","verdict":"fail","resultsDigest":"sha256:bb","suites":[]}`
)

func TestRun_Build_FailedReportExitsNonZeroAndWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "record.json")
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("blockprov build minted a token for a report whose verdict is not pass")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer tokenSrv.Close()
	args := append(buildArgs(t, dir, tokenSrv.URL, outPath), "--report", writeFile(t, dir, "report.json", failReport))

	stdout, exit := runBlockprov(t, args...)

	if exit == 0 {
		t.Fatalf("exit = 0 for a failed blockverify report, want non-zero\nstdout:\n%s", stdout)
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Errorf("os.Stat(record) = %v, want no record written for a report that is not a pass", err)
	}
}

func TestRun_Build_PassingReportWritesARecordProvenanceVerifyAccepts(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "record.json")
	tokenURL, resolve := mintServer(t, testRepository, testCommit)
	args := append(buildArgs(t, dir, tokenURL, outPath), "--report", writeFile(t, dir, "report.json", passReport))

	stdout, exit := runBlockprov(t, args...)

	if exit != 0 {
		t.Fatalf("exit = %d for a passing report, want 0\nstdout:\n%s", exit, stdout)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading written record: %v", err)
	}
	var rec provenance.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("written record is not valid JSON: %v", err)
	}
	if err := provenance.Verify(rec, resolve, provenance.Expect{Repository: testRepository}); err != nil {
		t.Errorf("provenance.Verify on the written record: %v", err)
	}
	if got, want := [2]string{rec.Payload.Conformance.RunURL, rec.Payload.SourceRepository}, [2]string{"https://github.com/mitstack-eu/sample-block/actions/runs/1", testRepository}; got != want {
		t.Errorf("runUrl, sourceRepository = %q, want the flags' values %q", got, want)
	}
}

// verify goes through provenance.Decode, because json.Unmarshal drops an attacker-added field and the record would be accepted.
func TestRun_Verify(t *testing.T) {
	for _, tc := range []struct {
		name, record, wantLine string
		wantExit               int
	}{
		{"the committed golden is accepted", "record.golden.json", "blockprov verify: record accepted", 0},
		{"another identity is rejected naming the mismatch", "invalid-other-identity.json", "blockprov verify: record rejected: provenance: token identity does not match the expected repository", 1},
		{"an unknown payload field is rejected", "invalid-unknown-field.json", "blockprov verify: record is not valid: provenance: record contains a field its shape does not declare", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, exit := runBlockprov(t, "verify", "--record", "../provenance/goldens/"+tc.record,
				"--jwks", "../provenance/goldens/record.golden.jwks.json", "--repository", "mitstack-eu/postgres-db")

			if exit != tc.wantExit || !strings.HasPrefix(stdout, tc.wantLine) {
				t.Errorf("verify %s = exit %d, stdout %q; want exit %d starting %q", tc.record, exit, stdout, tc.wantExit, tc.wantLine)
			}
		})
	}
}

type ociServer struct {
	*httptest.Server

	mu             sync.Mutex
	forceFail      bool
	blobs          map[string][]byte
	manifestBody   []byte
	manifestTag    string
	lastAuthHeader string
}

func newOCIServer(t *testing.T) *ociServer {
	t.Helper()
	s := &ociServer{blobs: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.lastAuthHeader = r.Header.Get("Authorization")
		fail := s.forceFail
		s.mu.Unlock()

		if fail {
			http.Error(w, "forced failure", http.StatusInternalServerError)
			return
		}

		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/blobs/uploads/"):
			w.Header().Set("Location", r.URL.Path+"session1")
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/blobs/uploads/"):
			body, _ := io.ReadAll(r.Body)
			digest := r.URL.Query().Get("digest")
			s.mu.Lock()
			s.blobs[digest] = body
			s.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/manifests/"):
			body, _ := io.ReadAll(r.Body)
			parts := strings.Split(strings.TrimRight(r.URL.Path, "/"), "/")
			s.mu.Lock()
			s.manifestBody = body
			s.manifestTag = parts[len(parts)-1]
			s.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Server.Close)
	return s
}

func (s *ociServer) blobCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.blobs)
}

func TestRun_Push_UploadsBlobsAndTagsTheManifestWithNoSubject(t *testing.T) {
	srv := newOCIServer(t)
	dir := t.TempDir()
	recordPath := writeFile(t, dir, "record.json", `{"payload":{"blockId":"sample-block"},"token":"tok"}`)
	imageDigest := "sha256:" + strings.Repeat("d", 64)

	stdout, exit := runBlockprov(t, "push",
		"--record", recordPath,
		"--registry", srv.URL,
		"--repo", "mitstack-sample-block",
		"--image-digest", imageDigest,
		"--username", "AWS",
		"--password", "ecr-password",
	)
	if exit != 0 {
		t.Fatalf("want exit 0, got %d\nstdout:\n%s", exit, stdout)
	}

	wantTag := "sha256-" + strings.TrimPrefix(imageDigest, "sha256:") + ".prov"
	if srv.manifestTag != wantTag {
		t.Errorf("manifest was tagged %q, want %q — an untagged referrer is garbage-collected by the ECR lifecycle policy after seven days", srv.manifestTag, wantTag)
	}

	var manifest map[string]interface{}
	if err := json.Unmarshal(srv.manifestBody, &manifest); err != nil {
		t.Fatalf("manifest body is not JSON: %v", err)
	}
	if _, present := manifest["subject"]; present {
		t.Error("pushed manifest must carry NO subject descriptor")
	}
	if manifest["artifactType"] != provenance.ArtifactType {
		t.Errorf("artifactType = %v, want %q", manifest["artifactType"], provenance.ArtifactType)
	}

	if srv.blobCount() == 0 {
		t.Error("push must upload at least the record as a blob")
	}

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("AWS:ecr-password"))
	if srv.lastAuthHeader != wantAuth {
		t.Errorf("Authorization header = %q, want HTTP Basic for AWS:ecr-password", srv.lastAuthHeader)
	}
}

func TestRun_Push_FailsLoudlyOnNon2xxFromRegistry(t *testing.T) {
	srv := newOCIServer(t)
	srv.forceFail = true
	dir := t.TempDir()
	recordPath := writeFile(t, dir, "record.json", `{"payload":{"blockId":"sample-block"},"token":"tok"}`)

	stdout, exit := runBlockprov(t, "push",
		"--record", recordPath,
		"--registry", srv.URL,
		"--repo", "mitstack-sample-block",
		"--image-digest", "sha256:"+strings.Repeat("e", 64),
		"--username", "AWS",
		"--password", "ecr-password",
	)
	if exit == 0 {
		t.Fatalf("want non-zero exit when the registry returns a non-2xx status, got 0\nstdout:\n%s", stdout)
	}
}

func TestRun_UsageErrorsExitTwo(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no subcommand", nil},
		{"an unknown subcommand", []string{"frobnicate"}},
		{"build missing required flags", []string{"build", "--manifest", "/nonexistent"}},
		{"verify missing required flags", []string{"verify", "--record", "/nonexistent"}},
		{"push missing required flags", []string{"push", "--record", "/nonexistent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, exit := runBlockprov(t, tc.args...)

			if exit != 2 {
				t.Errorf("Run(%q) = exit %d, want 2\nstdout:\n%s", tc.args, exit, stdout)
			}
		})
	}
}
