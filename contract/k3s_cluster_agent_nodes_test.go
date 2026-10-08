package contract_test

// The k3s cluster takes an optional agentNodes value input that is a compatible addition to the released catalogue.

import (
	"testing"

	"github.com/mitstack-eu/operator-kit/contract"
)

const k3sClusterID = "hetzner.k3s-cluster"

func TestK3sCluster_AgentNodesIsAnOptionalValueInputReplacedByUpdate(t *testing.T) {
	var input *contract.Input
	found := false
	for _, rt := range contract.ResourceTypes() {
		if rt.ID != k3sClusterID {
			continue
		}
		found = true
		for i := range rt.Inputs {
			if rt.Inputs[i].Name == "agentNodes" {
				input = &rt.Inputs[i]
			}
		}
	}
	if !found {
		t.Fatalf("ResourceTypes() has no %s", k3sClusterID)
	}
	if input == nil {
		t.Fatalf("%s has no agentNodes input; it must be added as an optional value input", k3sClusterID)
	}
	want := contract.Input{Name: "agentNodes", Required: false, Secret: false, Accepts: "value", Place: "", Replacement: "update"}
	if *input != want {
		t.Errorf("%s agentNodes = %+v; want %+v (optional, so existing apps stay valid, and updated in place)", k3sClusterID, *input, want)
	}
}

func TestK3sCluster_AgentNodesAdditionIsNotABreakAgainstTheReleasedCatalogue(t *testing.T) {
	_, golden := readCatalogueGolden(t)
	current := contract.CurrentCatalogue()

	listed := func(c contract.Catalogue) (contract.CatalogueInput, bool) {
		for _, ct := range c.Types {
			if ct.ID != k3sClusterID {
				continue
			}
			for _, in := range ct.Inputs {
				if in.Name == "agentNodes" {
					return in, true
				}
			}
		}
		return contract.CatalogueInput{}, false
	}

	in, ok := listed(current)
	if !ok {
		t.Fatalf("CurrentCatalogue() does not list agentNodes on %s; it must, as an optional input", k3sClusterID)
	}
	if in.Required {
		t.Errorf("CurrentCatalogue() lists %s agentNodes as required; want optional, since a required addition breaks consumers", k3sClusterID)
	}
	for _, b := range contract.CompareCatalogues(golden, current, false) {
		t.Errorf("CompareCatalogues(golden, current, false) reports a break: %s", b)
	}

	without := contract.Catalogue{Major: current.Major}
	for _, ct := range current.Types {
		ct.Inputs = append([]contract.CatalogueInput(nil), ct.Inputs...)
		if ct.ID == k3sClusterID {
			kept := ct.Inputs[:0]
			for _, in := range ct.Inputs {
				if in.Name != "agentNodes" {
					kept = append(kept, in)
				}
			}
			ct.Inputs = kept
		}
		without.Types = append(without.Types, ct)
	}
	for _, b := range contract.CompareCatalogues(without, current, false) {
		t.Errorf("CompareCatalogues(current without %s agentNodes, current, false) reports a break: %s", k3sClusterID, b)
	}
}
