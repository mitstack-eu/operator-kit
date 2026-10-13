// Package observe holds the kit's shared read-only observers, one per
// resource type, each satisfying handler.Observer. An observer reads the
// identity its contract.ResourceRef carries and asks the provider, through
// a small API interface the operator implements, what stands there right
// now; it never answers from what an ensure remembered. An identity missing
// the key an observer reads is unknown, and no API call is made.
//
// The package also holds the tag listers a block's verification reads to
// find everything carrying a subscription's id.
package observe

import (
	"fmt"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

// present is a present observation carrying outputs.
func present(outputs map[string]string) contract.Observation {
	return contract.Observation{State: contract.Present, Outputs: outputs}
}

// absent is an absent observation.
func absent() contract.Observation {
	return contract.Observation{State: contract.Absent}
}

// unknown is an unknown observation whose detail says why.
func unknown(format string, args ...any) contract.Observation {
	return contract.Observation{State: contract.Unknown, StatusDetail: fmt.Sprintf(format, args...)}
}

// identityKeys returns the values r's identity holds under keys, and false
// with an unknown observation naming the first missing key otherwise.
func identityKeys(r contract.ResourceRef, keys ...string) ([]string, contract.Observation, bool) {
	values := make([]string, len(keys))
	for i, key := range keys {
		values[i] = r.Identity[key]
		if values[i] == "" {
			return nil, unknown("resource %q: its identity holds no %s, so there is nothing to observe", r.Name, key), false
		}
	}
	return values, contract.Observation{}, true
}

// existence answers an observer that asks only whether its object exists:
// present with outputs when it does, absent when it does not.
func existence(exists bool, err error, outputs map[string]string) (contract.Observation, error) {
	if err != nil {
		return contract.Observation{}, err
	}
	if !exists {
		return absent(), nil
	}
	return present(outputs), nil
}
