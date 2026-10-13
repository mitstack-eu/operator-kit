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

// manifestDecoder reads a node tree by struct tags: a field whose tag has no omitempty is required, a fold tag names a second key whose value must agree, and a field named _ takes a key's value and keeps none of it.
type manifestDecoder struct {
	version string
	tag     string
	// coerce reads each scalar as the platform's yaml.v3 decoder does: a null is the zero value, a string takes any scalar's text, a bool also takes the YAML 1.1 words, and an int truncates a float toward zero.
	coerce bool
	// dropped collects the path of each key a field named _ reads with a non-null value.
	dropped *[]string
}

var (
	plainNull    = regexp.MustCompile(`^(null|Null|NULL|~)$`)
	plainBoolean = regexp.MustCompile(`^(true|True|TRUE|false|False|FALSE)$`)
	plainNumber  = regexp.MustCompile(`^([-+]?[0-9]+|0o[0-7]+|0x[0-9a-fA-F]+|` +
		`[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?|[-+]?\.(inf|Inf|INF)|\.(nan|NaN|NAN))$`)
	yamlStyleFloat = regexp.MustCompile(`^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$`)
)

// yaml11Booleans are the words yaml.v3 reads into a Go bool, plain or quoted, for YAML 1.1.
var yaml11Booleans = map[string]bool{
	"y": true, "Y": true, "yes": true, "Yes": true, "YES": true, "on": true, "On": true, "ON": true,
	"n": false, "N": false, "no": false, "No": false, "NO": false, "off": false, "Off": false, "OFF": false,
}

// durationType is read from a Go duration such as 75m, and a manifest takes only a positive one.
var durationType = reflect.TypeOf(time.Duration(0))

func isNull(n *yamlNode) bool {
	return n.kind == yamlNull || n.kind == yamlScalar && !n.str && plainNull.MatchString(n.value)
}

type manifestField struct {
	name     string
	index    int
	required bool
	fold     string
	dropped  bool
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
			fold:     f.Tag.Get("fold"),
			dropped:  f.Name == "_",
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
		if d.coerce {
			v.Set(reflect.Zero(v.Type()))
			return nil
		}
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
		if !d.coerce && !n.str && (plainBoolean.MatchString(n.value) || plainNumber.MatchString(n.value)) {
			return fmt.Errorf("%s is %s, which YAML reads as a number or a boolean, but it takes a string, so quote it", path, n.value)
		}
		v.SetString(n.value)
	case reflect.Bool:
		b, ok := d.boolean(n)
		if !ok {
			if d.coerce {
				return fmt.Errorf("%s takes a boolean only: true or false, or yes, no, on or off", path)
			}
			return fmt.Errorf("%s takes true or false only", path)
		}
		v.SetBool(b)
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

// boolean takes no quoted true or false under coerce, but takes a YAML 1.1 word plain or quoted, as yaml.v3 does.
func (d manifestDecoder) boolean(n *yamlNode) (value, ok bool) {
	if n.kind != yamlScalar {
		return false, false
	}
	if !n.str && plainBoolean.MatchString(n.value) && (d.coerce || n.value == "true" || n.value == "false") {
		return strings.EqualFold(n.value, "true"), true
	}
	if d.coerce {
		value, ok = yaml11Booleans[n.value]
	}
	return value, ok
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
		if d.coerce && isNull(item) {
			continue
		}
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

type foldedValue struct {
	field manifestField
	value string
}

func (d manifestDecoder) structure(n *yamlNode, v reflect.Value, path string) error {
	if err := mapEntries(n, path); err != nil {
		return err
	}
	fields := manifestFields(v.Type(), d.tag)
	byKey := make(map[string]manifestField, len(fields))
	for _, f := range fields {
		byKey[f.name] = f
		if f.fold != "" {
			byKey[f.fold] = f
		}
	}
	present := make(map[string]bool, len(n.keys))
	var folded []foldedValue
	for i, key := range n.keys {
		keyPath := fieldPath(path, key)
		f, ok := byKey[key]
		if !ok {
			return fmt.Errorf("%s is not a field of a %s manifest", keyPath, d.version)
		}
		val := n.vals[i]
		target := v.Field(f.index)
		underFold := key != f.name
		if f.dropped || underFold {
			target = reflect.New(target.Type()).Elem()
		}
		var err error
		switch {
		case isNull(val) && f.required:
			err = noValue(keyPath)
		default:
			err = d.decode(val, target, keyPath)
		}
		if err != nil {
			return err
		}
		if f.dropped && d.dropped != nil && !isNull(val) {
			*d.dropped = append(*d.dropped, keyPath)
		}
		if underFold {
			folded = append(folded, foldedValue{f, target.String()})
		}
		present[key] = true
	}
	for _, fv := range folded {
		if err := foldInto(v.Field(fv.field.index), fv, path); err != nil {
			return err
		}
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

func foldInto(field reflect.Value, fv foldedValue, path string) error {
	switch current := field.String(); {
	case fv.value == "":
		return nil
	case current == "" || current == fv.value:
		field.SetString(fv.value)
		return nil
	default:
		return fmt.Errorf("%s: %s %q and %s %q disagree; write one of them, or both with the same value",
			describe(path), fv.field.fold, fv.value, fv.field.name, current)
	}
}
