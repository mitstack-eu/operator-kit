package handler

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

const (
	classUndeclaredType = "undeclared-type"
	classBlockLevelVerb = "block-level-verb"
)

// serveBlock serves a request naming one resource for validate alone, and a recorded or requested type reaches the observer even when the entry no longer lists it, since the resource was created as it.
func (r *declaredRun) serveBlock(ctx context.Context, operation string) (int, contract.Response) {
	r.refs = make(map[string]contract.ResourceRef, len(r.resources))
	if r.d.Resource != "" {
		if operation != "validate" {
			return r.h.errorResponse(Refuse(classBlockLevelVerb,
				"resource %q: a request naming one resource is served for validate alone, since the block creates and deletes its resources through its block-level handler", r.d.Resource), r.secrets)
		}
		nr := r.resources[0]
		r.refs[nr.name] = r.refFor(nr, r.d.ResourceType, r.d.ResourceIdentity)
		return r.validateEach(ctx)
	}

	r.provided = r.h.provided
	switch operation {
	case "subscribe", "upgrade":
		reported, err := r.ensureBlock(ctx)
		if err != nil {
			return r.h.errorResponse(err, r.secrets)
		}
		r.d.VerbAt = r.h.now()
		r.setRefs(reported)
		return r.ensureEach(ctx)
	case "unsubscribe":
		outputs, err := r.h.resource.Delete(ctx, r.d)
		if err != nil {
			return r.h.errorResponse(err, r.secrets)
		}
		r.deleteOutputs = outputs
		r.d.VerbAt = r.h.now()
		r.setRefs(nil)
		return r.deleteEach(ctx)
	case "validate":
		replaces, err := r.planReplacements(ctx)
		if err != nil {
			return r.h.errorResponse(err, r.secrets)
		}
		r.setRefs(nil)
		status, resp := r.validateEach(ctx)
		if status == http.StatusOK {
			resp.Replaces = replaces
		}
		return status, resp
	default:
		return http.StatusInternalServerError, contract.Response{Error: "handler: unreachable — unknown operation " + operation}
	}
}

// ensureBlock returns what the ensure reported by resource name, refusing a
// resource undeclared, of an unlisted type, or with a secret in its identity.
func (r *declaredRun) ensureBlock(ctx context.Context) (map[string]contract.CreatedResource, error) {
	reporter, ok := r.h.resource.(ResourceReporter)
	if !ok {
		return nil, r.h.resource.Ensure(ctx, r.d)
	}
	created, err := reporter.EnsureResources(ctx, r.d)
	if err != nil {
		return nil, err
	}
	reported := make(map[string]contract.CreatedResource, len(created))
	for _, c := range created {
		nr, ok := r.h.declaredResource(c.Name)
		if !ok {
			return nil, Refuse(classUndeclaredResource,
				"the block reported resource %q, which the manifest's spec.resources does not declare", c.Name)
		}
		if !slices.Contains(nr.declaration.Types, c.Type) {
			return nil, typeNotListed(nr, c.Type)
		}
		if err := refuseSecretOutputs(c.Identity, r.d.Secrets); err != nil {
			return nil, err
		}
		reported[c.Name] = c
	}
	return reported, nil
}

func (r *declaredRun) planReplacements(ctx context.Context) ([]string, error) {
	planner, ok := r.h.resource.(ReplacementPlanner)
	if !ok {
		return nil, nil
	}
	replaces, err := planner.PlanReplacements(ctx, r.d)
	if err != nil {
		return nil, err
	}
	for _, name := range replaces {
		if _, ok := r.h.declaredResource(name); !ok {
			return nil, Refuse(classUndeclaredResource,
				"the block plans to replace resource %q, which the manifest's spec.resources does not declare", name)
		}
	}
	return replaces, nil
}

func (r *declaredRun) setRefs(reported map[string]contract.CreatedResource) {
	for _, nr := range r.resources {
		c := reported[nr.name]
		r.refs[nr.name] = r.refFor(nr, c.Type, c.Identity)
	}
}

// refFor falls back to the engine's record, then to the one type nr's
// entry lists.
func (r *declaredRun) refFor(nr namedResource, typ string, identity map[string]string) contract.ResourceRef {
	rec := r.d.RecordedResources[nr.name]
	if typ == "" {
		typ = rec.Type
	}
	if typ == "" {
		typ = nr.declaration.Type
	}
	if identity == nil {
		identity = rec.Identity
	}
	return contract.ResourceRef{Name: nr.name, Type: typ, Identity: identity, Desired: r.d}
}

func typeNotListed(nr namedResource, typ string) error {
	return Refuse(classUndeclaredType, "resource %q is of type %q, which its manifest entry does not list (it lists %s)",
		nr.name, typ, strings.Join(nr.declaration.Types, ", "))
}
