package kittest

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v3/contract"
)

// Manifest is a block manifest parsed by contract.ParseManifest and read by dotted key path.
type Manifest struct {
	contract.Manifest
	tb testing.TB
}

// ReadManifest parses the manifest file at path through contract.ParseManifest and fails tb when it cannot be read or parsed.
func ReadManifest(tb testing.TB, path string) Manifest {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("reading manifest %s: %v", path, err)
	}
	m, err := contract.ParseManifest(raw)
	if err != nil {
		tb.Fatalf("parsing manifest %s: %v", path, err)
	}
	return Manifest{Manifest: m, tb: tb}
}

// Section returns the value at a dotted key path such as "spec.lifecycle", and false when the manifest does not set it.
func (m Manifest) Section(path string) (any, bool) {
	m.tb.Helper()
	v := m.lookup(path)
	if isUnset(v) {
		return nil, false
	}
	return v.Interface(), true
}

// SpecScalar returns spec.<key> as text, and false when the manifest does not set it.
func (m Manifest) SpecScalar(key string) (string, bool) {
	m.tb.Helper()
	path := "spec." + key
	v := m.lookup(path)
	var text string
	switch v.Kind() {
	case reflect.String:
		text = strings.TrimSpace(v.String())
	case reflect.Bool:
		if v.Bool() {
			text = "true"
		}
	default:
		m.tb.Fatalf("manifest key %s is a section, not a scalar", path)
		return "", false
	}
	return text, text != ""
}

func (m Manifest) lookup(path string) reflect.Value {
	m.tb.Helper()
	if m.Block == nil {
		m.tb.Fatalf("manifest key %s read from a manifest ReadManifest did not return", path)
		return reflect.Value{}
	}
	v := reflect.ValueOf(*m.Block)
	for _, seg := range strings.Split(path, ".") {
		v = deref(v)
		switch v.Kind() {
		case reflect.Struct:
			field, ok := fieldByKey(v.Type(), seg)
			if !ok {
				m.tb.Fatalf("manifest key %s: %q is not a key of %s", path, seg, v.Type())
				return reflect.Value{}
			}
			v = v.FieldByIndex(field.Index)
		default:
			m.tb.Fatalf("manifest key %s: %q is below a %s, which has no keys", path, seg, v.Type())
			return reflect.Value{}
		}
	}
	return deref(v)
}

func deref(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v = reflect.Zero(v.Type().Elem())
		} else {
			v = v.Elem()
		}
	}
	return v
}

func fieldByKey(t reflect.Type, key string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.IsExported() && name != "" && name != "-" && name == key {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

func isUnset(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	}
	return v.IsZero()
}
