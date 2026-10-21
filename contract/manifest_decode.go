package contract

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// manifestDecoder reads a node tree by struct tags, and a field whose tag has no omitempty is required.
type manifestDecoder struct {
	version string
	tag     string
}

var (
	plainNull    = regexp.MustCompile(`^(null|Null|NULL|~)$`)
	plainBoolean = regexp.MustCompile(`^(true|True|TRUE|false|False|FALSE)$`)
	plainNumber  = regexp.MustCompile(`^([-+]?[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+|` +
		`[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?|[-+]?\.(inf|Inf|INF)|\.(nan|NaN|NAN))$`)
	yamlStyleFloat = regexp.MustCompile(`^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$`)
)

// durationType is read from a Go duration such as 75m, and a manifest takes only a positive one.
var durationType = reflect.TypeOf(time.Duration(0))

func isNull(n *yamlNode) bool {
	return n.kind == yamlNull || n.kind == yamlScalar && !n.str && plainNull.MatchString(n.value)
}

type manifestField struct {
	name     string
	index    int
	required bool
}

func manifestFields(t reflect.Type, tag string) []manifestField {
	fields := make([]manifestField, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get(tag), ",")
		fields = append(fields, manifestField{
			name:     name,
			index:    i,
			required: opts != "omitempty",
		})
	}
	return fields
}

func fieldPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func describe(path string) string {
	if path == "" {
		return "the document"
	}
	return path
}

func readable(n *yamlNode, path string) error {
	switch {
	case n.alias:
		return fmt.Errorf("%s is an alias, which the reader does not follow, so what it declares is unknown", describe(path))
	case n.tag != "":
		return fmt.Errorf("%s is tagged %s, but %s", describe(path), n.tag, n.tagNote)
	case n.reserved != 0:
		return fmt.Errorf("%s starts with %q, which YAML does not allow at the start of an unquoted value, so quote it", describe(path), n.reserved)
	case n.kind == yamlOpaque:
		return fmt.Errorf("%s is in a form the reader cannot follow (an unclosed quote or flow collection, or a key inside a value), so what it declares is unknown", describe(path))
	case n.misplaced:
		return fmt.Errorf("%s holds a line that is neither one of its keys nor inside one of their values", describe(path))
	}
	return nil
}

func noValue(path string) error {
	return fmt.Errorf("%s has no value, so leave it out or give it one", describe(path))
}

func (d manifestDecoder) decode(n *yamlNode, v reflect.Value, path string) error {
	if err := readable(n, path); err != nil {
		return err
	}
	if isNull(n) {
		return noValue(path)
	}
	switch v.Type() {
	case requirementType:
		q, err := d.requirement(n, path)
		v.Set(reflect.ValueOf(q))
		return err
	case resourceType:
		r, err := d.resource(n, path)
		v.Set(reflect.ValueOf(r))
		return err
	case durationType:
		dur, err := positiveDuration(n, path)
		v.Set(reflect.ValueOf(dur))
		return err
	}
	switch v.Kind() {
	case reflect.String:
		if n.kind != yamlScalar {
			return fmt.Errorf("%s must be one value, not a mapping or a list", path)
		}
		if !n.str && (plainBoolean.MatchString(n.value) || plainNumber.MatchString(n.value)) {
			return fmt.Errorf("%s is %s, which YAML reads as a number or a boolean, but it takes a string, so quote it", path, n.value)
		}
		v.SetString(n.value)
	case reflect.Bool:
		if n.kind != yamlScalar || n.str || n.value != "true" && n.value != "false" {
			return fmt.Errorf("%s takes true or false only", path)
		}
		v.SetBool(n.value == "true")
	case reflect.Int:
		i, ok := plainInteger(n)
		if !ok || v.OverflowInt(i) {
			return fmt.Errorf("%s takes an integer only", path)
		}
		v.SetInt(i)
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		if err := d.decode(n, p.Elem(), path); err != nil {
			return err
		}
		v.Set(p)
	case reflect.Slice:
		return d.list(n, v, path)
	case reflect.Map:
		return d.mapping(n, v, path)
	case reflect.Struct:
		return d.structure(n, v, path)
	default:
		return fmt.Errorf("%s has a Go type, %s, the manifest decoder does not read", path, v.Type())
	}
	return nil
}

func positiveDuration(n *yamlNode, path string) (time.Duration, error) {
	if n.kind != yamlScalar {
		return 0, fmt.Errorf("%s must be one value, not a mapping or a list", path)
	}
	dur, err := time.ParseDuration(n.value)
	if err != nil {
		return 0, fmt.Errorf("%s takes a positive duration such as 75m: %w", path, err)
	}
	if dur <= 0 {
		return 0, fmt.Errorf("%s is %s, but it takes a positive duration such as 75m", path, n.value)
	}
	return dur, nil
}

// plainInteger reads a plain scalar as yaml.v3 does for a Go int; a quoted scalar is a string, which an int does not take.
func plainInteger(n *yamlNode) (int64, bool) {
	if n.kind != yamlScalar || n.str {
		return 0, false
	}
	plain := strings.ReplaceAll(n.value, "_", "")
	if i, err := strconv.ParseInt(plain, 0, 64); err == nil {
		return i, true
	}
	if u, err := strconv.ParseUint(plain, 0, 64); err == nil {
		return int64(u), u <= math.MaxInt64
	}
	if !yamlStyleFloat.MatchString(plain) {
		return 0, false
	}
	f, err := strconv.ParseFloat(plain, 64)
	if err != nil || f < math.MinInt64 || f >= math.MaxInt64 {
		return 0, false
	}
	return int64(f), true
}

func (d manifestDecoder) list(n *yamlNode, v reflect.Value, path string) error {
	if n.kind != yamlSeq {
		return fmt.Errorf("%s must be a list", path)
	}
	list := reflect.MakeSlice(v.Type(), 0, len(n.items))
	for i, item := range n.items {
		elem := reflect.New(v.Type().Elem()).Elem()
		if err := d.decode(item, elem, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
		list = reflect.Append(list, elem)
	}
	v.Set(list)
	return nil
}

func mapEntries(n *yamlNode, path string) error {
	if n.kind != yamlMap {
		return fmt.Errorf("%s must be a mapping", describe(path))
	}
	seen := make(map[string]bool, len(n.keys))
	for _, key := range n.keys {
		switch {
		case key == "<<":
			return fmt.Errorf("%s holds a merge key, which the reader does not follow, so what it declares is unknown", describe(path))
		case seen[key]:
			return fmt.Errorf("%s is written twice", fieldPath(path, key))
		}
		seen[key] = true
	}
	return nil
}

func (d manifestDecoder) mapping(n *yamlNode, v reflect.Value, path string) error {
	if err := mapEntries(n, path); err != nil {
		return err
	}
	m := reflect.MakeMapWithSize(v.Type(), len(n.keys))
	for i, key := range n.keys {
		elem := reflect.New(v.Type().Elem()).Elem()
		if err := d.decode(n.vals[i], elem, fieldPath(path, key)); err != nil {
			return err
		}
		m.SetMapIndex(reflect.ValueOf(key), elem)
	}
	v.Set(m)
	return nil
}

func (d manifestDecoder) structure(n *yamlNode, v reflect.Value, path string) error {
	if err := mapEntries(n, path); err != nil {
		return err
	}
	fields := manifestFields(v.Type(), d.tag)
	byKey := make(map[string]manifestField, len(fields))
	for _, f := range fields {
		byKey[f.name] = f
	}
	present := make(map[string]bool, len(n.keys))
	for i, key := range n.keys {
		keyPath := fieldPath(path, key)
		f, ok := byKey[key]
		if !ok {
			return fmt.Errorf("%s is not a field of a %s manifest", keyPath, d.version)
		}
		if err := d.decode(n.vals[i], v.Field(f.index), keyPath); err != nil {
			return err
		}
		present[key] = true
	}
	for _, f := range fields {
		if !f.required {
			continue
		}
		fv := v.Field(f.index)
		switch {
		case !present[f.name]:
			return fmt.Errorf("%s is required", fieldPath(path, f.name))
		case (fv.Kind() == reflect.String || fv.Kind() == reflect.Slice) && fv.Len() == 0:
			return fmt.Errorf("%s is required and must not be empty", fieldPath(path, f.name))
		}
	}
	return nil
}
