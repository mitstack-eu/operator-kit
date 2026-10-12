package handler

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mitstack-eu/operator-kit/contract"
)

var errNilResource = errors.New("handler: Config.Resource is required, or Config.Resources for a block whose manifest declares its resources")

var errResourceAndResources = errors.New("handler: Config.Resource and Config.Resources are both set; a block sets Resource, or Resources when its manifest declares its resources, never both")

var errObserversAndResources = errors.New("handler: Config.Observers and Config.Resources are both set; a block served over Config.Resource observes each declared resource through Config.Observers, and a DeclaredResource observes itself")

var errObserversWithoutManifest = fmt.Errorf("handler: Config.Observers is set but Config.Manifest is not a %s manifest; an observer can only serve a resource the manifest declares under spec.resources", contract.ManifestAPIVersion)

var errTypeObserversAndResources = errors.New("handler: Config.TypeObservers and Config.Resources are both set; a type observer serves a block served over Config.Resource, and a DeclaredResource observes itself")

var errTypeObserversWithoutManifest = fmt.Errorf("handler: Config.TypeObservers is set but Config.Manifest is not a %s manifest; a type observer serves a resource a block-level block's earlier manifest declared", contract.ManifestAPIVersion)

// errEmptyJWKSURL stands because webhook.AuthenticateHook rejects every request on an empty JWKSURL before consulting PublicKey.
var errEmptyJWKSURL = errors.New("handler: Config.JWKSURL is required")

// knownHooks comes from the contract so the handler and the platform's manifest validation share one set.
var knownHooks = contract.LifecycleHookPoints()

// manifestDeclarations holds what New reads of a manifest; only a v1beta manifest declares resources and params, and it declares no hook and does not retain its resource.
type manifestDeclarations struct {
	apiVersion           string
	hooks                []string
	retainsOnUnsubscribe bool
	resources            []contract.ManifestResource
	provided             map[string]bool
	secretParams         map[string]bool
}

// readManifestDeclarations errors on a manifest the parser refuses, since a declaration nobody can read must not be mistaken for an absent one.
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
		provided := make(map[string]bool, len(m.Block.Spec.Provides))
		for _, name := range m.Block.Spec.Provides {
			provided[name] = true
		}
		return manifestDeclarations{
			apiVersion:   m.APIVersion,
			resources:    m.Block.Spec.Resources,
			provided:     provided,
			secretParams: secretParams,
		}, nil
	}
	if m.V1Alpha == nil || m.V1Alpha.Spec.Lifecycle == nil {
		return manifestDeclarations{apiVersion: m.APIVersion}, nil
	}
	l := m.V1Alpha.Spec.Lifecycle
	return manifestDeclarations{apiVersion: m.APIVersion, hooks: l.Hooks, retainsOnUnsubscribe: l.RetainsOnUnsubscribe}, nil
}

// namedResource of a block-level block has an observer and no DeclaredResource.
type namedResource struct {
	name        string
	resource    DeclaredResource
	observer    Observer
	declaration contract.ManifestResource
}

// observedResources refuses a declared resource with no observer and an observer under an undeclared name, and validateHooks refuses any hook since a v1beta manifest declares none.
func observedResources(cfg Config, declared manifestDeclarations) ([]namedResource, error) {
	if err := validateHooks(cfg, declared.hooks); err != nil {
		return nil, err
	}
	declaredSet := make(map[string]bool, len(declared.resources))
	out := make([]namedResource, 0, len(declared.resources))
	for _, decl := range declared.resources {
		declaredSet[decl.Name] = true
		o := cfg.Observers[decl.Name]
		if o == nil {
			return nil, fmt.Errorf("handler: the manifest declares resource %q, but Config.Observers has no observe for it; every declared resource is observed through its own Observer", decl.Name)
		}
		out = append(out, namedResource{name: decl.Name, observer: o, declaration: decl})
	}
	if undeclared := undeclaredKeys(cfg.Observers, declaredSet); undeclared != "" {
		return nil, fmt.Errorf("handler: Config.Observers has an observer for %s, which the manifest's spec.resources does not declare", undeclared)
	}
	return out, nil
}

func validateTypeObservers(observers map[string]Observer) error {
	keys := make([]string, 0, len(observers))
	for typ := range observers {
		keys = append(keys, typ)
	}
	sort.Strings(keys)
	for _, typ := range keys {
		if _, _, _, err := contract.ParseResourceTypeID(typ); err != nil {
			return fmt.Errorf("handler: Config.TypeObservers has an observer under %q, which is not a resource type id: %w", typ, err)
		}
		if observers[typ] == nil {
			return fmt.Errorf("handler: Config.TypeObservers has a nil observer under %q, so a resource of that type could never be observed", typ)
		}
	}
	return nil
}

func undeclaredKeys[V any](m map[string]V, declared map[string]bool) string {
	var undeclared []string
	for name := range m {
		if !declared[name] {
			undeclared = append(undeclared, strconv.Quote(name))
		}
	}
	sort.Strings(undeclared)
	return strings.Join(undeclared, ", ")
}

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

	if undeclared := undeclaredKeys(cfg.Resources, declaredSet); undeclared != "" {
		return nil, fmt.Errorf("handler: Config.Resources has a handler for %s, which the manifest's spec.resources does not declare", undeclared)
	}
	return out, nil
}

// validateHooks refuses a declaration naming an unknown hook or one twice even with no Hooks registered, so New refuses exactly what the platform's manifest validation refuses.
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

// ManifestAppNameNaming returns nil without error for a manifest declaring no spec.naming.appName, and errors on one the parser refuses, since a declaration nobody can read must not be mistaken for an absent one.
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
