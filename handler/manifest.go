package handler

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

var errNilResource = errors.New("handler: Config.Resource is required")

var errObserversWithoutManifest = fmt.Errorf("handler: Config.Observers is set but Config.Manifest is not a %s manifest; an observer can only serve a resource the manifest declares under spec.resources", contract.ManifestAPIVersion)

var errTypeObserversWithoutManifest = fmt.Errorf("handler: Config.TypeObservers is set but Config.Manifest is not a %s manifest; a type observer serves a resource a block-level block's earlier manifest declared", contract.ManifestAPIVersion)

// errEmptyJWKSURL stands because webhook.AuthenticateHook rejects every request on an empty JWKSURL before consulting PublicKey.
var errEmptyJWKSURL = errors.New("handler: Config.JWKSURL is required")

// manifestDeclarations holds what New reads of a manifest, and is empty when Config.Manifest is.
type manifestDeclarations struct {
	apiVersion string
	resources  []contract.ManifestResource
	provided   map[string]bool
	capability bool
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
	provided := make(map[string]bool, len(m.Block.Spec.Provides))
	for _, name := range m.Block.Spec.Provides {
		provided[name] = true
	}
	return manifestDeclarations{
		apiVersion: m.APIVersion,
		resources:  m.Block.Spec.Resources,
		provided:   provided,
		capability: m.Block.Type == contract.ManifestTypeCapability,
	}, nil
}

type namedResource struct {
	name        string
	observer    Observer
	declaration contract.ManifestResource
}

// observedResources refuses a declared resource with no observer and an observer under an undeclared name.
func observedResources(cfg Config, declared manifestDeclarations) ([]namedResource, error) {
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
