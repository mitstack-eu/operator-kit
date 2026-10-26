// Package handler, part of the core every block needs, serves the four lifecycle verbs over a block author's Resource, owning every invariant a block must not override.
package handler

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/webhook"
)

// maxHookBodyBytes bounds the read so a hostile caller cannot exhaust memory before the trust boundary runs.
const maxHookBodyBytes = 1 << 20 // 1 MiB

const defaultVerbBudget = 20

const defaultPollInterval = 500 * time.Millisecond

type Resource interface {
	// Ensure may be called more than once for the same subscription and must converge rather than duplicate.
	Ensure(ctx context.Context, d contract.Desired) error

	// Observe is polled after Ensure and Delete and called once for validate, and sees the verb's Secrets held only for that one call.
	// Its outputs reach the wire unscrubbed, so they must never carry secret material, and a value identical to one of d.Secrets is refused.
	Observe(ctx context.Context, d contract.Desired) (contract.Observation, error)

	// Delete must not error when nothing was ever provisioned, and must be idempotent, since a settle timeout's redrive calls it again after it may already have destroyed the resource.
	// Its outputs are merged into the unsubscribe answer once the block settles absent and reach the wire unscrubbed, so they must never carry secret material.
	Delete(ctx context.Context, d contract.Desired) (outputs map[string]string, err error)
}

// Observer reports what one declared resource actually is, from the provider and never from what an ensure remembered.
type Observer interface {
	ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error)
}

// ResourceReporter, when a block-level Resource implements it, replaces Ensure with EnsureResources, and a reported resource the manifest does not declare is refused with a 400.
type ResourceReporter interface {
	EnsureResources(ctx context.Context, d contract.Desired) ([]contract.CreatedResource, error)
}

// ReplacementPlanner names, on a validate, the declared resources an upgrade would replace, and a name the manifest does not declare is refused with a 400.
type ReplacementPlanner interface {
	PlanReplacements(ctx context.Context, d contract.Desired) ([]string, error)
}

// CredentialIssuer, when a capability's block-level Resource implements it, answers an issue with the named app's credential and when it expires.
type CredentialIssuer interface {
	IssueCredential(ctx context.Context, d contract.Desired, appID string) (contract.Credential, error)
}

// Config wires a Handler; only Resource and JWKSURL are required, VerbBudget defaults to 20, PollInterval to 500ms, and a nil Logs answers GET /logs with 404.
type Config struct {
	Resource Resource

	// Observers needs one Observer for each declared resource, and is refused without a mitstack/v1beta manifest.
	Observers map[string]Observer

	// TypeObservers is keyed by resource-type id and serves a validate naming a resource the manifest no longer declares; refused without a mitstack/v1beta manifest.
	TypeObservers map[string]Observer

	Manifest []byte

	// Types is the type snapshot the manifest names its types from, and a declared resource of a type it marks EndsWithSubscription is counted absent once the unsubscribe's delete succeeds, unobserved.
	Types contract.TypeSnapshot

	JWKSURL string

	// PublicKey defaults to a kit KeyCache over JWKSURL and is no alternative to it, since webhook.AuthenticateHook rejects every request when JWKSURL is empty.
	PublicKey func(kid string) (ed25519.PublicKey, error)

	Replay *webhook.Replay

	Logs *webhook.LogBuffer

	VerbBudget int

	PollInterval time.Duration

	Sleep func(ctx context.Context, d time.Duration)

	Now func() time.Time
}

type Handler struct {
	resource Resource

	// declared is nil unless the manifest is mitstack/v1beta, whose block the handler serves at block level.
	declared      []namedResource
	typeObservers map[string]Observer
	provided      map[string]bool

	types      contract.TypeSnapshot
	capability bool

	jwksURL   string
	publicKey func(kid string) (ed25519.PublicKey, error)
	replay    *webhook.Replay
	logs      *webhook.LogBuffer

	verbBudget   int
	pollInterval time.Duration
	sleep        func(ctx context.Context, d time.Duration)
	now          func() time.Time

	idempotency *idempotencyCache
}

// New refuses a configuration that would let a block override an invariant.
func New(cfg Config) (*Handler, error) {
	if cfg.Resource == nil {
		return nil, errNilResource
	}
	if cfg.JWKSURL == "" {
		return nil, errEmptyJWKSURL
	}
	declared, err := readManifestDeclarations(cfg.Manifest)
	if err != nil {
		return nil, err
	}
	var resources []namedResource
	switch {
	case declared.apiVersion == contract.ManifestAPIVersion:
		resources, err = observedResources(cfg, declared)
		if err == nil {
			err = validateTypeObservers(cfg.TypeObservers)
		}
	case len(cfg.Observers) > 0:
		err = errObserversWithoutManifest
	case len(cfg.TypeObservers) > 0:
		err = errTypeObserversWithoutManifest
	}
	if err != nil {
		return nil, err
	}

	h := &Handler{
		resource:      cfg.Resource,
		declared:      resources,
		typeObservers: cfg.TypeObservers,
		provided:      declared.provided,
		types:         cfg.Types,
		capability:    declared.capability,
		jwksURL:       cfg.JWKSURL,
		publicKey:     cfg.PublicKey,
		replay:        cfg.Replay,
		logs:          cfg.Logs,
		verbBudget:    cfg.VerbBudget,
		pollInterval:  cfg.PollInterval,
		sleep:         cfg.Sleep,
		now:           cfg.Now,
		idempotency:   newIdempotencyCache(),
	}

	if h.verbBudget <= 0 {
		h.verbBudget = defaultVerbBudget
	}
	if h.pollInterval <= 0 {
		h.pollInterval = defaultPollInterval
	}
	if h.sleep == nil {
		h.sleep = defaultSleep
	}
	if h.now == nil {
		h.now = time.Now
	}
	if h.replay == nil {
		h.replay = webhook.NewReplay()
	}
	if h.publicKey == nil {
		cache := webhook.NewKeyCache()
		jwksURL := h.jwksURL
		h.publicKey = func(kid string) (ed25519.PublicKey, error) {
			return cache.PublicKey(jwksURL, nil, kid)
		}
	}

	return h, nil
}

func defaultSleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/hook":
		if r.Method != http.MethodPost {
			h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.serveHook(w, r)
	case "/logs":
		if r.Method != http.MethodGet {
			h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.serveLogs(w, r)
	default:
		h.writeError(w, http.StatusNotFound, "no such path")
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.Serve(w, r) }

func (h *Handler) webhookDeps() webhook.Deps {
	return webhook.Deps{
		JWKSURL:    h.jwksURL,
		PublicKey:  h.publicKey,
		Replay:     h.replay,
		WriteError: h.writeError,
		Now:        h.now,
	}
}

func (h *Handler) serveLogs(w http.ResponseWriter, r *http.Request) {
	if h.logs == nil {
		h.writeError(w, http.StatusNotFound, "this block keeps no logs")
		return
	}
	webhook.ServeLogs(h.webhookDeps(), h.logs, w, r)
}

func (h *Handler) serveHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHookBodyBytes+1))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "cannot read request body: "+err.Error())
		return
	}
	if len(body) > maxHookBodyBytes {
		h.writeError(w, http.StatusBadRequest, "request body exceeds the maximum allowed size")
		return
	}

	pub, ok := webhook.AuthenticateHook(h.webhookDeps(), w, r, body)
	if !ok {
		return
	}

	req, err := contract.DecodeRequest(body)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.OBO == "" {
		h.writeError(w, http.StatusUnauthorized, "missing OBO token")
		return
	}

	if _, err := webhook.VerifyOBO(pub, req.OBO, h.now()); err != nil {
		h.writeError(w, http.StatusUnauthorized, "invalid OBO token: "+err.Error())
		return
	}

	h.dispatchRequest(w, r, req)
}

// requestRefusals classes each 4xx the handler answers before a block is consulted, so the engine reads every 4xx as a refusal.
var requestRefusals = map[int]string{
	http.StatusBadRequest:       "malformed-request",
	http.StatusUnauthorized:     "untrusted-request",
	http.StatusNotFound:         "unknown-path",
	http.StatusMethodNotAllowed: "method-not-allowed",
}

func (h *Handler) writeError(w http.ResponseWriter, status int, msg string) {
	if class, ok := requestRefusals[status]; ok {
		msg = (&Refused{Class: class, Detail: msg}).Error()
		writeResponse(w, status, contract.Response{Error: msg, StatusDetail: msg})
		return
	}
	writeResponse(w, status, contract.Response{Error: msg})
}

func writeResponse(w http.ResponseWriter, status int, resp contract.Response) {
	raw, err := json.Marshal(resp)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"handler: encoding response failed"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
