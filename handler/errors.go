package handler

import (
	"fmt"
	"strings"
)

const refusedPrefix = "refused:"

// ClassHostCannotRun refuses a resource whose host cannot run its workload, which a fresh subscribe on the same host cannot repair.
const ClassHostCannotRun = "host-cannot-run"

// unclassifiedRefusal keeps a wire-level statusDetail from carrying an empty class segment.
const unclassifiedRefusal = "unclassified"

// Refused is the error a block returns for a request it will not carry out, whose Class travels in statusDetail so the engine can read a refusal rather than infer one.
type Refused struct {
	// Class is machine-readable and renders as "unclassified" when empty.
	Class  string
	Detail string
}

// Error renders as "refused:<class>: <detail>", the exact statusDetail shape on the wire.
func (e *Refused) Error() string {
	class := e.Class
	if class == "" {
		class = unclassifiedRefusal
	}
	return refusedPrefix + class + ": " + e.Detail
}

// Refuse builds a *Refused whose Detail is formatted as fmt.Errorf formats.
func Refuse(class, format string, args ...any) error {
	return &Refused{Class: class, Detail: fmt.Sprintf(format, args...)}
}

// RefusedClass reads the class out of a decoded statusDetail, returning "" when it lacks the "refused:" prefix.
func RefusedClass(detail string) string {
	if !strings.HasPrefix(detail, refusedPrefix) {
		return ""
	}
	rest := detail[len(refusedPrefix):]
	idx := strings.Index(rest, ":")
	if idx < 0 {
		return ""
	}
	return rest[:idx]
}
