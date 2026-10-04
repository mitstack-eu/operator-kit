package tofuhandler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mitstack-eu/operator-kit/contract"
	"github.com/mitstack-eu/operator-kit/handler"
)

// Resource is the shared handler for one declared resource. Every type is
// served by this same code; only Spec and the inputs differ.
type Resource struct {
	Engine *Engine
	Spec   Spec
	// Name is the resource's name under the manifest's spec.resources.
	Name string
	// Inputs resolves the resource's inputs from the request. It is the
	// block's binding glue until the engine sends each resource its own
	// recorded inputs.
	Inputs func(d contract.Desired) (map[string]any, error)
	// Region is the AWS region a type with the aws provider is created in.
	Region string
}

var _ handler.DeclaredResource = (*Resource)(nil)

// object is one provider object a resource's state held after an ensure,
// recorded so an absence proof can ask the provider about it later.
type object struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	ID      string `json:"id"`
}

const (
	mainFile    = "main.tf.json"
	hclFile     = "module.tf"
	tfvarsFile  = "inputs.auto.tfvars.json"
	objectsFile = "objects.json"
	probeDir    = "absence-probe"
)

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Dir is the working directory of this resource for one subscription.
func (r *Resource) Dir(id contract.Identity) string {
	return filepath.Join(r.Engine.Root, unsafeChars.ReplaceAllString(id.SubscriptionID, "_"), r.Name)
}

// Ensure renders the resource's module from its inputs, applies it, and
// records the provider objects its state then holds. Applying an unchanged
// module changes nothing, so a repeated Ensure converges.
func (r *Resource) Ensure(ctx context.Context, d contract.Desired) error {
	in, err := r.Inputs(d)
	if err != nil {
		return &handler.Refused{Class: "invalid-inputs", Detail: fmt.Sprintf("resource %q: %v", r.Name, err)}
	}
	files, err := render(r.Spec, in, r.Region, d.CloudIdentity, r.Engine.Offline)
	if err != nil {
		return &handler.Refused{Class: "invalid-inputs", Detail: fmt.Sprintf("resource %q: %v", r.Name, err)}
	}
	dir := r.Dir(d.Identity)
	if err := writeRoot(dir, files); err != nil {
		return err
	}
	if _, err := r.Engine.run(ctx, dir, "init", "-input=false", "-no-color"); err != nil {
		return fmt.Errorf("resource %q: tofu init: %w", r.Name, err)
	}
	if _, err := r.Engine.run(ctx, dir, "apply", "-auto-approve", "-input=false", "-no-color"); err != nil {
		return fmt.Errorf("resource %q: tofu apply: %w", r.Name, err)
	}
	objs, err := r.stateObjects(ctx, dir)
	if err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(objs, "", "  ")
	return os.WriteFile(filepath.Join(dir, objectsFile), raw, 0o600)
}

// Observe refreshes the state against the provider and reports present when
// every object the last ensure created still exists, with the module's
// outputs. Once a delete has emptied the state, it asks the provider about
// each recorded object through an import plan, so absence is proven by the
// provider and never assumed from an empty state.
func (r *Resource) Observe(ctx context.Context, id contract.Identity) (contract.Observation, error) {
	dir := r.Dir(id)
	recorded, err := readObjects(dir)
	if err != nil {
		return contract.Observation{}, err
	}
	if recorded == nil {
		return contract.Observation{State: contract.Absent, StatusDetail: "no ensure has run for this resource"}, nil
	}
	held, err := r.stateObjects(ctx, dir)
	if err != nil {
		return contract.Observation{}, err
	}
	if len(held) == 0 {
		return r.proveAbsent(ctx, dir, recorded)
	}

	if _, err := r.Engine.run(ctx, dir, "apply", "-refresh-only", "-auto-approve", "-input=false", "-no-color"); err != nil {
		return contract.Observation{}, fmt.Errorf("resource %q: refresh: %w", r.Name, err)
	}
	held, err = r.stateObjects(ctx, dir)
	if err != nil {
		return contract.Observation{}, err
	}
	switch {
	case len(held) == len(recorded):
		outs, secrets, err := r.outputs(ctx, dir)
		if err != nil {
			return contract.Observation{}, err
		}
		return contract.Observation{State: contract.Present, Outputs: outs, SecretOutputs: secrets}, nil
	case len(held) == 0:
		return contract.Observation{State: contract.Absent, StatusDetail: "the refresh found none of the resource's objects"}, nil
	default:
		return contract.Observation{State: contract.Unknown, StatusDetail: fmt.Sprintf("the refresh found %d of the resource's %d objects", len(held), len(recorded))}, nil
	}
}

// Delete destroys everything the resource's state holds. A resource never
// ensured, or already destroyed, is absent, which is success.
func (r *Resource) Delete(ctx context.Context, id contract.Identity) error {
	dir := r.Dir(id)
	if _, err := os.Stat(filepath.Join(dir, mainFile)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	held, err := r.stateObjects(ctx, dir)
	if err != nil {
		return err
	}
	if len(held) == 0 {
		return nil
	}
	if _, err := r.Engine.run(ctx, dir, "destroy", "-auto-approve", "-input=false", "-no-color"); err != nil {
		return fmt.Errorf("resource %q: tofu destroy: %w", r.Name, err)
	}
	return nil
}

// proveAbsent plans an import of each recorded object into a scratch copy of
// the module with no state. The provider answers that a non-existent object
// cannot be imported, which proves it gone; a plan that succeeds means the
// object still stands. Any other failure is returned, never read as absence.
func (r *Resource) proveAbsent(ctx context.Context, dir string, recorded []object) (contract.Observation, error) {
	probe := filepath.Join(dir, probeDir)
	for _, o := range recorded {
		if err := os.RemoveAll(probe); err != nil {
			return contract.Observation{}, err
		}
		if err := os.MkdirAll(probe, 0o700); err != nil {
			return contract.Observation{}, err
		}
		for _, f := range []string{mainFile, hclFile, tfvarsFile, ".terraform.lock.hcl"} {
			if err := copyIfExists(filepath.Join(dir, f), filepath.Join(probe, f)); err != nil {
				return contract.Observation{}, err
			}
		}
		imp, _ := json.Marshal(map[string]any{"import": []any{map[string]any{"to": o.Address, "id": o.ID}}})
		if err := os.WriteFile(filepath.Join(probe, "import.tf.json"), imp, 0o600); err != nil {
			return contract.Observation{}, err
		}
		if _, err := r.Engine.run(ctx, probe, "init", "-input=false", "-no-color"); err != nil {
			return contract.Observation{}, fmt.Errorf("resource %q: absence probe init: %w", r.Name, err)
		}
		_, err := r.Engine.run(ctx, probe, "plan", "-input=false", "-no-color", "-lock=false")
		var ee *errExit
		switch {
		case err == nil:
			return contract.Observation{State: contract.Present, StatusDetail: fmt.Sprintf("%s %s still exists after delete", o.Address, o.ID)}, nil
		case errors.As(err, &ee) && strings.Contains(ee.stderr, "non-existent remote object"):
			continue
		default:
			return contract.Observation{}, fmt.Errorf("resource %q: absence probe for %s: %w", r.Name, o.Address, err)
		}
	}
	_ = os.RemoveAll(probe)
	return contract.Observation{State: contract.Absent, StatusDetail: fmt.Sprintf("the provider found none of the %d recorded objects", len(recorded))}, nil
}

// stateObjects lists the provider objects the state holds, leaving out the
// types that exist only in state.
func (r *Resource) stateObjects(ctx context.Context, dir string) ([]object, error) {
	if _, err := os.Stat(filepath.Join(dir, ".terraform")); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	out, err := r.Engine.run(ctx, dir, "show", "-json", "-no-color")
	if err != nil {
		return nil, fmt.Errorf("resource %q: tofu show: %w", r.Name, err)
	}
	var doc struct {
		Values *struct {
			RootModule struct {
				Resources []struct {
					Address string         `json:"address"`
					Mode    string         `json:"mode"`
					Type    string         `json:"type"`
					Values  map[string]any `json:"values"`
				} `json:"resources"`
			} `json:"root_module"`
		} `json:"values"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, fmt.Errorf("resource %q: reading tofu show: %w", r.Name, err)
	}
	if doc.Values == nil {
		return nil, nil
	}
	var objs []object
	for _, res := range doc.Values.RootModule.Resources {
		if res.Mode != "managed" || r.Spec.stateOnly(res.Type) {
			continue
		}
		id, _ := res.Values["id"].(string)
		objs = append(objs, object{Address: res.Address, Type: res.Type, ID: id})
	}
	return objs, nil
}

// outputs reads the module's outputs, splitting the sensitive ones out as
// secret outputs, each under its kind's output name.
func (r *Resource) outputs(ctx context.Context, dir string) (map[string]string, map[string]string, error) {
	out, err := r.Engine.run(ctx, dir, "output", "-json", "-no-color")
	if err != nil {
		return nil, nil, fmt.Errorf("resource %q: tofu output: %w", r.Name, err)
	}
	var doc map[string]struct {
		Sensitive bool `json:"sensitive"`
		Value     any  `json:"value"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		return nil, nil, err
	}
	var outs, secrets map[string]string
	for name, o := range doc {
		v, ok := o.Value.(string)
		if !ok {
			raw, _ := json.Marshal(o.Value)
			v = string(raw)
		}
		key := r.Spec.kindOutput(name)
		if o.Sensitive {
			if secrets == nil {
				secrets = map[string]string{}
			}
			secrets[key] = v
			continue
		}
		if outs == nil {
			outs = map[string]string{}
		}
		outs[key] = v
	}
	return outs, secrets, nil
}

// Objects returns the provider objects the last ensure recorded, for a
// sibling resource whose input binds to this resource whole.
func (r *Resource) Objects(id contract.Identity) ([]object, error) {
	return readObjects(r.Dir(id))
}

func writeRoot(dir string, f rendered) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, mainFile), f.mainJSON, 0o600); err != nil {
		return err
	}
	if f.hcl != "" {
		if err := os.WriteFile(filepath.Join(dir, hclFile), []byte(f.hcl), 0o600); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, tfvarsFile), f.tfvars, 0o600)
}

func readObjects(dir string) ([]object, error) {
	raw, err := os.ReadFile(filepath.Join(dir, objectsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	objs := []object{}
	if err := json.Unmarshal(raw, &objs); err != nil {
		return nil, err
	}
	return objs, nil
}

func copyIfExists(src, dst string) error {
	raw, err := os.ReadFile(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o600)
}
