package kittest

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/contract"
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

type v1alphaRoot struct {
	APIVersion string                   `yaml:"apiVersion"`
	Kind       string                   `yaml:"kind"`
	Metadata   contract.V1AlphaMetadata `yaml:"metadata"`
	Spec       contract.V1AlphaSpec     `yaml:"spec"`
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
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if !v.IsZero() {
			text = fmt.Sprint(v.Interface())
		}
	default:
		m.tb.Fatalf("manifest key %s is a section, not a scalar", path)
		return "", false
	}
	return text, text != ""
}

func (m Manifest) lookup(path string) reflect.Value {
	m.tb.Helper()
	var v reflect.Value
	var tag string
	switch {
	case m.Block != nil:
		v, tag = reflect.ValueOf(*m.Block), "json"
	case m.V1Alpha != nil:
		v, tag = reflect.ValueOf(v1alphaRoot{
			APIVersion: m.APIVersion,
			Kind:       m.V1Alpha.Kind,
			Metadata:   m.V1Alpha.Metadata,
			Spec:       m.V1Alpha.Spec,
		}), "yaml"
	default:
		m.tb.Fatalf("manifest key %s read from a manifest ReadManifest did not return", path)
		return reflect.Value{}
	}
	for _, seg := range strings.Split(path, ".") {
		v = deref(v)
		switch v.Kind() {
		case reflect.Struct:
			field, ok := fieldByKey(v.Type(), tag, seg)
			if !ok {
				m.tb.Fatalf("manifest key %s: %q is not a key of %s", path, seg, v.Type())
				return reflect.Value{}
			}
			v = v.FieldByIndex(field.Index)
		case reflect.Map:
			if v.Type().Key().Kind() != reflect.String {
				m.tb.Fatalf("manifest key %s: %s is not keyed by name", path, v.Type())
				return reflect.Value{}
			}
			next := v.MapIndex(reflect.ValueOf(seg).Convert(v.Type().Key()))
			if !next.IsValid() {
				next = reflect.Zero(v.Type().Elem())
			}
			v = next
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

func fieldByKey(t reflect.Type, tag, key string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get(tag), ",")
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
