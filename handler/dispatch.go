package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/events"
	"github.com/mitstack-eu/operator-kit/v3/safe"
)

// dispatchRequest runs the verb through the idempotency cache, storing only a settled answer.
//
// A pending:true answer, or an unsubscribe answered absent:false, is never replayed so the engine's re-drive observes the resource again. The cache key carries the named resource, and an empty idempotencyKey or a validate skips the cache because a replayed probe would report a state the resource may have left; an issue skips it because a replayed credential may have expired.
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
	if req.IdempotencyKey != "" && req.Operation != "validate" && req.Operation != contract.OperationIssue {
		key := idempotencyKey{subscriptionID: req.SubscriptionID, idempotencyKey: req.IdempotencyKey, resource: req.Resource}
		status, raw = h.idempotency.do(h.now(), key, run)
	} else {
		status, raw, _ = run()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// dispatchVerbRecovering recovers inside the single-flight call because a recover further out would leave a waiter on the same key holding a zero status, and its fixed error carries nothing from the panic value since the platform stores the response body.
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

// panicValueForLog renders any value but a string or an error only as its type, because formatting it could print a secret a literal scrub cannot find, such as a []byte printed as decimal bytes.
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

// emitVerbEvent emits none for validate, since ProbeHealth dispatches validate in answer to a terminal event and a completed one would loop, and none for an issue, which changes nothing the subscription's events record.
//
// It emits no start event because the event log's partial unique index on (subscriptionId, idempotencyKey) would drop the terminal event under a start event that took its slot.
func (h *Handler) emitVerbEvent(ctx context.Context, operation string, status int, resp contract.Response) {
	if operation == "validate" || operation == contract.OperationIssue {
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

// dispatchVerb refuses with a 400 a request naming a resource of a block served over a single Resource, before the Resource is reached.
func (h *Handler) dispatchVerb(ctx context.Context, req contract.Request, secrets []string) (int, contract.Response) {
	if req.Operation == contract.OperationIssue {
		return h.issue(ctx, req, secrets)
	}
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

// observe never retains d, so no Handler field can hand one verb's secrets to a later call.
func (h *Handler) observe(ctx context.Context, d contract.Desired) (contract.Observation, error) {
	return h.resource.Observe(ctx, d)
}

// refuseSecretOutputs inspects only Outputs, since SecretOutputs is where a secret is returned on purpose, and matches exact values so an output merely containing a secret is left alone.
func refuseSecretOutputs(outputs map[string]string, secrets map[string]map[string]string) error {
	for key, value := range outputs {
		for _, group := range secrets {
			for _, secretValue := range group {
				// An empty secret value is an unset optional secret and must not combine with an empty output into a false refusal.
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

func (h *Handler) ensureAndSettle(ctx context.Context, d contract.Desired, secrets []string) (int, contract.Response) {
	if err := h.resource.Ensure(ctx, d); err != nil {
		return h.errorResponse(err, secrets)
	}
	return h.settlePresent(ctx, d, secrets)
}

func (h *Handler) deleteAndSettle(ctx context.Context, desired contract.Desired, secrets []string) (int, contract.Response) {
	handoverOutputs, err := h.resource.Delete(ctx, desired)
	if err != nil {
		return h.errorResponse(err, secrets)
	}
	return h.settleAbsent(ctx, desired, secrets, handoverOutputs)
}

// settlePresent answers 200 {"pending":true} on budget exhaustion, since pending is never an error.
//
// It checks ctx.Err() before every attempt because defaultSleep returns at once once ctx is done, and a cancelled request would otherwise spend its whole budget on unpaced Observe calls against a cloud API.
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

// settleAbsent always answers a definite boolean, absent:false with a detail for anything but Absent, including Unknown, and checks ctx.Err() before every attempt like settlePresent.
//
// On absent:true, the outputs Delete returned are merged over Observe's outputs; the absent:false answer carries none.
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

// validate calls Observe once and layers "pending":true and the observed redrive on Unknown, which Response() cannot carry; an Observe error is classified, never folded into pending.
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
		resp.Redrive = obs.Redrive
	}
	return http.StatusOK, scrubResponse(resp, secrets)
}

// errorResponse answers a *Refused with 400 and statusDetail "refused:<class>: <msg>" and anything else with 500, and logs a refusal with no class since the wire would otherwise be the only trace.
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

func scrubResponse(resp contract.Response, secrets []string) contract.Response {
	resp.Error = safe.Scrub(resp.Error, secrets...)
	resp.StatusDetail = safe.Scrub(resp.StatusDetail, secrets...)
	return resp
}

// secretsFor collects every literal secret a request carries so all can be scrubbed from the response body and logs.
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
	return out
}
