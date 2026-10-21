package contract

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
)

const ManifestGoldensDir = "manifest-goldens"

//go:embed manifest-goldens/*.yaml
var ManifestGoldens embed.FS

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
