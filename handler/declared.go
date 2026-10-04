package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/safe"
)

// classUndeclaredResource is the refusal class a request naming a resource
// the block does not declare is answered with.
const classUndeclaredResource = "undeclared-resource"

// dispatchDeclared serves one verb over a block's declared resources:
// subscribe and upgrade ensure each resource in creation order and settle it
// present before the next, unsubscribe deletes each in reverse order and
// settles it absent before the next, and validate observes each once. A
// request naming one declared resource runs the verb on that resource
// alone, its calls handed a Desired carrying the name, the request's
// resource inputs and the identity its ensure stored, and its answer
// carries that resource's entry and outputs alone. A name the manifest does
// not declare runs an unsubscribe or validate on the Config.Types handler
// for the request's resourceType, under that name, and is otherwise refused
// with a 400 before any resource is reached. A resource implementing
// ReportingResource is ensured through EnsureWithReport, and its ensure
// entry carries the identity and outputs it reported. A resource
// implementing RequestObserver is observed through
// ObserveWithRequest and one implementing RequestDeleter is deleted through
// DeleteWithRequest, each handed the verb's Desired whether or not the
// request names a resource; any other is observed and deleted with the
// Identity alone. Every answer, an error included,
// carries the resources map: an entry for each resource the verb reached,
// holding the calls that ran for it. A resource still converging, left
// standing or failing stops the verb there, so no later resource is
// reached. The request context is checked before each resource's first
// call, and once it is done the verb stops too, answering that it has not
// finished and naming the first resource it did not reach.
func (h *Handler) dispatchDeclared(ctx context.Context, operation string, d contract.Desired, secrets []string) (int, contract.Response) {
	resources := h.declared
	if d.Resource != "" {
		nr, ok := h.declaredResource(d.Resource)
		if !ok {
			var handled bool
			nr, handled = h.typedResource(d.Resource, d.ResourceType)
			if !handled || (operation != "unsubscribe" && operation != "validate") {
				return h.errorResponse(undeclaredResource(operation, d, handled), secrets)
			}
		}
		resources = []namedResource{nr}
	}

	run := &declaredRun{h: h, d: d, secrets: secrets, resources: resources, outcomes: map[string]contract.ResourceOutcome{}}
	switch operation {
	case "subscribe", "upgrade":
		return run.ensureEach(ctx)
	case "validate":
		return run.validateEach(ctx)
	case "unsubscribe":
		return run.deleteEach(ctx)
	default:
		return http.StatusInternalServerError, contract.Response{Error: "handler: unreachable — unknown operation " + operation}
	}
}

// declaredResource returns the declared resource the manifest names name,
// and whether there is one.
func (h *Handler) declaredResource(name string) (namedResource, bool) {
	for _, nr := range h.declared {
		if nr.name == name {
			return nr, true
		}
	}
	return namedResource{}, false
}

// typedResource returns, under name, the Config.Types handler for
// resourceType, the recorded type of a resource the manifest does not
// declare, and whether there is one.
func (h *Handler) typedResource(name, resourceType string) (namedResource, bool) {
	r, ok := h.types[resourceType]
	if !ok {
		return namedResource{}, false
	}
	return namedResource{name: name, resource: r, declaration: contract.ManifestResource{Name: name, Type: resourceType}}, true
}

// undeclaredResource is the refusal of a request naming a resource the
// manifest does not declare, saying why its recorded type did not serve it.
func undeclaredResource(operation string, d contract.Desired, handled bool) error {
	const notDeclared = "resource %q is not declared under the manifest's spec.resources"
	switch {
	case d.ResourceType == "":
		return Refuse(classUndeclaredResource, notDeclared+", and the request sent no resourceType to unsubscribe or validate it by", d.Resource)
	case handled:
		return Refuse(classUndeclaredResource, notDeclared+", so %s is refused: a resource served by its recorded type %q is only unsubscribed or validated",
			d.Resource, operation, d.ResourceType)
	default:
		return Refuse(classUndeclaredResource, notDeclared+", and Config.Types holds no handler for its recorded type %q", d.Resource, d.ResourceType)
	}
}

// declaredRun is one verb's pass over the resources it runs on, in creation
// order, collecting each resource's outcome under its name.
type declaredRun struct {
	h         *Handler
	d         contract.Desired
	secrets   []string
	resources []namedResource
	outcomes  map[string]contract.ResourceOutcome
}

// ensureEach ensures each resource in creation order and polls its Observe
// until Present before moving to the next. Every resource present answers
// 200 with the union of their outputs and secret outputs, a later resource
// winning a key both produce. A resource not present within the verb budget,
// or not reached because the request context is done, answers 200
// pending:true and stops there.
func (r *declaredRun) ensureEach(ctx context.Context) (int, contract.Response) {
	var outputs, secretOutputs map[string]string
	for _, nr := range r.resources {
		if ctx.Err() != nil {
			return http.StatusOK, contract.Response{Pending: true, StatusDetail: r.notReached(nr.name), Resources: r.outcomes}
		}
		var entry contract.ResourceOutcome
		ensured, err := r.ensure(ctx, nr)
		if err != nil {
			return r.fail(nr.name, &entry, &entry.Ensure, err)
		}
		entry.Ensure = ensured

		obs, attempts, err := r.settle(ctx, nr, contract.Present, &entry)
		if err != nil {
			return r.fail(nr.name, &entry, &entry.Observe, err)
		}
		r.outcomes[nr.name] = entry
		if obs.State != contract.Present {
			return http.StatusOK, contract.Response{
				Pending:      true,
				StatusDetail: safe.Scrub(fmt.Sprintf("resource %q still %s after %d attempts", nr.name, obs.State, attempts), r.secrets...),
				Resources:    r.outcomes,
			}
		}
		outputs = mergeOutputs(outputs, obs.Outputs)
		secretOutputs = mergeOutputs(secretOutputs, obs.SecretOutputs)
	}

	resp := contract.Observation{State: contract.Present, Outputs: outputs, SecretOutputs: secretOutputs}.Response()
	resp.Resources = r.outcomes
	return http.StatusOK, resp
}

// ensure runs one resource's ensure and returns its ensure outcome: through
// EnsureWithReport when the resource implements ReportingResource, carrying
// the identity and outputs it reported, and through Ensure otherwise, with
// nothing to report. A reported identity or output value equal to a
// request secret is refused rather than reported.
func (r *declaredRun) ensure(ctx context.Context, nr namedResource) (*contract.CallOutcome, error) {
	reporting, ok := nr.resource.(ReportingResource)
	if !ok {
		if err := nr.resource.Ensure(ctx, r.d); err != nil {
			return nil, err
		}
		return &contract.CallOutcome{}, nil
	}

	report, err := reporting.EnsureWithReport(ctx, r.d)
	if err != nil {
		return nil, err
	}
	if err := refuseSecretOutputs(report.Identity, r.d.Secrets); err != nil {
		return nil, err
	}
	if err := refuseSecretOutputs(report.Outputs, r.d.Secrets); err != nil {
		return nil, err
	}
	return &contract.CallOutcome{Identity: report.Identity, Outputs: report.Outputs}, nil
}

// deleteEach deletes each resource in reverse creation order and polls its
// Observe until Absent before moving to the next, so a resource created
// earlier outlives every one created after it. Every resource absent
// answers 200 absent:true with the union of the absent observations'
// outputs. A resource not absent within the verb budget, or not reached
// because the request context is done, answers 200 absent:false and stops
// there.
func (r *declaredRun) deleteEach(ctx context.Context) (int, contract.Response) {
	var outputs map[string]string
	for i := len(r.resources) - 1; i >= 0; i-- {
		nr := r.resources[i]
		if ctx.Err() != nil {
			absent := false
			return http.StatusOK, contract.Response{Absent: &absent, StatusDetail: r.notReached(nr.name), Resources: r.outcomes}
		}
		var entry contract.ResourceOutcome
		if err := r.delete(ctx, nr); err != nil {
			return r.fail(nr.name, &entry, &entry.Delete, err)
		}
		entry.Delete = &contract.CallOutcome{}

		obs, attempts, err := r.settle(ctx, nr, contract.Absent, &entry)
		if err != nil {
			return r.fail(nr.name, &entry, &entry.Observe, err)
		}
		r.outcomes[nr.name] = entry
		if obs.State != contract.Absent {
			absent := false
			return http.StatusOK, contract.Response{
				Absent:       &absent,
				StatusDetail: safe.Scrub(fmt.Sprintf("resource %q observed %s after %d attempts", nr.name, obs.State, attempts), r.secrets...),
				Resources:    r.outcomes,
			}
		}
		outputs = mergeOutputs(outputs, obs.Outputs)
	}

	absent := true
	return http.StatusOK, contract.Response{Absent: &absent, Outputs: outputs, Resources: r.outcomes}
}

// validateEach observes each resource once, in creation order, and never
// polls. Every resource present answers as a present Observation carrying
// the union of their outputs and secret outputs; every resource absent
// answers absent:true; any other mix answers pending:true naming each
// resource's state. An Observe error is classified, never folded into
// pending, and stops the probe there. A resource not reached because the
// request context is done answers pending:true naming it.
func (r *declaredRun) validateEach(ctx context.Context) (int, contract.Response) {
	var outputs, secretOutputs map[string]string
	present, absent := 0, 0
	states := make([]string, 0, len(r.resources))
	for _, nr := range r.resources {
		if ctx.Err() != nil {
			return http.StatusOK, contract.Response{Pending: true, StatusDetail: r.notReached(nr.name), Resources: r.outcomes}
		}
		var entry contract.ResourceOutcome
		obs, err := r.observe(ctx, nr)
		if err != nil {
			return r.fail(nr.name, &entry, &entry.Observe, err)
		}
		call, err := r.observed(obs)
		if err != nil {
			return r.fail(nr.name, &entry, &entry.Observe, err)
		}
		entry.Observe = call
		r.outcomes[nr.name] = entry

		switch obs.State {
		case contract.Present:
			present++
			outputs = mergeOutputs(outputs, obs.Outputs)
			secretOutputs = mergeOutputs(secretOutputs, obs.SecretOutputs)
		case contract.Absent:
			absent++
		}
		states = append(states, fmt.Sprintf("%q %s", nr.name, obs.State))
	}

	var resp contract.Response
	switch len(r.resources) {
	case present:
		resp = contract.Observation{State: contract.Present, Outputs: outputs, SecretOutputs: secretOutputs}.Response()
	case absent:
		resp = contract.Observation{State: contract.Absent}.Response()
	default:
		resp = contract.Response{
			Pending:      true,
			StatusDetail: safe.Scrub("resources observed "+strings.Join(states, ", "), r.secrets...),
		}
	}
	resp.Resources = r.outcomes
	return http.StatusOK, resp
}

// notReached is the statusDetail of a verb that stopped because the request
// context was done before it reached the named resource, scrubbed of
// request secrets.
func (r *declaredRun) notReached(name string) string {
	return safe.Scrub(fmt.Sprintf("resource %q not reached: the request ended before the verb got to it", name), r.secrets...)
}

// settle polls one resource's Observe up to the verb budget, stopping early
// once it reports want, and stopping before any attempt once ctx is done,
// exactly as settlePresent and settleAbsent pace a single Resource. The last
// observation is recorded as entry's observe outcome, its outputs refused
// when they carry a request secret; no observe outcome is recorded when no
// attempt ran. It returns the last observation and the attempts made.
func (r *declaredRun) settle(ctx context.Context, nr namedResource, want contract.State, entry *contract.ResourceOutcome) (contract.Observation, int, error) {
	var last contract.Observation
	attempts := 0
	for attempts < r.h.verbBudget {
		if ctx.Err() != nil {
			break
		}
		obs, err := r.observe(ctx, nr)
		if err != nil {
			return last, attempts, err
		}
		attempts++
		last = obs
		if obs.State == want {
			break
		}
		if attempts < r.h.verbBudget {
			r.h.sleep(ctx, r.h.pollInterval)
		}
	}
	if attempts == 0 {
		return last, attempts, nil
	}
	call, err := r.observed(last)
	if err != nil {
		return last, attempts, err
	}
	entry.Observe = call
	return last, attempts, nil
}

// observe runs one resource's observe: through ObserveWithRequest, handed
// the verb's Desired, when the resource implements RequestObserver, and
// through Observe, handed its Identity alone, otherwise.
func (r *declaredRun) observe(ctx context.Context, nr namedResource) (contract.Observation, error) {
	if ro, ok := nr.resource.(RequestObserver); ok {
		return ro.ObserveWithRequest(ctx, r.d)
	}
	return nr.resource.Observe(ctx, r.d.Identity)
}

// delete runs one resource's delete: through DeleteWithRequest, handed the
// verb's Desired, when the resource implements RequestDeleter, and through
// Delete, handed its Identity alone, otherwise.
func (r *declaredRun) delete(ctx context.Context, nr namedResource) error {
	if rd, ok := nr.resource.(RequestDeleter); ok {
		return rd.DeleteWithRequest(ctx, r.d)
	}
	return nr.resource.Delete(ctx, r.d.Identity)
}

// observed turns an observation into its observe outcome, with the
// statusDetail scrubbed of request secrets. Outputs carrying a request
// secret are refused rather than reported.
func (r *declaredRun) observed(obs contract.Observation) (*contract.CallOutcome, error) {
	if err := refuseSecretOutputs(obs.Outputs, r.d.Secrets); err != nil {
		return nil, err
	}
	return &contract.CallOutcome{
		State:        obs.State,
		Outputs:      obs.Outputs,
		StatusDetail: safe.Scrub(obs.StatusDetail, r.secrets...),
	}, nil
}

// fail records err as the outcome of one call on the named resource, call
// pointing at that call's field in entry, and answers the verb the way
// errorResponse classifies err: a *Refused is 400, anything else 500. The
// answer carries every resource outcome recorded so far.
func (r *declaredRun) fail(name string, entry *contract.ResourceOutcome, call **contract.CallOutcome, err error) (int, contract.Response) {
	status, resp := r.h.errorResponse(err, r.secrets)
	*call = &contract.CallOutcome{Error: resp.Error, StatusDetail: resp.StatusDetail}
	r.outcomes[name] = *entry
	resp.Resources = r.outcomes
	return status, resp
}
