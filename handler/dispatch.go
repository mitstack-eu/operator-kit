package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/events"
	"github.com/mitstack-eu/operator-kit/safe"
)

// dispatchRequest is the second half of the POST /hook pipeline: everything
// after the trust boundary and envelope decode have already succeeded. It
// stashes the events envelope on ctx, then runs the verb through the
// idempotency cache: a repeat delivery of the same key replays the first
// call's response without touching the Resource again, and a delivery that
// finds its key already in flight waits for that call and shares its
// response rather than running the verb a second time. Only a settled
// answer is stored: a 2xx carrying pending:true, or an unsubscribe answered
// absent:false, reaches the caller and any waiter on its key but is never
// replayed, so the engine's re-drive of the same key observes the resource
// again. A subscribe or upgrade answer that carries absent:false is settled
// and stored as usual. The cache key carries the resource the request
// names, so one idempotency key naming different resources runs each of
// them, and a repeat of the same named request replays. An empty
// idempotencyKey and a validate both skip the cache entirely, so neither is
// ever stored, replayed or single-flighted: validate is a read-only probe,
// and a replayed probe would report a state the resource may have left. A
// panic in the Resource or a hook is recovered inside run, so the calling
// delivery and every waiter on its key receive the same retryable 500.
func (h *Handler) dispatchRequest(w http.ResponseWriter, r *http.Request, req contract.Request) {
	secrets := h.secretsFor(req)

	var callback *events.Callback
	if req.EventsCallback != nil {
		callback = &events.Callback{URL: req.EventsCallback.URL, Token: req.EventsCallback.Token}
	}
	ctx := events.WithEnvelope(r.Context(), events.Envelope{
		SubscriptionID: req.SubscriptionID,
		Operation:      req.Operation,
		IdempotencyKey: req.IdempotencyKey,
		Callback:       callback,
	})

	run := func() (int, []byte, bool) {
		status, resp := h.dispatchVerbRecovering(ctx, req, secrets)
		h.emitVerbEvent(ctx, req.Operation, status, resp)

		raw, err := json.Marshal(resp)
		if err != nil {
			raw, _ = json.Marshal(contract.Response{Error: "handler: encoding response failed: " + err.Error()})
			return http.StatusInternalServerError, raw, true
		}
		notYetAbsent := req.Operation == "unsubscribe" && resp.Absent != nil && !*resp.Absent
		return status, raw, !resp.Pending && !notYetAbsent
	}

	var status int
	var raw []byte
	if req.IdempotencyKey != "" && req.Operation != "validate" {
		key := idempotencyKey{subscriptionID: req.SubscriptionID, idempotencyKey: req.IdempotencyKey, resource: req.Resource}
		status, raw = h.idempotency.do(h.now(), key, run)
	} else {
		status, raw, _ = run()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// dispatchVerbRecovering runs dispatchVerb and turns a panic raised by the
// Resource or a hook into the answer errorResponse gives any other fault:
// HTTP 500 with no refusal class. The engine reads a 500 as an operator
// failure and retries it, and the idempotency cache never stores one, so the
// retry reaches the Resource again. It recovers here, inside the
// single-flight call, because a recover further out would leave a waiter on
// the same key holding the in-flight call's zero status. The response error
// is a fixed message per verb that carries nothing from the panic value,
// since the platform stores the response body. The panic is logged with the
// block, subscription and verb, as described on panicValueForLog.
func (h *Handler) dispatchVerbRecovering(ctx context.Context, req contract.Request, secrets []string) (status int, resp contract.Response) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		log.Printf("handler: block %s subscription %s: %s panicked: %s",
			safe.LogSafe(req.BlockID, secrets...),
			safe.LogSafe(req.SubscriptionID, secrets...),
			req.Operation,
			panicValueForLog(p, secrets))
		status = http.StatusInternalServerError
		resp = contract.Response{Error: "handler: the block panicked during " + req.Operation}
	}()
	return h.dispatchVerb(ctx, req, secrets)
}

// panicValueForLog renders a recovered panic value for the log. A string or
// an error is kept, with every request secret scrubbed where it appears
// literally. Any other value is rendered only as its type, because
// formatting it could print a secret in a form a literal scrub cannot find,
// such as a []byte printed as decimal bytes.
func panicValueForLog(p any, secrets []string) string {
	switch v := p.(type) {
	case string:
		return safe.LogSafe(v, secrets...)
	case error:
		return safe.LogSafe(v.Error(), secrets...)
	default:
		return fmt.Sprintf("value of type %T", p)
	}
}

// emitVerbEvent emits at most one event for one verb call: none for
// validate, since a probe is not a lifecycle operation and a terminal event
// on one asks the engine for another probe (ProbeHealth dispatches validate
// in answer to a terminal event, so a validate that emits completed would
// loop). For subscribe, upgrade and unsubscribe it keys the choice off the
// literal contract.Response.Pending field: completed on a settled 2xx,
// progress when Pending is true, failed (carrying the detail) on 4xx/5xx.
// There is no start event for any verb — an event log's partial unique
// index on (subscriptionId, idempotencyKey) for progress/completed/
// unhealthy would drop the terminal event under a start event that already
// took its slot.
func (h *Handler) emitVerbEvent(ctx context.Context, operation string, status int, resp contract.Response) {
	if operation == "validate" {
		return
	}
	if status >= 200 && status < 300 {
		if resp.Pending {
			_ = events.EmitProgress(ctx, resp.StatusDetail)
		} else {
			_ = events.EmitCompleted(ctx)
		}
		return
	}
	detail := resp.Error
	if detail == "" {
		detail = resp.StatusDetail
	}
	_ = events.EmitFailed(ctx, detail)
}

// dispatchVerb calls the Resource and its declared hooks for req.Operation,
// or the declared resources when the block declares its resources. A block
// served over a single Resource declares no resource a request could name,
// so a request naming one is refused with a 400 before the Resource is
// reached. contract.DecodeRequest has already validated the operation enum,
// so the four cases here are exhaustive.
func (h *Handler) dispatchVerb(ctx context.Context, req contract.Request, secrets []string) (int, contract.Response) {
	desired := req.Desired()

	if h.declared != nil {
		return h.dispatchDeclared(ctx, req.Operation, desired, secrets)
	}
	if req.Resource != "" {
		return h.errorResponse(Refuse(classUndeclaredResource,
			"resource %q is not declared: the block is served as one resource and declares none", req.Resource), secrets)
	}

	switch req.Operation {
	case "subscribe", "upgrade":
		return h.ensureAndSettle(ctx, desired, secrets)
	case "validate":
		return h.validate(ctx, desired, secrets)
	case "unsubscribe":
		return h.deleteAndSettle(ctx, desired, secrets)
	default:
		return http.StatusInternalServerError, contract.Response{Error: "handler: unreachable — unknown operation " + req.Operation}
	}
}

// observe calls RequestObserver.ObserveWithRequest when the Resource
// implements it, so the verb's own Secrets reach the observation the way a
// token-backed block needs, and falls back to the plain Observe(ctx,
// Identity) otherwise. d is never retained beyond this call: the Handler
// holds no field of its own that could hand one verb's secrets to a later,
// unrelated call.
func (h *Handler) observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	if ro, ok := h.resource.(RequestObserver); ok {
		return ro.ObserveWithRequest(ctx, d)
	}
	return h.resource.Observe(ctx, d.Identity)
}

// refuseSecretOutputs refuses a response whose outputs carry a value that
// exactly matches one of the request's secrets, before that response ever
// reaches the wire. It only inspects Outputs — SecretOutputs is where a
// Resource is meant to return a secret value on purpose — and it matches on
// exact value, never substring, so an output that merely contains a secret
// (an owner login embedded in a repo path, say) is left alone.
func refuseSecretOutputs(outputs map[string]string, secrets map[string]map[string]string) error {
	for key, value := range outputs {
		for _, group := range secrets {
			for _, secretValue := range group {
				// An empty secret value is an unset optional secret, not a
				// secret, and an empty output value leaks nothing — so the
				// two must never combine into a false refusal.
				if secretValue == "" {
					continue
				}
				if value == secretValue {
					return Refuse("secret-in-output", "output %q carries a secret value", key)
				}
			}
		}
	}
	return nil
}

// ensureAndSettle runs beforeEnsure, Ensure, afterEnsure, then polls Observe
// until Present or the verb budget runs out. subscribe and upgrade share
// this exact call sequence.
func (h *Handler) ensureAndSettle(ctx context.Context, d contract.Desired, secrets []string) (int, contract.Response) {
	if hook, ok := h.hooks[HookBeforeEnsure]; ok {
		if err := hook(ctx, d); err != nil {
			return h.errorResponse(err, secrets)
		}
	}
	if err := h.resource.Ensure(ctx, d); err != nil {
		return h.errorResponse(err, secrets)
	}
	if hook, ok := h.hooks[HookAfterEnsure]; ok {
		if err := hook(ctx, d); err != nil {
			return h.errorResponse(err, secrets)
		}
	}
	return h.settlePresent(ctx, d, secrets)
}

// deleteAndSettle runs beforeDelete, Delete, afterDelete, then settles the
// teardown. desired carries the unsubscribe request's own Params (in
// addition to its Identity), so a Resource that also implements
// HandoverResource sees them — a plain Resource's Delete still receives only
// the Identity, unchanged. A manifest that declares
// spec.lifecycle.retainsOnUnsubscribe answers absent:true as soon as
// beforeDelete, Delete (or DeleteWithRequest) and afterDelete have all
// succeeded, carrying any handover outputs, without ever polling Observe —
// the block keeps the resource by design, so Observe would just keep
// reporting Present. Any failure along the way still takes the ordinary
// errorResponse path regardless of the declaration. Every other Resource
// settles the ordinary way: settleAbsent polls Observe until Absent or the
// verb budget runs out.
func (h *Handler) deleteAndSettle(ctx context.Context, desired contract.Desired, secrets []string) (int, contract.Response) {
	id := desired.Identity

	if hook, ok := h.hooks[HookBeforeDelete]; ok {
		if err := hook(ctx, desired); err != nil {
			return h.errorResponse(err, secrets)
		}
	}

	var handoverOutputs map[string]string
	if hr, ok := h.resource.(HandoverResource); ok {
		outputs, err := hr.DeleteWithRequest(ctx, desired)
		if err != nil {
			return h.errorResponse(err, secrets)
		}
		handoverOutputs = outputs
	} else if err := h.resource.Delete(ctx, id); err != nil {
		return h.errorResponse(err, secrets)
	}

	if hook, ok := h.hooks[HookAfterDelete]; ok {
		if err := hook(ctx, desired); err != nil {
			return h.errorResponse(err, secrets)
		}
	}

	if h.retainsOnUnsubscribe {
		if err := refuseSecretOutputs(handoverOutputs, desired.Secrets); err != nil {
			return h.errorResponse(err, secrets)
		}
		absent := true
		return http.StatusOK, contract.Response{
			Absent:  &absent,
			Outputs: handoverOutputs,
			StatusDetail: safe.Scrub(
				"the block declares it retains its resource on unsubscribe, so absence was not observed",
				secrets...),
		}
	}

	return h.settleAbsent(ctx, desired, secrets, handoverOutputs)
}

// settlePresent polls Observe up to VerbBudget times, stopping early on
// Present. Present within budget answers 200 with the block's own
// Observation.Response(). Budget exhaustion while Absent or Unknown answers
// 200 {"pending":true} — pending is never an error (MIT-1293). It also
// stops as soon as ctx.Err() is non-nil, checked before every attempt, and
// answers with what it has observed so far: defaultSleep returns at once
// once ctx is done, so without this check a cancelled request would spend
// its whole remaining budget on Observe calls back to back, unpaced,
// against a cloud API, for an answer nobody will read.
func (h *Handler) settlePresent(ctx context.Context, d contract.Desired, secrets []string) (int, contract.Response) {
	var last contract.Observation
	attempts := 0
	for attempts < h.verbBudget {
		if ctx.Err() != nil {
			break
		}
		obs, err := h.observe(ctx, d)
		if err != nil {
			return h.errorResponse(err, secrets)
		}
		attempts++
		last = obs
		if obs.State == contract.Present {
			if err := refuseSecretOutputs(obs.Outputs, d.Secrets); err != nil {
				return h.errorResponse(err, secrets)
			}
			return http.StatusOK, scrubResponse(obs.Response(), secrets)
		}
		if attempts < h.verbBudget {
			h.sleep(ctx, h.pollInterval)
		}
	}
	return http.StatusOK, contract.Response{
		Pending:      true,
		StatusDetail: safe.Scrub(fmt.Sprintf("still %s after %d attempts", last.State, attempts), secrets...),
	}
}

// settleAbsent polls Observe up to VerbBudget times, stopping early on
// Absent. Unlike settlePresent (and unlike validate), it always answers a
// definite boolean: Absent within budget is absent:true, and anything else
// — including Unknown — is absent:false with a detail, never an omitted
// marker. Like settlePresent, it stops as soon as ctx.Err() is non-nil,
// checked before every attempt, and answers with what it has observed so
// far rather than spending the remaining budget unpaced.
//
// handoverOutputs is whatever a HandoverResource's DeleteWithRequest
// returned, or nil for a plain Resource. On the absent:true answer it is
// merged with Observe's own outputs — the union of the two, handoverOutputs
// winning on a key both produce. The absent:false answer carries no
// outputs, same as before HandoverResource existed.
func (h *Handler) settleAbsent(ctx context.Context, d contract.Desired, secrets []string, handoverOutputs map[string]string) (int, contract.Response) {
	var last contract.Observation
	attempts := 0
	for attempts < h.verbBudget {
		if ctx.Err() != nil {
			break
		}
		obs, err := h.observe(ctx, d)
		if err != nil {
			return h.errorResponse(err, secrets)
		}
		attempts++
		last = obs
		if obs.State == contract.Absent {
			merged := mergeOutputs(obs.Outputs, handoverOutputs)
			if err := refuseSecretOutputs(merged, d.Secrets); err != nil {
				return h.errorResponse(err, secrets)
			}
			absent := true
			return http.StatusOK, contract.Response{
				Absent:       &absent,
				Outputs:      merged,
				StatusDetail: safe.Scrub(obs.StatusDetail, secrets...),
			}
		}
		if attempts < h.verbBudget {
			h.sleep(ctx, h.pollInterval)
		}
	}
	absent := false
	return http.StatusOK, contract.Response{
		Absent:       &absent,
		StatusDetail: safe.Scrub(fmt.Sprintf("observed %s after %d attempts", last.State, attempts), secrets...),
	}
}

// mergeOutputs unions base (Observe's outputs) with overlay (a
// HandoverResource's returned outputs, or a later declared resource's
// outputs), overlay winning on a key both carry. An empty overlay returns
// base unchanged, so the ordinary non-handover path allocates nothing new.
func mergeOutputs(base, overlay map[string]string) map[string]string {
	if len(overlay) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range overlay {
		merged[k] = v
	}
	return merged
}

// validate calls Observe exactly once and never polls. It returns
// Observation.Response(), with "pending":true layered on top for Unknown —
// the half Response() cannot carry, since its own doc says it reports what
// was observed rather than that a resource is still converging. An Observe
// error is classified, never folded into pending.
func (h *Handler) validate(ctx context.Context, d contract.Desired, secrets []string) (int, contract.Response) {
	obs, err := h.observe(ctx, d)
	if err != nil {
		return h.errorResponse(err, secrets)
	}
	if err := refuseSecretOutputs(obs.Outputs, d.Secrets); err != nil {
		return h.errorResponse(err, secrets)
	}
	resp := obs.Response()
	if obs.State == contract.Unknown {
		resp.Pending = true
	}
	return http.StatusOK, scrubResponse(resp, secrets)
}

// errorResponse classifies err into the wire response the CONTRACT pins: a
// *Refused (by errors.As) is 400 with statusDetail "refused:<class>: <msg>",
// anything else is 500 with no such prefix. A *Refused whose Class is empty
// logs that the block returned a refusal with no class, since a silent
// "unclassified" on the wire would otherwise be the only trace of it.
func (h *Handler) errorResponse(err error, secrets []string) (int, contract.Response) {
	var refused *Refused
	if errors.As(err, &refused) {
		if refused.Class == "" {
			log.Printf("handler: block returned a refusal with no class: %s", safe.LogSafe(refused.Detail, secrets...))
		}
		return http.StatusBadRequest, contract.Response{
			Error:        safe.Scrub(err.Error(), secrets...),
			StatusDetail: safe.Scrub(refused.Error(), secrets...),
		}
	}

	log.Printf("handler: %s", safe.LogSafe(err.Error(), secrets...))
	return http.StatusInternalServerError, contract.Response{Error: safe.Scrub(err.Error(), secrets...)}
}

// scrubResponse redacts every secret value out of the two free-text fields a
// Resource controls, before the response reaches the wire.
func scrubResponse(resp contract.Response, secrets []string) contract.Response {
	resp.Error = safe.Scrub(resp.Error, secrets...)
	resp.StatusDetail = safe.Scrub(resp.StatusDetail, secrets...)
	return resp
}

// secretsFor collects every literal secret value a request carries —
// secrets, secretInputs, appSecrets, the OBO token, the events callback
// token, and each secret value in the resourceInputs of the declared
// resource the request names, or of the resource Config.Types serves by its
// recorded type (see secretResourceInputs) — so the handler can scrub all
// of them out of anything it writes to the response body or a log line.
func (h *Handler) secretsFor(req contract.Request) []string {
	var out []string
	for _, group := range req.Secrets {
		for _, v := range group {
			out = append(out, v)
		}
	}
	for _, v := range req.SecretInputs {
		out = append(out, v)
	}
	for _, v := range req.AppSecrets {
		out = append(out, v)
	}
	if req.OBO != "" {
		out = append(out, req.OBO)
	}
	if req.EventsCallback != nil && req.EventsCallback.Token != "" {
		out = append(out, req.EventsCallback.Token)
	}
	if req.Resource != "" {
		nr, ok := h.declaredResource(req.Resource)
		if !ok {
			nr, ok = h.typedResource(req.Resource, req.ResourceType)
		}
		if ok {
			out = append(out, secretResourceInputs(nr.declaration, h.secretParams, req.ResourceInputs)...)
		}
	}
	return out
}
