package kittest

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/handler"
)

// SuppliedResource is a resource the app supplies to a block rather than
// one the block creates, such as a repository the block's role trusts. The
// check only observes it: it must stand before the block creates anything
// and still stand after the block has deleted everything it created.
type SuppliedResource interface {
	Observe(ctx context.Context, id contract.Identity) (contract.Observation, error)
}

// DeclaredResources is what a block whose mitstack/v1beta manifest declares
// its resources supplies to have each resource checked.
type DeclaredResources struct {
	// Manifest is the block's manifest.yaml, read through
	// contract.ParseManifest for the resources spec.resources declares, in
	// creation order.
	Manifest []byte
	// Resources holds one handler for each declared resource, keyed by its
	// name, exactly as handler.Config.Resources does.
	Resources map[string]handler.DeclaredResource
	// Supplied holds the resources the app supplies to the block, keyed by
	// a name the problems quote. Optional.
	Supplied map[string]SuppliedResource
	// Desired is the subscription every call is made for. Ensure,
	// ObserveWithRequest and DeleteWithRequest receive it whole; Observe
	// and Delete receive its Identity.
	Desired contract.Desired
	// Resource names one declared resource to drive alone, the way the
	// engine drives a request naming it, and Ensure then receives Desired
	// with its Resource set to this name. Optional; empty drives every
	// declared resource.
	Resource string
	// ResourceInputs are the inputs recorded for the driven resource at
	// subscribe, set on Desired's ResourceInputs, the way the engine hands
	// them on a request naming it. Optional; nil leaves Desired's own. Set
	// with no Resource named, here or on Desired, it is one problem and
	// nothing is driven.
	ResourceInputs map[string]json.RawMessage
	// ResourceIdentity is the identity the driven resource's ensure stored,
	// set on Desired's ResourceIdentity, the way the engine hands it on a
	// later request naming it. Optional; nil leaves Desired's own. Set with
	// no Resource named, here or on Desired, it is one problem and nothing
	// is driven.
	ResourceIdentity map[string]string
	// Types holds a handler for each resource type the block can still
	// delete once its manifest stops declaring a resource of that type,
	// keyed by resource-type id, exactly as handler.Config.Types does.
	Types map[string]handler.DeclaredResource
	// ResourceType is the recorded type of the resource Resource names when
	// the manifest no longer declares it, set on Desired's ResourceType.
	ResourceType string
}

// CheckDeclaredResources drives each declared resource through its
// lifecycle and returns one problem per violation, empty when the block
// conforms. In order, it:
//
//   - observes each supplied resource once, which must be present;
//   - for each declared resource in manifest order, calls Ensure, Observe
//     (present), Ensure again and Observe again (present, with the same
//     outputs and secret outputs as the first), so a repeated ensure that
//     changes what Observe reports is caught;
//   - for each resource it ensured, in reverse manifest order, calls Delete
//     then Observe, which must report absent: the absence proof;
//   - observes each supplied resource again, which must still be present,
//     since a block never deletes what the app supplies.
//
// With DeclaredResources.Resource set, the creation pass and the teardown
// drive that one resource alone, under a Desired naming it, as the engine
// does for a request naming a resource; a name the manifest does not
// declare is one problem, and nothing is driven. DeclaredResources'
// ResourceInputs and ResourceIdentity, when set, go on the Desired every
// call is made for. A Desired that carries either with no Resource named
// is one problem naming the fields set, and nothing is driven, supplied
// resources included, since the engine never sends such a request. A
// resource implementing handler.ReportingResource is ensured through
// EnsureWithReport, never Ensure, and a second ensure reporting a different
// identity from the first is a problem, since it means the ensure created
// another resource rather than converging the one it stored. A resource
// implementing handler.RequestObserver is observed through
// ObserveWithRequest, and one implementing handler.RequestDeleter is
// deleted through DeleteWithRequest, each handed that Desired; any other is
// observed and deleted with its Identity alone.
//
// A resource that fails stops the creation pass there, and a resource not
// proved absent stops the teardown there, so a resource created earlier is
// never deleted while a later one still stands. A missing handler, a
// handler for an undeclared resource, a manifest that is not
// mitstack/v1beta, one declaring no resources and one the parser refuses
// are each a problem, and nothing is driven. Each problem about a resource
// begins `resource "<name>": ` and each about a supplied resource begins
// `supplied resource "<name>": `.
func CheckDeclaredResources(in DeclaredResources) []string {
	order, problems := pairDeclaredResources(in)
	if len(problems) > 0 {
		return problems
	}

	desired := in.Desired
	if in.ResourceInputs != nil {
		desired.ResourceInputs = in.ResourceInputs
	}
	if in.ResourceIdentity != nil {
		desired.ResourceIdentity = in.ResourceIdentity
	}
	if in.Resource != "" {
		if !slices.Contains(order, in.Resource) {
			return []string{fmt.Sprintf("resource %q: the manifest's spec.resources does not declare it, so there is no resource to drive", in.Resource)}
		}
		order = []string{in.Resource}
		desired.Resource = in.Resource
	}
	if p := unboundResourceFields(desired); p != "" {
		return []string{p}
	}

	c := &declaredCheck{in: in, desired: desired, ctx: context.Background()}
	supplied := sortedKeys(in.Supplied)
	if !c.suppliedStand(supplied, "before the first ensure") {
		return c.problems
	}

	ensured := 0
	for _, name := range order {
		ensured++
		if !c.ensureTwice(name) {
			break
		}
	}
	for i := ensured - 1; i >= 0; i-- {
		if !c.deleteAndProveAbsent(order[i]) {
			break
		}
	}
	c.suppliedStand(supplied, "after the last delete")
	return c.problems
}

// AssertDeclaredResources runs CheckDeclaredResources and fails tb with one
// error per problem, in the order the check found them.
func AssertDeclaredResources(tb testing.TB, in DeclaredResources) {
	tb.Helper()
	for _, p := range CheckDeclaredResources(in) {
		tb.Error(p)
	}
}

// unboundResourceFields returns the problem for a Desired that carries
// resource inputs or a resource identity but names no resource, naming only
// the fields set, and "" when there is none. contract.DecodeRequest refuses
// such a request, so the engine never sends one.
func unboundResourceFields(d contract.Desired) string {
	if d.Resource != "" {
		return ""
	}
	var fields []string
	if len(d.ResourceInputs) > 0 {
		fields = append(fields, "ResourceInputs")
	}
	if len(d.ResourceIdentity) > 0 {
		fields = append(fields, "ResourceIdentity")
	}
	switch len(fields) {
	case 0:
		return ""
	case 1:
		return fields[0] + " needs a non-empty Resource naming the resource it is for: the engine never sends it with no resource named, so nothing is driven"
	default:
		return strings.Join(fields, " and ") + " need a non-empty Resource naming the resource they are for: the engine never sends them with no resource named, so nothing is driven"
	}
}

// pairDeclaredResources reads the manifest's spec.resources and returns the
// declared names in creation order, or the problems that leave the check
// nothing it may drive.
func pairDeclaredResources(in DeclaredResources) ([]string, []string) {
	m, err := contract.ParseManifest(in.Manifest)
	if err != nil {
		return nil, []string{fmt.Sprintf("the manifest is not one the kit's parser accepts, so its resources are unknown: %v", err)}
	}
	if m.Block == nil {
		return nil, []string{fmt.Sprintf("the manifest is %s, but only a %s manifest declares resources under spec.resources, so there are none to drive",
			m.APIVersion, contract.ManifestAPIVersion)}
	}
	if len(m.Block.Spec.Resources) == 0 {
		return nil, []string{"the manifest declares no resources under spec.resources, so the check has nothing to prove"}
	}

	var problems []string
	order := make([]string, 0, len(m.Block.Spec.Resources))
	declared := make(map[string]bool, len(m.Block.Spec.Resources))
	for _, r := range m.Block.Spec.Resources {
		order = append(order, r.Name)
		declared[r.Name] = true
		if in.Resources[r.Name] == nil {
			problems = append(problems, fmt.Sprintf("resource %q: the manifest declares it, but Resources has no handler for it", r.Name))
		}
	}
	for _, name := range sortedKeys(in.Resources) {
		if !declared[name] {
			problems = append(problems, fmt.Sprintf("resource %q: Resources has a handler for it, but the manifest's spec.resources does not declare it", name))
		}
	}
	return order, problems
}

// declaredCheck is one run of CheckDeclaredResources, collecting problems.
// desired is the Desired every call is made for: the one the check was
// given, naming the driven resource when the check drives one alone.
type declaredCheck struct {
	in       DeclaredResources
	desired  contract.Desired
	ctx      context.Context
	problems []string
}

func (c *declaredCheck) resourceProblem(name, format string, args ...any) {
	c.problems = append(c.problems, fmt.Sprintf("resource %q: ", name)+fmt.Sprintf(format, args...))
}

// suppliedStand observes each supplied resource once and reports whether
// every one is present, recording a problem for each that is not. when
// says at which point of the check it was observed.
func (c *declaredCheck) suppliedStand(names []string, when string) bool {
	ok := true
	for _, name := range names {
		obs, err := c.in.Supplied[name].Observe(c.ctx, c.desired.Identity)
		switch {
		case err != nil:
			c.problems = append(c.problems, fmt.Sprintf("supplied resource %q: Observe %s failed: %v", name, when, err))
			ok = false
		case obs.State != contract.Present:
			c.problems = append(c.problems, fmt.Sprintf(
				"supplied resource %q: observed %s %s, want present: the app supplies it, so the block must find it standing and never delete it",
				name, obs.State, when))
			ok = false
		}
	}
	return ok
}

// ensureTwice ensures one resource, observes it present, ensures it again
// and observes it present with the same outputs and secret outputs, and,
// for a resource that reports its ensure, the same identity. It reports
// whether the resource ended present, so the creation pass may move on to
// the next.
func (c *declaredCheck) ensureTwice(name string) bool {
	r := c.in.Resources[name]

	first, firstIdentity, ok := c.ensureAndObserve(name, r, "the first")
	if !ok {
		return false
	}
	second, secondIdentity, ok := c.ensureAndObserve(name, r, "the second")
	if !ok {
		return false
	}

	if changed := changedKeys(firstIdentity, secondIdentity); len(changed) > 0 {
		c.resourceProblem(name, "the second Ensure reported a different identity from the first, changing %s; Ensure must converge the resource it stored, not create another",
			strings.Join(changed, ", "))
	}
	if !reflect.DeepEqual(first.Outputs, second.Outputs) {
		c.resourceProblem(name, "the second Ensure changed its outputs from %v to %v; Ensure must converge, not change what it created",
			first.Outputs, second.Outputs)
	}
	if changed := changedKeys(first.SecretOutputs, second.SecretOutputs); len(changed) > 0 {
		c.resourceProblem(name, "the second Ensure changed its secret outputs %s; Ensure must converge, not change what it created",
			strings.Join(changed, ", "))
	}
	return true
}

// ensureAndObserve ensures the resource then calls Observe once, recording
// a problem when either fails or the resource is not present. It returns
// the observation and the identity the ensure reported, nil for a resource
// that does not report its ensure. which names the ensure in the problem.
func (c *declaredCheck) ensureAndObserve(name string, r handler.DeclaredResource, which string) (contract.Observation, map[string]string, bool) {
	identity, err := c.ensure(r)
	if err != nil {
		c.resourceProblem(name, "%s Ensure failed: %v", which, err)
		return contract.Observation{}, nil, false
	}
	obs, err := c.observe(r)
	if err != nil {
		c.resourceProblem(name, "Observe after %s Ensure failed: %v", which, err)
		return contract.Observation{}, nil, false
	}
	if obs.State != contract.Present {
		c.resourceProblem(name, "observed %s after %s Ensure, want present", obs.State, which)
		return contract.Observation{}, nil, false
	}
	return obs, identity, true
}

// ensure ensures one resource the way the Handler does: through
// EnsureWithReport when it implements handler.ReportingResource, returning
// the identity it reported, and through Ensure otherwise.
func (c *declaredCheck) ensure(r handler.DeclaredResource) (map[string]string, error) {
	if reporting, ok := r.(handler.ReportingResource); ok {
		report, err := reporting.EnsureWithReport(c.ctx, c.desired)
		return report.Identity, err
	}
	return nil, r.Ensure(c.ctx, c.desired)
}

// observe observes one resource the way the Handler does: through
// ObserveWithRequest, handed the check's Desired, when it implements
// handler.RequestObserver, and through Observe, handed its Identity alone,
// otherwise.
func (c *declaredCheck) observe(r handler.DeclaredResource) (contract.Observation, error) {
	if ro, ok := r.(handler.RequestObserver); ok {
		return ro.ObserveWithRequest(c.ctx, c.desired)
	}
	return r.Observe(c.ctx, c.desired.Identity)
}

// delete deletes one resource the way the Handler does: through
// DeleteWithRequest, handed the check's Desired, when it implements
// handler.RequestDeleter, and through Delete, handed its Identity alone,
// otherwise.
func (c *declaredCheck) delete(r handler.DeclaredResource) error {
	if rd, ok := r.(handler.RequestDeleter); ok {
		return rd.DeleteWithRequest(c.ctx, c.desired)
	}
	return r.Delete(c.ctx, c.desired.Identity)
}

// deleteAndProveAbsent deletes one resource and observes it once, which
// must report absent. It reports whether the absence was proved, so the
// teardown may move on to the resource created before it.
func (c *declaredCheck) deleteAndProveAbsent(name string) bool {
	r := c.in.Resources[name]
	if err := c.delete(r); err != nil {
		c.resourceProblem(name, "Delete failed: %v", err)
		return false
	}
	obs, err := c.observe(r)
	if err != nil {
		c.resourceProblem(name, "Observe after Delete failed, so its absence is unproved: %v", err)
		return false
	}
	if obs.State != contract.Absent {
		c.resourceProblem(name, "observed %s after Delete, want absent: Delete must leave nothing standing", obs.State)
		return false
	}
	return true
}

// changedKeys returns, sorted and quoted, each key whose value differs
// between a and b or that only one of them holds. It names keys only, so a
// secret value never reaches a problem.
func changedKeys(a, b map[string]string) []string {
	var out []string
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			out = append(out, fmt.Sprintf("%q", k))
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, fmt.Sprintf("%q", k))
		}
	}
	sort.Strings(out)
	return out
}

// sortedKeys returns m's keys in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
