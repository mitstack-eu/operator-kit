package contract

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
)

// LifecycleHooksField is the one block manifest field that carries lifecycle hook declarations, read by both the kit handler and the platform's parser.
const LifecycleHooksField = "spec.lifecycle.hooks"

const AppNameNamingField = "spec.naming.appName"

// AppNameNaming is a block's AppNameNamingField declaration, whose yaml tags without omitempty are keys the parser requires.
type AppNameNaming struct {
	MaxLength    int           `yaml:"maxLength"`
	DerivedNames []DerivedName `yaml:"derivedNames,omitempty"`
}

type DerivedName struct {
	// Template is the name with the app name written as {appName}.
	Template string `yaml:"template"`
	Limit    int    `yaml:"limit"`
}

func LifecycleHookPoints() []string {
	return []string{"beforeEnsure", "afterEnsure", "beforeDelete", "afterDelete"}
}

const ManifestGoldensDir = "manifest-goldens"

//go:embed manifest-goldens/*.yaml
var ManifestGoldens embed.FS

// HooksManifestGolden returns the manifest golden that declares lifecycle hooks, panicking on a missing embedded file as a packaging bug.
func HooksManifestGolden() []byte {
	raw, err := ManifestGoldens.ReadFile(ManifestGoldensDir + "/lifecycle-hooks.yaml")
	if err != nil {
		panic("contract: embedded manifest golden missing: " + err.Error())
	}
	return raw
}

// TypeBlockHooksManifestGolden returns the HooksManifestGolden block written with `type: block`, `format:` and `store:` spellings, panicking on a missing embedded file as a packaging bug.
func TypeBlockHooksManifestGolden() []byte {
	raw, err := ManifestGoldens.ReadFile(ManifestGoldensDir + "/lifecycle-hooks-type-block.yaml")
	if err != nil {
		panic("contract: embedded manifest golden missing: " + err.Error())
	}
	return raw
}

func HooksManifestGoldenDeclares() []string {
	return []string{"beforeEnsure", "afterDelete"}
}

// slugPattern is the platform's slug; Go's $ matches only at the end of the text, so a trailing newline is refused.
var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

func isSlug(s string) bool { return slugPattern.MatchString(s) }

func isKebab(s string) bool {
	if s == "" {
		return false
	}
	for _, group := range strings.Split(s, "-") {
		if group == "" {
			return false
		}
		for _, c := range group {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}

// ParseBlockID splits a bare slug or @<org slug>/<slug> into namespace and name, trimming nothing, so an id carrying whitespace is refused.
func ParseBlockID(id string) (namespace, name string, err error) {
	return parseNamespacedName("block", id)
}

func ParseCapabilityID(id string) (namespace, name string, err error) {
	return parseNamespacedName("capability", id)
}

func parseNamespacedName(kind, id string) (namespace, name string, err error) {
	if org, rest, ok := splitNamespace(id); ok {
		if isSlug(org) && isSlug(rest) {
			return org, rest, nil
		}
	} else if isSlug(id) {
		return "", id, nil
	}
	return "", "", fmt.Errorf(
		"%s id %q is neither a community id <name> nor a private id @<org>/<name>, "+
			"where each of org and name is 1 to 40 lowercase letters, digits and dashes that start and end with a letter or digit",
		kind, id)
}

// ParseResourceTypeID splits <capability>.<name> or @<org>/<capability>.<name> into namespace, capability and name, trimming nothing.
func ParseResourceTypeID(id string) (namespace, capability, name string, err error) {
	rest := id
	if strings.HasPrefix(id, "@") {
		org, after, ok := splitNamespace(id)
		if !ok || !isSlug(org) {
			return "", "", "", resourceTypeIDError(id)
		}
		namespace, rest = org, after
	}
	capability, name, hasDot := strings.Cut(rest, ".")
	if !hasDot || !isSlug(capability) || !isKebab(name) {
		return "", "", "", resourceTypeIDError(id)
	}
	return namespace, capability, name, nil
}

func resourceTypeIDError(id string) error {
	return fmt.Errorf(
		"resource type id %q is neither a community type <capability>.<name> nor a private type @<org>/<capability>.<name>, "+
			"where org and capability are each 1 to 40 lowercase letters, digits and dashes that start and end with a letter or digit, "+
			"and name is lowercase kebab case",
		id)
}

func splitNamespace(id string) (org, rest string, ok bool) {
	after, found := strings.CutPrefix(id, "@")
	if !found {
		return "", "", false
	}
	return strings.Cut(after, "/")
}
