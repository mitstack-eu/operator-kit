package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/mitstack-eu/operator-kit/v3/contract"
	"github.com/mitstack-eu/operator-kit/v3/handler"
	"github.com/mitstack-eu/operator-kit/v3/observe"
	"github.com/mitstack-eu/operator-kit/v3/webhook"
)

//go:embed manifest.yaml
var manifest []byte

// cloud holds each object the operator created under the subscription that tagged it, and observes every declared resource.
type cloud struct {
	mu      sync.Mutex
	objects map[string]observe.Tagged
	subs    map[string]string
}

func (c *cloud) set(id string, obj *observe.Tagged, sub string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.objects, id)
	if obj != nil {
		c.objects[id], c.subs[id] = *obj, sub
	}
}

func (c *cloud) ListTagged(_ context.Context, sub string) ([]observe.Tagged, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []observe.Tagged
	for id, obj := range c.objects {
		if c.subs[id] == sub {
			out = append(out, obj)
		}
	}
	return out, nil
}

func (c *cloud) Lose(_ context.Context, r contract.CreatedResource) error {
	c.set(r.Identity["id"], nil, "")
	return nil
}

func (c *cloud) ObserveResource(_ context.Context, r contract.ResourceRef) (contract.Observation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.objects[r.Identity["id"]]; ok {
		return contract.Observation{State: contract.Present}, nil
	}
	return contract.Observation{State: contract.Absent}, nil
}

// operator creates one object per declared resource, of the first type its entry lists, and implements no issue handler.
type operator struct {
	cloud     *cloud
	resources []contract.ManifestResource
	logs      *webhook.LogBuffer
}

func (o *operator) EnsureResources(_ context.Context, d contract.Desired) ([]contract.CreatedResource, error) {
	var out []contract.CreatedResource
	for _, r := range o.resources {
		id := d.SubscriptionID + "/" + r.Name
		o.cloud.set(id, &observe.Tagged{Type: r.Types[0], ID: id}, d.SubscriptionID)
		out = append(out, contract.CreatedResource{Name: r.Name, Type: r.Types[0], Identity: map[string]string{"id": id}})
	}
	if o.logs != nil {
		o.logs.Append(fmt.Sprintf("ensured %d resources for subscription %s", len(out), d.SubscriptionID))
	}
	return out, nil
}

func (o *operator) Ensure(ctx context.Context, d contract.Desired) error {
	_, err := o.EnsureResources(ctx, d)
	return err
}

func (o *operator) Observe(context.Context, contract.Desired) (contract.Observation, error) {
	return contract.Observation{State: contract.Unknown}, nil
}

func (o *operator) Delete(_ context.Context, d contract.Desired) (map[string]string, error) {
	for _, r := range o.resources {
		o.cloud.set(d.SubscriptionID+"/"+r.Name, nil, "")
	}
	return nil, nil
}

func newOperator(logs *webhook.LogBuffer) (*operator, map[string]handler.Observer, error) {
	m, err := contract.ParseManifest(manifest)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the operator's manifest: %w", err)
	}
	c := &cloud{objects: map[string]observe.Tagged{}, subs: map[string]string{}}
	observers := map[string]handler.Observer{}
	for _, r := range m.Block.Spec.Resources {
		observers[r.Name] = c
	}
	return &operator{cloud: c, resources: m.Block.Spec.Resources, logs: logs}, observers, nil
}

func newHandler(jwksURL string, logs *webhook.LogBuffer) (*handler.Handler, error) {
	op, observers, err := newOperator(logs)
	if err != nil {
		return nil, err
	}
	return handler.New(handler.Config{Resource: op, Observers: observers, Manifest: manifest, JWKSURL: jwksURL, Logs: logs})
}

func main() {
	h, err := newHandler(os.Getenv("JWKS_URL"), nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe(":8080", h))
}
