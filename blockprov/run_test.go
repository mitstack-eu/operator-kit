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

func TestRun_Build_FailedReportExitsNonZeroAndWritesNoFile(t *testing.T) {
	dir := t.TempDir()
	reportPath := writeFile(t, dir, "report.json", `{"suiteVersion":"1.0.0","kitVersion":"v0.1.0","mode":"standalone","manifestDigest":"sha256:aa","verdict":"fail","resultsDigest":"sha256:bb","suites":[]}`)
	outPath := filepath.Join(dir, "record.json")

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("blockprov build must not mint a token when the report verdict is not pass")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer tokenSrv.Close()

	args := buildArgs(t, dir, tokenSrv.URL, outPath)
	args = append(args, "--report", reportPath)

	stdout, exit := runBlockprov(t, args...)
	if exit == 0 {
		t.Fatalf("want non-zero exit for a failed blockverify report, got 0\nstdout:\n%s", stdout)
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Errorf("build must write NO output file when the report verdict is not pass (stat err = %v)", err)
	}
}

func TestRun_Build_PassingReportWritesARecordProvenanceVerifyAccepts(t *testing.T) {
	dir := t.TempDir()
	reportPath := writeFile(t, dir, "report.json", `{"suiteVersion":"1.0.0","kitVersion":"v0.1.0","mode":"standalone","manifestDigest":"sha256:aa","verdict":"pass","resultsDigest":"sha256:bb","suites":[]}`)
	outPath := filepath.Join(dir, "record.json")

	tokenURL, resolve := mintServer(t, testRepository, testCommit)

	args := buildArgs(t, dir, tokenURL, outPath)
	args = append(args, "--report", reportPath)

	stdout, exit := runBlockprov(t, args...)
	if exit != 0 {
		t.Fatalf("want exit 0 for a passing report, got %d\nstdout:\n%s", exit, stdout)
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
	if rec.Payload.Conformance.RunURL != "https://github.com/mitstack-eu/sample-block/actions/runs/1" {
		t.Errorf("runUrl = %q, want the --run-url flag's value", rec.Payload.Conformance.RunURL)
	}
	if rec.Payload.SourceRepository != testRepository {
		t.Errorf("sourceRepository = %q, want %q", rec.Payload.SourceRepository, testRepository)
	}
}

func TestRun_Verify_GoldenExitsZero(t *testing.T) {
	stdout, exit := runBlockprov(t, "verify",
		"--record", "../provenance/goldens/record.golden.json",
		"--jwks", "../provenance/goldens/record.golden.jwks.json",
		"--repository", "mitstack-eu/postgres-db",
	)
	if exit != 0 {
		t.Fatalf("want exit 0 verifying the committed golden, got %d\nstdout:\n%s", exit, stdout)
	}
}

func TestRun_Verify_OtherIdentityExitsNonZeroAndNamesTheMismatch(t *testing.T) {
	stdout, exit := runBlockprov(t, "verify",
		"--record", "../provenance/goldens/invalid-other-identity.json",
		"--jwks", "../provenance/goldens/record.golden.jwks.json",
		"--repository", "mitstack-eu/postgres-db",
	)
	if exit == 0 {
		t.Fatalf("want non-zero exit verifying invalid-other-identity.json, got 0\nstdout:\n%s", stdout)
	}
	if !strings.Contains(strings.ToLower(stdout), "identity") {
		t.Errorf("stdout must name the identity mismatch, got:\n%s", stdout)
	}
}

// verify must go through provenance.Decode, because json.Unmarshal drops an attacker-added field and the record would be accepted.
func TestRun_Verify_UnknownFieldExitsNonZero(t *testing.T) {
	stdout, exit := runBlockprov(t, "verify",
		"--record", "../provenance/goldens/invalid-unknown-field.json",
		"--jwks", "../provenance/goldens/record.golden.jwks.json",
		"--repository", "mitstack-eu/postgres-db",
	)
	if exit == 0 {
		t.Fatalf("want non-zero exit verifying a record with an unknown payload field, got 0 (blockprov verify must decode through provenance.Decode, not json.Unmarshal)\nstdout:\n%s", stdout)
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

func TestRun_UsageError_NoSubcommand(t *testing.T) {
	stdout, exit := runBlockprov(t)
	if exit != 2 {
		t.Fatalf("want exit 2 with no subcommand, got %d\nstdout:\n%s", exit, stdout)
	}
}

func TestRun_UsageError_UnknownSubcommand(t *testing.T) {
	stdout, exit := runBlockprov(t, "frobnicate")
	if exit != 2 {
		t.Fatalf("want exit 2 for an unknown subcommand, got %d\nstdout:\n%s", exit, stdout)
	}
}

func TestRun_UsageError_BuildMissingRequiredFlags(t *testing.T) {
	stdout, exit := runBlockprov(t, "build", "--manifest", "/nonexistent")
	if exit != 2 {
		t.Fatalf("want exit 2 when required build flags are missing, got %d\nstdout:\n%s", exit, stdout)
	}
}

func TestRun_UsageError_VerifyMissingRequiredFlags(t *testing.T) {
	stdout, exit := runBlockprov(t, "verify", "--record", "/nonexistent")
	if exit != 2 {
		t.Fatalf("want exit 2 when required verify flags are missing, got %d\nstdout:\n%s", exit, stdout)
	}
}

func TestRun_UsageError_PushMissingRequiredFlags(t *testing.T) {
	stdout, exit := runBlockprov(t, "push", "--record", "/nonexistent")
	if exit != 2 {
		t.Fatalf("want exit 2 when required push flags are missing, got %d\nstdout:\n%s", exit, stdout)
	}
}
