package contract

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

var blockManifestType = reflect.TypeOf(BlockManifest{})

// requirementType and resourceType are read as a type id or a mapping, not as a struct of fields.
var (
	requirementType = reflect.TypeOf(ManifestRequirement{})
	resourceType    = reflect.TypeOf(ManifestResource{})
)

// v1betaDecoder refuses every null, and every plain scalar YAML reads as a number or a boolean where a string is declared.
var v1betaDecoder = manifestDecoder{version: ManifestAPIVersion, tag: "json"}

var typeIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*\.[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

var retiredEntryKeys = []string{"kind", "in", "on", "inputs"}

// decodeBlockManifest also holds what the types cannot say: type is block or capability, names and references are unique, and every provides entry names a resource.
func decodeBlockManifest(root *yamlNode) (*BlockManifest, error) {
	b := &BlockManifest{}
	if err := v1betaDecoder.decode(root, reflect.ValueOf(b).Elem(), ""); err != nil {
		return nil, err
	}
	if b.Type != ManifestTypeBlock && b.Type != ManifestTypeCapability {
		return nil, fmt.Errorf("type is %q, but the type of a %s manifest must be %q or %q",
			b.Type, ManifestAPIVersion, ManifestTypeBlock, ManifestTypeCapability)
	}
	names := make(map[string]bool, len(b.Spec.Resources))
	for i, r := range b.Spec.Resources {
		if names[r.Name] {
			return nil, fmt.Errorf("spec.resources[%d].name: %q names an earlier resource too, and a resource name appears once in a block", i, r.Name)
		}
		names[r.Name] = true
	}
	refs := make(map[string]int, len(b.Spec.Requires))
	for i, q := range b.Spec.Requires {
		ref := q.Reference()
		if j, ok := refs[ref]; ok {
			return nil, fmt.Errorf("spec.requires[%d]: %q is the reference of spec.requires[%d] too, and a requirement's reference appears once in a block", i, ref, j)
		}
		refs[ref] = i
	}
	for i, p := range b.Spec.Provides {
		if !names[p] {
			return nil, fmt.Errorf("spec.provides[%d]: %q names no resource in spec.resources", i, p)
		}
	}
	return b, nil
}

type manifestEntry struct {
	name  string
	types []string
	many  bool
}

func (d manifestDecoder) requirement(n *yamlNode, path string) (ManifestRequirement, error) {
	e, err := d.entry(n, path, true)
	if err != nil {
		return ManifestRequirement{}, err
	}
	return ManifestRequirement{Name: e.name, Types: e.types, Many: e.many}, nil
}

// resource names an entry without a name by the one type id it lists.
func (d manifestDecoder) resource(n *yamlNode, path string) (ManifestResource, error) {
	e, err := d.entry(n, path, false)
	if err != nil {
		return ManifestResource{}, err
	}
	r := ManifestResource{Name: e.name, Types: e.types}
	if len(e.types) == 1 {
		r.Type = e.types[0]
		if r.Name == "" {
			r.Name = r.Type
		}
	}
	return r, nil
}

// entry refuses a retired key by name and an entry listing several types without a name.
func (d manifestDecoder) entry(n *yamlNode, path string, requirement bool) (manifestEntry, error) {
	if n.kind == yamlScalar {
		id, err := typeID(n, path)
		return manifestEntry{types: []string{id}}, err
	}
	if n.kind != yamlMap {
		return manifestEntry{}, fmt.Errorf("%s must be a type id or a mapping of type and name", path)
	}
	if err := mapEntries(n, path); err != nil {
		return manifestEntry{}, err
	}
	var e manifestEntry
	hasType := false
	for i, key := range n.keys {
		keyPath := fieldPath(path, key)
		val := n.vals[i]
		var err error
		switch {
		case slices.Contains(retiredEntryKeys, key):
			err = fmt.Errorf("%s: %q is retired: an entry names its exact type under type, and a block creates and orders its own resources", path, key)
		case key == "type":
			hasType = true
			e.types, err = typeList(val, keyPath)
		case key == "name":
			err = d.decode(val, reflect.ValueOf(&e.name).Elem(), keyPath)
			if err == nil && e.name == "" {
				err = fmt.Errorf("%s must not be empty; leave it out to name the entry by its type", keyPath)
			}
		case key == "many" && requirement:
			err = d.decode(val, reflect.ValueOf(&e.many).Elem(), keyPath)
		default:
			err = fmt.Errorf("%s is not a field of a %s manifest", keyPath, d.version)
		}
		if err != nil {
			return manifestEntry{}, err
		}
	}
	if !hasType {
		return manifestEntry{}, fmt.Errorf("%s is required", fieldPath(path, "type"))
	}
	if len(e.types) > 1 && e.name == "" {
		return manifestEntry{}, fmt.Errorf("%s lists types %s but has no name; an entry listing more than one type needs a name to be told apart by",
			path, strings.Join(e.types, ", "))
	}
	return e, nil
}

func typeList(n *yamlNode, path string) ([]string, error) {
	if err := readable(n, path); err != nil {
		return nil, err
	}
	switch {
	case isNull(n):
		return nil, noValue(path)
	case n.kind == yamlScalar:
		id, err := typeID(n, path)
		return []string{id}, err
	case n.kind != yamlSeq:
		return nil, fmt.Errorf("%s must be a type id or a list of type ids", path)
	case len(n.items) == 0:
		return nil, fmt.Errorf("%s is required and must not be empty", path)
	}
	ids := make([]string, 0, len(n.items))
	for i, item := range n.items {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		if err := readable(item, itemPath); err != nil {
			return nil, err
		}
		if item.kind != yamlScalar || isNull(item) {
			return nil, fmt.Errorf("%s must be a type id", itemPath)
		}
		id, err := typeID(item, itemPath)
		if err != nil {
			return nil, err
		}
		if slices.Contains(ids, id) {
			return nil, fmt.Errorf("%s: type %s is listed twice", path, id)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func typeID(n *yamlNode, path string) (string, error) {
	if !typeIDPattern.MatchString(n.value) {
		return "", fmt.Errorf("%s: %q is not a type id, which is <capability>.<name> in lowercase kebab case", path, n.value)
	}
	return n.value, nil
}
