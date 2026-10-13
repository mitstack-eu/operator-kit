package handler

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/safe"
)

const classUndeclaredResource = "undeclared-resource"

// dispatchDeclared refuses a name the manifest does not declare with a 400 before any resource is reached, unless a block-level validate names a type Config.TypeObservers holds.
//
// A resource still converging, left standing or failing stops the verb there, and the request context is checked before each resource's first call. Every answer, an error included, carries the resources map of the resources reached.
func (h *Handler) dispatchDeclared(ctx context.Context, operation string, d contract.Desired, secrets []string) (int, contract.Response) {
	resources := h.declared
	if d.Resource != "" {
		nr, ok := h.declaredResource(d.Resource)
		if !ok {
			nr, ok = h.typeObserved(operation, d)
		}
		if !ok {
			return h.errorResponse(Refuse(classUndeclaredResource,
				"resource %q (type %q) is not declared under the manifest's spec.resources", d.Resource, d.ResourceType), secrets)
		}
		resources = []namedResource{nr}
	}

	run := &declaredRun{h: h, d: d, secrets: secrets, resources: resources, outcomes: map[string]contract.ResourceOutcome{}}
	if h.blockLevel {
		return run.serveBlock(ctx, operation)
	}
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

func (h *Handler) typeObserved(operation string, d contract.Desired) (namedResource, bool) {
	if !h.blockLevel || operation != "validate" {
		return namedResource{}, false
	}
	o, ok := h.typeObservers[d.ResourceType]
	if !ok {
		return namedResource{}, false
	}
	decl := contract.ManifestResource{Name: d.Resource, Type: d.ResourceType, Types: []string{d.ResourceType}}
	return namedResource{name: d.Resource, observer: o, declaration: decl}, true
}

func (h *Handler) declaredResource(name string) (namedResource, bool) {
	for _, nr := range h.declared {
		if nr.name == name {
			return nr, true
		}
	}
	return namedResource{}, false
}

// declaredRun's refs, for a block-level block whose handler has already ensured or deleted the whole block, hold what each observer is handed, and provided limits the answer's outputs.
type declaredRun struct {
	h         *Handler
	d         contract.Desired
	secrets   []string
	resources []namedResource
	outcomes  map[string]contract.ResourceOutcome
	refs      map[string]contract.ResourceRef
	provided  map[string]bool

	resourceSecretOutputs map[string]map[string]string
}

func (r *declaredRun) answers(name string) bool {
	return r.provided == nil || r.provided[name]
}

// keepSecretOutputs holds a resource's secret outputs apart from outcomes, which the engine records.
func (r *declaredRun) keepSecretOutputs(name string, secretOutputs map[string]string) {
	if len(secretOutputs) == 0 {
		return
	}
	if r.resourceSecretOutputs == nil {
		r.resourceSecretOutputs = map[string]map[string]string{}
	}
	r.resourceSecretOutputs[name] = maps.Clone(secretOutputs)
}

// ensureEach settles each resource present before the next, a later resource winning a key both produce, and ensures none on its own for a block-level block.
func (r *declaredRun) ensureEach(ctx context.Context) (int, contract.Response) {
	var outputs, secretOutputs map[string]string
	for _, nr := range r.resources {
		if ctx.Err() != nil {
			return http.StatusOK, contract.Response{Pending: true, StatusDetail: r.notReached(nr.name), Resources: r.outcomes, SecretOutputs: secretOutputs, ResourceSecretOutputs: r.resourceSecretOutputs}
		}
		var entry contract.ResourceOutcome
		if r.refs == nil {
			ensured, err := r.ensure(ctx, nr)
			if err != nil {
				return r.fail(nr.name, &entry, &entry.Ensure, err)
			}
			entry.Ensure = ensured
		}

		obs, attempts, err := r.settle(ctx, nr, contract.Present, &entry)
		if err != nil {
			return r.fail(nr.name, &entry, &entry.Observe, err)
		}
		r.outcomes[nr.name] = entry
		if obs.State != contract.Present {
			return http.StatusOK, contract.Response{
				Pending:               true,
				StatusDetail:          safe.Scrub(fmt.Sprintf("resource %q still %s after %d attempts", nr.name, obs.State, attempts), r.secrets...),
				Resources:             r.outcomes,
				SecretOutputs:         secretOutputs,
				ResourceSecretOutputs: r.resourceSecretOutputs,
			}
		}
		if r.answers(nr.name) {
			outputs = mergeOutputs(outputs, obs.Outputs)
			secretOutputs = mergeOutputs(secretOutputs, obs.SecretOutputs)
			r.keepSecretOutputs(nr.name, obs.SecretOutputs)
		}
	}

	resp := contract.Observation{State: contract.Present, Outputs: outputs, SecretOutputs: secretOutputs}.Response()
	resp.Resources = r.outcomes
	resp.ResourceSecretOutputs = r.resourceSecretOutputs
	return http.StatusOK, resp
}

// ensure refuses a reported identity or output value equal to a request secret rather than reporting it.
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

// deleteEach settles each resource absent in reverse creation order so one created earlier outlives every one created after it, deletes none on its own for a block-level block, and observes none whose type ends with its subscription.
func (r *declaredRun) deleteEach(ctx context.Context) (int, contract.Response) {
	var outputs map[string]string
	for i := len(r.resources) - 1; i >= 0; i-- {
		nr := r.resources[i]
		if ctx.Err() != nil {
			absent := false
			return http.StatusOK, contract.Response{Absent: &absent, StatusDetail: r.notReached(nr.name), Resources: r.outcomes}
		}
		var entry contract.ResourceOutcome
		if r.refs == nil {
			if err := r.delete(ctx, nr); err != nil {
				return r.fail(nr.name, &entry, &entry.Delete, err)
			}
			entry.Delete = &contract.CallOutcome{}
		}
		if ended, ok := r.endedWithSubscription(nr); ok {
			entry.Delete = ended
			r.outcomes[nr.name] = entry
			continue
		}

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
		if r.answers(nr.name) {
			outputs = mergeOutputs(outputs, obs.Outputs)
		}
	}

	absent := true
	return http.StatusOK, contract.Response{Absent: &absent, Outputs: outputs, Resources: r.outcomes}
}

// endedWithSubscription records nr absent by its delete, not by an observe, when Config.Types marks its type EndsWithSubscription.
func (r *declaredRun) endedWithSubscription(nr namedResource) (*contract.CallOutcome, bool) {
	ref := r.refFor(nr, r.refs[nr.name].Type, r.refs[nr.name].Identity)
	t, ok := r.h.types.Lookup(ref.Type)
	if !ok || !t.EndsWithSubscription {
		return nil, false
	}
	return &contract.CallOutcome{
		State:        contract.Absent,
		Type:         ref.Type,
		Identity:     ref.Identity,
		StatusDetail: fmt.Sprintf("type %s ends with its subscription, so the resource is absent once the delete succeeds, unobserved", ref.Type),
	}, true
}

// validateEach never polls, and an Observe error is classified, never folded into pending, and stops the probe there.
func (r *declaredRun) validateEach(ctx context.Context) (int, contract.Response) {
	var outputs, secretOutputs map[string]string
	present, absent := 0, 0
	states := make([]string, 0, len(r.resources))
	for _, nr := range r.resources {
		if ctx.Err() != nil {
			return http.StatusOK, contract.Response{Pending: true, StatusDetail: r.notReached(nr.name), Resources: r.outcomes, SecretOutputs: secretOutputs, ResourceSecretOutputs: r.resourceSecretOutputs}
		}
		var entry contract.ResourceOutcome
		obs, err := r.observe(ctx, nr)
		if err != nil {
			return r.fail(nr.name, &entry, &entry.Observe, err)
		}
		call, err := r.observed(nr, obs)
		if err != nil {
			return r.fail(nr.name, &entry, &entry.Observe, err)
		}
		entry.Observe = call
		r.outcomes[nr.name] = entry

		switch obs.State {
		case contract.Present:
			present++
			if r.answers(nr.name) {
				outputs = mergeOutputs(outputs, obs.Outputs)
				secretOutputs = mergeOutputs(secretOutputs, obs.SecretOutputs)
				r.keepSecretOutputs(nr.name, obs.SecretOutputs)
			}
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
			Pending:       true,
			StatusDetail:  safe.Scrub("resources observed "+strings.Join(states, ", "), r.secrets...),
			SecretOutputs: secretOutputs,
		}
	}
	resp.Resources = r.outcomes
	resp.ResourceSecretOutputs = r.resourceSecretOutputs
	return http.StatusOK, resp
}

func (r *declaredRun) notReached(name string) string {
	return safe.Scrub(fmt.Sprintf("resource %q not reached: the request ended before the verb got to it", name), r.secrets...)
}

// settle paces Observe as settlePresent and settleAbsent do, and records no observe outcome when no attempt ran.
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
	call, err := r.observed(nr, last)
	if err != nil {
		return last, attempts, err
	}
	entry.Observe = call
	return last, attempts, nil
}

func (r *declaredRun) observe(ctx context.Context, nr namedResource) (contract.Observation, error) {
	if nr.observer != nil {
		return nr.observer.ObserveResource(ctx, r.refs[nr.name])
	}
	if ro, ok := nr.resource.(RequestObserver); ok {
		return ro.ObserveWithRequest(ctx, r.d)
	}
	return nr.resource.Observe(ctx, r.d.Identity)
}

func (r *declaredRun) delete(ctx context.Context, nr namedResource) error {
	if rd, ok := nr.resource.(RequestDeleter); ok {
		return rd.DeleteWithRequest(ctx, r.d)
	}
	return nr.resource.Delete(ctx, r.d.Identity)
}

// observed refuses outputs carrying a request secret rather than reporting them.
func (r *declaredRun) observed(nr namedResource, obs contract.Observation) (*contract.CallOutcome, error) {
	if err := refuseSecretOutputs(obs.Outputs, r.d.Secrets); err != nil {
		return nil, err
	}
	ref := r.refs[nr.name]
	return &contract.CallOutcome{
		State:        obs.State,
		Outputs:      obs.Outputs,
		StatusDetail: safe.Scrub(obs.StatusDetail, r.secrets...),
		Identity:     ref.Identity,
		Type:         ref.Type,
	}, nil
}

// fail answers as errorResponse classifies err, carrying every resource outcome recorded so far.
func (r *declaredRun) fail(name string, entry *contract.ResourceOutcome, call **contract.CallOutcome, err error) (int, contract.Response) {
	status, resp := r.h.errorResponse(err, r.secrets)
	*call = &contract.CallOutcome{Error: resp.Error, StatusDetail: resp.StatusDetail}
	r.outcomes[name] = *entry
	resp.Resources = r.outcomes
	return status, resp
}
