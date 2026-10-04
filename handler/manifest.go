package handler

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mitstack-eu/operator-kit/contract"
)

// errNilResource is returned by New when neither Config.Resource nor
// Config.Resources is set.
var errNilResource = errors.New("handler: Config.Resource is required, or Config.Resources for a block whose manifest declares its resources")

// errResourceAndResources is returned by New when Config.Resource and
// Config.Resources are both set, since a block is served one way or the
// other.
var errResourceAndResources = errors.New("handler: Config.Resource and Config.Resources are both set; a block sets Resource, or Resources when its manifest declares its resources, never both")

// errEmptyJWKSURL is returned by New when Config.JWKSURL is empty.
// webhook.AuthenticateHook rejects every request on an empty JWKSURL before
// it ever consults Config.PublicKey, so PublicKey is never a substitute for
// it.
var errEmptyJWKSURL = errors.New("handler: Config.JWKSURL is required")

// knownHooks is the closed set of hook points New will ever accept a
// registration for. It comes from the contract so the handler and the
// platform's manifest validation share one set.
var knownHooks = contract.LifecycleHookPoints()

// manifestDeclarations is what New reads of a block's manifest: its
// apiVersion, the hook points spec.lifecycle.hooks declares,
// spec.lifecycle.retainsOnUnsubscribe, the resources spec.resources
// declares with their types and inputs, and the names of the params
// spec.params marks secret. A mitstack/v1beta lifecycle has neither hooks
// nor retainsOnUnsubscribe, so a v1beta manifest declares no hook and does
// not retain its resource; only a v1beta manifest declares resources and
// params.
type manifestDeclarations struct {
	apiVersion           string
	hooks                []string
	retainsOnUnsubscribe bool
	resources            []contract.ManifestResource
	secretParams         map[string]bool
}

// readManifestDeclarations reads Config.Manifest through
// contract.ParseManifest. An empty manifest declares nothing. A manifest
// the parser refuses is an error carrying the parser's message, since a
// declaration nobody can read must not be mistaken for an absent one.
func readManifestDeclarations(manifest []byte) (manifestDeclarations, error) {
	if len(manifest) == 0 {
		return manifestDeclarations{}, nil
	}
	m, err := contract.ParseManifest(manifest)
	if err != nil {
		return manifestDeclarations{}, fmt.Errorf("handler: Config.Manifest is not a manifest the kit's parser accepts: %w", err)
	}
	if m.Block != nil {
		secretParams := map[string]bool{}
		for _, p := range m.Block.Spec.Params {
			if p.Secret {
				secretParams[p.Name] = true
			}
		}
		return manifestDeclarations{
			apiVersion:   m.APIVersion,
			resources:    m.Block.Spec.Resources,
			secretParams: secretParams,
		}, nil
	}
	if m.V1Alpha == nil || m.V1Alpha.Spec.Lifecycle == nil {
		return manifestDeclarations{apiVersion: m.APIVersion}, nil
	}
	l := m.V1Alpha.Spec.Lifecycle
	return manifestDeclarations{apiVersion: m.APIVersion, hooks: l.Hooks, retainsOnUnsubscribe: l.RetainsOnUnsubscribe}, nil
}

// namedResource is one declared resource, the name the manifest gives it,
// and its manifest declaration, which holds its type and inputs.
type namedResource struct {
	name        string
	resource    DeclaredResource
	declaration contract.ManifestResource
}

// declaredResources pairs each resource the manifest's spec.resources
// declares with its handler in Config.Resources, in creation order. It
// refuses Hooks set alongside Resources, a Resources with no manifest or
// with a manifest that is not contract.ManifestAPIVersion, a declared
// resource with no handler, and a handler under a name the manifest does
// not declare, quoting the name.
func declaredResources(cfg Config, declared manifestDeclarations) ([]namedResource, error) {
	if len(cfg.Hooks) > 0 {
		return nil, errors.New("handler: Config.Hooks is set together with Config.Resources; a block that declares its resources registers no hooks")
	}
	if declared.apiVersion == "" {
		return nil, errors.New("handler: Config.Resources is set but Config.Manifest is empty; a resource can only be handled if the manifest declares it under spec.resources")
	}
	if declared.apiVersion != contract.ManifestAPIVersion {
		return nil, fmt.Errorf("handler: Config.Resources is set but the manifest is %s; only a %s manifest declares resources under spec.resources",
			declared.apiVersion, contract.ManifestAPIVersion)
	}

	declaredSet := make(map[string]bool, len(declared.resources))
	out := make([]namedResource, 0, len(declared.resources))
	for _, decl := range declared.resources {
		declaredSet[decl.Name] = true
		r := cfg.Resources[decl.Name]
		if r == nil {
			return nil, fmt.Errorf("handler: the manifest declares resource %q, but Config.Resources has no handler for it", decl.Name)
		}
		out = append(out, namedResource{name: decl.Name, resource: r, declaration: decl})
	}

	var undeclared []string
	for name := range cfg.Resources {
		if !declaredSet[name] {
			undeclared = append(undeclared, strconv.Quote(name))
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		return nil, fmt.Errorf("handler: Config.Resources has a handler for %s, which the manifest's spec.resources does not declare",
			strings.Join(undeclared, ", "))
	}
	return out, nil
}

// typedResources copies Config.Types, refusing it on a block served over
// Resource, a nil handler, and a key that is not a catalogued resource-type
// id, quoting each such key.
func typedResources(cfg Config) (map[string]DeclaredResource, error) {
	if len(cfg.Types) == 0 {
		return nil, nil
	}
	if cfg.Resource != nil {
		return nil, errors.New("handler: Config.Types is set together with Config.Resource; only a block that declares its resources is served by resource type")
	}

	ids := make([]string, 0, len(cfg.Types))
	var unknown []string
	for id := range cfg.Types {
		ids = append(ids, id)
		if _, ok := contract.LookupResourceType(id); !ok {
			unknown = append(unknown, strconv.Quote(id))
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("handler: Config.Types has a handler for %s, which the resource-type catalogue does not hold",
			strings.Join(unknown, ", "))
	}

	sort.Strings(ids)
	out := make(map[string]DeclaredResource, len(ids))
	for _, id := range ids {
		if cfg.Types[id] == nil {
			return nil, fmt.Errorf("handler: Config.Types holds no handler for resource type %q", id)
		}
		out[id] = cfg.Types[id]
	}
	return out, nil
}

// validateHooks refuses a Config that would let a block override an
// invariant through Hooks: a registration under a name that is not one of
// the four HookXxx constants, a registration for a hook declared does not
// name, or a non-empty Hooks with no Manifest to check it against. declared
// is the manifest's spec.lifecycle.hooks, which is refused when it names a
// hook outside knownHooks or names one hook twice, even with no Hooks
// registered at all, so that New refuses exactly the declarations the
// platform's manifest validation refuses.
func validateHooks(cfg Config, declared []string) error {
	if len(cfg.Hooks) > 0 && len(cfg.Manifest) == 0 {
		return errors.New("handler: Config.Hooks is set but Config.Manifest is empty — a hook can only be registered if the manifest declares it")
	}

	knownSet := make(map[string]bool, len(knownHooks))
	for _, h := range knownHooks {
		knownSet[h] = true
	}
	declaredSet := make(map[string]bool, len(declared))
	for _, h := range declared {
		if !knownSet[h] {
			return fmt.Errorf("handler: manifest's spec.lifecycle.hooks declares unknown hook point %q; the known hook points are %s",
				h, strings.Join(knownHooks, ", "))
		}
		if declaredSet[h] {
			return fmt.Errorf("handler: manifest's spec.lifecycle.hooks declares hook %q more than once", h)
		}
		declaredSet[h] = true
	}

	for name := range cfg.Hooks {
		if !knownSet[name] {
			return fmt.Errorf("handler: Config.Hooks registers unknown hook point %q; the known hook points are %s",
				name, strings.Join(knownHooks, ", "))
		}
		if !declaredSet[name] {
			return fmt.Errorf("handler: Config.Hooks registers hook %q, but the manifest's spec.lifecycle.hooks does not declare it (declares: %s)",
				name, strings.Join(declared, ", "))
		}
	}
	return nil
}

// ManifestAppNameNaming reads a block manifest's spec.naming.appName
// declaration (contract.AppNameNamingField) through contract.ParseManifest,
// which requires its maxLength and each derived name's template and limit.
// A manifest that declares no spec.naming.appName returns nil and no error;
// a mitstack/v1beta manifest has no spec.naming, so it declares none. A
// manifest the parser refuses, an empty one included, is an error carrying
// the parser's message, since a declaration nobody can read must not be
// mistaken for an absent one.
func ManifestAppNameNaming(manifest []byte) (*contract.AppNameNaming, error) {
	m, err := contract.ParseManifest(manifest)
	if err != nil {
		return nil, fmt.Errorf("handler: the manifest's %s is unknown, because the kit's parser refuses the manifest: %w",
			contract.AppNameNamingField, err)
	}
	if m.V1Alpha == nil || m.V1Alpha.Spec.Naming == nil {
		return nil, nil
	}
	return m.V1Alpha.Spec.Naming.AppName, nil
}
