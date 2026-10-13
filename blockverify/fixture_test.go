package main

// The fixture is a compiling external block module written from literal strings, because this module cannot import api/internal/scaffold.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// kitModuleDir is absolute so the fixture's go.mod can replace onto it from any working directory.
func kitModuleDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed — cannot resolve the kit module directory")
	}
	dir := filepath.Dir(filepath.Dir(thisFile))
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("resolved kit module dir %s has no go.mod: %v", dir, err)
	}
	return dir
}

const kitShimGo = `package main

// kitshim.go — MIT-1466.
//
// The mitstack trust boundary (Ed25519 webhook signature, replay/skew window,
// OBO audience binding, the /logs signed GET, the recent-log ring buffer) and
// the ADR 0042 events channel now have exactly ONE implementation, in the
// operator-kit module. This file is the operator's thin binding to it.
//
// KEEP THIS FILE IDENTICAL ACROSS OPERATORS — scripts/check-operator-kit-pin.sh
// enforces that, and that an operator declares no local replacement for a
// kit-owned symbol. Do not add operator-specific code here; put it in the
// operator's own files.

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/events"
	"github.com/mitstack-eu/operator-kit/v2/webhook"
)

var (
	jwksCache  = map[string]ed25519.PublicKey{}
	nonceCache = map[string]time.Time{}
)

var (
	kitReplay    = webhook.NewReplayOver(&nonceCache)
	kitKeys      = webhook.NewKeyCacheOver(&jwksCache)
	kitLogBuffer = webhook.NewLogBuffer(maxRecentLogLines)
)

const maxRecentLogLines = webhook.MaxRecentLogLines

const replaySkew = webhook.ReplaySkew

func kitDeps(h *Handler) webhook.Deps {
	return webhook.Deps{
		JWKSURL:    h.JWKSURL,
		PublicKey:  h.publicKey,
		Replay:     kitReplay,
		WriteError: writeError,
	}
}

func authenticate(h *Handler, w http.ResponseWriter, r *http.Request, body []byte) (ed25519.PublicKey, bool) {
	return webhook.AuthenticateHook(kitDeps(h), w, r, body)
}

func authenticateLogsGET(h *Handler, w http.ResponseWriter, r *http.Request) (ed25519.PublicKey, bool) {
	return webhook.AuthenticateLogsGET(kitDeps(h), w, r)
}

func verifyReplay(body []byte, now time.Time) error { return kitReplay.Verify(body, now) }

func verifyTimestampSkew(ts string, now time.Time) error {
	return webhook.VerifyTimestampSkew(ts, now)
}

func verifyEd25519Sig(pub ed25519.PublicKey, body []byte, sigHeader string) bool {
	return webhook.VerifyEd25519Sig(pub, body, sigHeader)
}

func publicKeyFromJWKS(jwksJSON []byte, kid string) (ed25519.PublicKey, error) {
	return webhook.PublicKeyFromJWKS(jwksJSON, kid)
}

func (h *Handler) publicKey(kid string) (ed25519.PublicKey, error) {
	return kitKeys.PublicKey(h.JWKSURL, h.Client, kid)
}

func (h *Handler) verifyEd25519(kid, sigHeader string, body []byte) bool {
	pub, err := h.publicKey(kid)
	if err != nil {
		return false
	}
	return webhook.VerifyEd25519Sig(pub, body, sigHeader)
}

type OBOClaims = webhook.OBOClaims

var ErrOBOExpired = webhook.ErrOBOExpired
var ErrOBOInvalid = webhook.ErrOBOInvalid

func MintOBO(priv ed25519.PrivateKey, kid string, claims OBOClaims, ttl time.Duration, now time.Time) (string, error) {
	return webhook.MintOBO(priv, kid, claims, ttl, now)
}

func VerifyOBO(pub ed25519.PublicKey, token string, now time.Time) (OBOClaims, error) {
	return webhook.VerifyOBO(pub, token, now)
}

func appendRecentLog(line string) { kitLogBuffer.Append(line) }

func clearRecentLogs() { kitLogBuffer.Clear() }

func recentLogSnapshot() []string { return kitLogBuffer.Snapshot() }

type recentLogWriter struct{}

func (recentLogWriter) Write(p []byte) (int, error) { return kitLogBuffer.Writer().Write(p) }

func (h *Handler) ServeLogs(w http.ResponseWriter, r *http.Request) {
	webhook.ServeLogs(kitDeps(h), kitLogBuffer, w, r)
}

type EventsCallback = events.Callback

func ContextWithEventsEnvelope(ctx context.Context, req request) context.Context {
	return events.WithEnvelope(ctx, events.Envelope{
		SubscriptionID: req.SubscriptionID,
		Operation:      req.Operation,
		IdempotencyKey: req.IdempotencyKey,
		Callback:       req.EventsCallback,
	})
}

func EmitProgress(ctx context.Context, detail string) error { return events.EmitProgress(ctx, detail) }
func EmitCompleted(ctx context.Context) error                { return events.EmitCompleted(ctx) }
func EmitFailed(ctx context.Context, detail string) error    { return events.EmitFailed(ctx, detail) }
func scrubEventDetail(detail string) string                  { return events.ScrubDetail(detail) }
`

const handlerGo = `package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

// request mirrors the subset of the mitstack lifecycle webhook payload this
// minimal fixture operator cares about.
type request struct {
	Operation      string          ` + "`json:\"operation\"`" + `
	SubscriptionID string          ` + "`json:\"subscriptionId\"`" + `
	IdempotencyKey string          ` + "`json:\"idempotencyKey\"`" + `
	AppID          string          ` + "`json:\"appId,omitempty\"`" + `
	SecretToken    string          ` + "`json:\"secretToken,omitempty\"`" + `
	EventsCallback *EventsCallback ` + "`json:\"eventsCallback,omitempty\"`" + `
}

// resourceStore is a trivial in-memory stand-in for whatever real resource a
// production operator would provision — just enough to prove subscribe
// creates something and unsubscribe deletes it (MIT-1124's teardown contract).
type resourceStore struct {
	mu        sync.Mutex
	resources map[string]bool
}

func newResourceStore() *resourceStore { return &resourceStore{resources: map[string]bool{}} }

func (s *resourceStore) Create(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources[id] = true
}

func (s *resourceStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.resources, id)
}

func (s *resourceStore) Exists(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resources[id]
}

// Handler serves the four lifecycle verbs for this fixture block.
type Handler struct {
	Client  *http.Client
	JWKSURL string
	Store   *resourceStore
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	if _, ok := authenticate(h, w, r, body); !ok {
		return
	}

	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "decode payload: "+err.Error())
		return
	}
	r = r.WithContext(ContextWithEventsEnvelope(r.Context(), req))

	switch req.Operation {
	case "subscribe", "upgrade":
		h.Store.Create(req.SubscriptionID)
		writeOK(w)
	case "unsubscribe":
		h.Store.Delete(req.SubscriptionID)
		writeOK(w)
	case "validate":
		writeOK(w)
	default:
		writeError(w, http.StatusBadRequest, "unknown operation "+req.Operation)
	}
}

func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"outputs": map[string]string{}})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
`

const mainGo = `// fixture is a minimal reference mitstack block operator, used only as a
// compiling fixture for blockverify's own test suite.
package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	jwksURL := os.Getenv("MITSTACK_JWKS_URL")
	if jwksURL == "" {
		log.Fatal("MITSTACK_JWKS_URL is required — mitstack's /.well-known/jwks.json, the only trust anchor")
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8099"
	}
	h := &Handler{JWKSURL: jwksURL, Client: &http.Client{Timeout: 10 * time.Second}, Store: newResourceStore()}
	mux := http.NewServeMux()
	mux.Handle("/hook", h)
	log.Printf("fixture block operator listening on %s", addr)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
`

const testHelpersGo = `package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func newHandler(t *testing.T) (*Handler, *kittest.Signer) {
	t.Helper()
	signer := kittest.NewSigner(t, "test-kid", 7)
	jwksURL := signer.JWKSServer(t)
	return &Handler{JWKSURL: jwksURL, Store: newResourceStore()}, signer
}

func post(t *testing.T, h *Handler, signer *kittest.Signer, req request) *httptest.ResponseRecorder {
	t.Helper()
	body := envelopeFor(t, req)
	r := signer.HookRequest(t, "/hook", body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func envelopeFor(t *testing.T, req request) []byte {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal request into map: %v", err)
	}
	m["nonce"] = "n0nce-" + req.Operation + "-" + req.SubscriptionID
	m["createdAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("remarshal envelope: %v", err)
	}
	return out
}
`

const contractConformanceTestGo = `package main

import (
	"encoding/json"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

func TestContract_DecodesEveryValidGolden(t *testing.T) {
	t.Log(kittest.Describe(t))

	kittest.AssertDecodesEveryValidGolden(t,
		func(raw []byte) error {
			var req request
			return json.Unmarshal(raw, &req)
		},
		"operation", "appId",
	)
}

func TestContract_RejectsMalformedGoldens(t *testing.T) {
	kittest.AssertRejectsMalformed(t, contractAccepts)
}

func contractAccepts(raw []byte) bool {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return false
	}
	var envelope struct {
		Nonce string ` + "`json:\"nonce\"`" + `
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Nonce == "" {
		return false
	}
	switch req.Operation {
	case "subscribe", "unsubscribe", "upgrade", "validate":
		return true
	}
	return false
}
`

// contractResponseTestGo is trivial on purpose: it only has to satisfy the presence check and pass.
const contractResponseTestGo = `package main

import "testing"

func TestContract_EmitsConformingResponse(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("fixture arithmetic broke")
	}
}
`

const secretsLeakTestGo = `package main

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/kittest"
)

// TestNoSecretLeak proves the fixture operator never echoes a received
// secret back into its HTTP response.
func TestNoSecretLeak(t *testing.T) {
	const sentinel = "LEAK-CANARY-secret-token-value"

	h, signer := newHandler(t)

	rec := post(t, h, signer, request{
		Operation:      "subscribe",
		SubscriptionID: "sub_leak_check",
		SecretToken:    sentinel,
	})

	kittest.AssertNoSecret(t, "subscribe response", rec.Body.String(), sentinel)
}
`

const teardownTestGo = `package main

import (
	"net/http"
	"testing"
)

// TestUnsubscribeDeletesTheResourceItCreated proves unsubscribe removes what
// subscribe created (MIT-1124's teardown contract).
func TestUnsubscribeDeletesTheResourceItCreated(t *testing.T) {
	h, signer := newHandler(t)

	subRec := post(t, h, signer, request{Operation: "subscribe", SubscriptionID: "sub_teardown_1"})
	if subRec.Code != http.StatusOK {
		t.Fatalf("subscribe: want 200, got %d: %s", subRec.Code, subRec.Body)
	}
	if !h.Store.Exists("sub_teardown_1") {
		t.Fatal("subscribe did not create the resource this test depends on")
	}

	unsubRec := post(t, h, signer, request{Operation: "unsubscribe", SubscriptionID: "sub_teardown_1"})
	if unsubRec.Code != http.StatusOK {
		t.Fatalf("unsubscribe: want 200, got %d: %s", unsubRec.Code, unsubRec.Body)
	}
	if h.Store.Exists("sub_teardown_1") {
		t.Error("unsubscribe must delete the resource subscribe created")
	}
}
`

const manifestYAML = `apiVersion: mitstack/v1alpha
kind: Block
metadata:
  id: blockverify-fixture
  name: blockverify fixture block
  owner: blockverify-tests
spec:
  layer: backend
  version: 1.0.0
  description: >
    A minimal compiling fixture block, used only to prove blockverify's own
    conformance suite against a standalone external repo.
  provides:
    - blockverify-fixture-capability
  requires: []
`

const dockerfileContents = `FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
RUN go build -o /out/fixture .

FROM alpine:3.20
COPY --from=build /out/fixture /usr/local/bin/fixture
ENTRYPOINT ["/usr/local/bin/fixture"]
`

// goldenFixtureFiles returns the full set of files (relative path -> content)
// for a minimal, compiling, fully-conformant standalone block module. It is
// the PASS case every blockverify suite must accept.
func goldenFixtureFiles(t *testing.T) map[string]string {
	t.Helper()
	goMod := "module fixture\n\ngo 1.25\n\n" +
		"require github.com/mitstack-eu/operator-kit/v2 v2.0.0\n\n" +
		"replace github.com/mitstack-eu/operator-kit/v2 => " + kitModuleDir(t) + "\n"

	return map[string]string{
		"go.mod":                       goMod,
		"kitshim.go":                   kitShimGo,
		"handler.go":                   handlerGo,
		"main.go":                      mainGo,
		"testhelpers_test.go":          testHelpersGo,
		"contract_conformance_test.go": contractConformanceTestGo,
		"contract_response_test.go":    contractResponseTestGo,
		"secrets_leak_test.go":         secretsLeakTestGo,
		"teardown_test.go":             teardownTestGo,
		"manifest.yaml":                manifestYAML,
		"Dockerfile":                   dockerfileContents,
	}
}

// writeFixture materializes files (relative path -> content) under dir.
func writeFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

// newGoldenFixture writes the full golden (pass-everywhere) fixture into a
// fresh t.TempDir() and returns its path.
func newGoldenFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, goldenFixtureFiles(t))
	return dir
}
