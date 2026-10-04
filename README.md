# operator-kit

This module is the contract every mitstack block imports to talk to the
engine. It covers the webhook trust boundary a block's handler sits behind,
the events a block emits as it works, and `kittest`, the conformance harness
a block's own tests run against the same fixtures the engine is tested
against. The `contract` package owns the wire contract itself: the envelope
and response JSON schemas, the golden request and response fixtures both
sides are checked against, and the resource registry. The registry is the one
list of output names a block may declare in `spec.outputs` and read under
`spec.requires[].reads`, and the engine reads it from this package.

The `contract` package also holds the resource-type catalogue, which is the
one list of resource types a block can create or reference.
`contract.ResourceTypes` returns every type, sorted by its ID, and
`contract.LookupResourceType` returns the one type with a given ID, such as
`aws.ecr-repository`. Each type implements a kind, which names what the
resource is without naming a provider: the kind of `aws.ecr-repository` is
`container-repository`. A kind is an interface of outputs. `contract.Kinds`
and `contract.LookupKind` return each kind's required and optional outputs,
each marked secret or not, and a type produces every required output of its
kind, may produce the optional ones and names no other output.

Each type lives in one place, which `ResourceType.Place` reads from the ID
prefix. The place is a capability from `contract.Capabilities`, as
`aws.ec2-instance` lives in `aws`, or a kind, as `host.container` lives on a
host. A type also names the capabilities it relies on and the one it hosts,
if any. It declares its inputs, each with a secret mark, what it accepts and
whether changing it replaces the resource. An input whose accepts is
`resource:<kind>` or `output:<kind>/<output>` takes a resource, and it also
says where that resource must live. The input's place is `own` for the
type's own capability, `capability:<slug>` for a named capability, or
`carrier` for the capability of the resource the type lives on. For example,
the `repository` input of `ubicloud.runner` takes `capability:github`. An
input that accepts `value` has no place. The type declares its lifecycle,
with a handover when it is stateful, its upgrade behaviour, its pricing, how it is
observed and proven absent, a time budget for each of `ensure`, `observe` and
`delete`, and the failures it can meet, each `permanent` or `transient`. It
also carries a category, the languages it serves, the artifact type it holds
and its scope.

A type that maps one to one onto an OpenTofu provider resource names that
resource, and its inputs are generated from the provider's schema by
`cmd/catalogue-inputs` into `contract/provider-inputs.json`. Regenerate them
with
`go run ./cmd/catalogue-inputs -schema cmd/catalogue-inputs/testdata/provider-schema-extract.json > contract/provider-inputs.json`.
A provider schema carries no replacement rule, so each such type lists by
hand the inputs its provider forces a new resource on.

Types that share a name, the part of the ID after the first dot, share a
kind, and `contract.ValidateResourceTypes` refuses a catalogue in which they
do not. That rule is stricter than ADR 0081, and the reason is naming: a name
means one real thing in every place, so a different thing gets a different
name. Every catalogue type has a bare name that mitstack assigns, so
`contract.ValidateResourceTypes` also refuses an ID whose prefix is not a bare
slug, such as `reconcile/crossplane.claim`, and an ID whose prefix is neither
a capability nor a kind.

The `contract` package also owns the grammar of block, capability and
resource-type ids, which the platform's ADR 0080 sets. A community name is
bare, with no prefix, and mitstack assigns it. A private name sits under its
org's namespace, as in `@acme/billing-db`. Two orgs can then each hold a
private block named `api`, and a private name never takes a community one,
because no bare name is private. Each part is a slug of 1 to 40 lowercase
letters, digits and dashes that starts and ends with a letter or digit.
`contract.ParseBlockID` and `contract.ParseCapabilityID` accept `billing-db`
or `@acme/billing-db`, and `contract.ParseResourceTypeID` accepts
`aws.ebs-volume` or `@acme/aws.special-bucket`.

blockverify, the kit's conformance runner, judges a manifest's `metadata.id`
with `contract.ParseBlockID` in its manifest suite. Write a private id in
quotes, as in `id: "@acme/billing-db"`, because YAML does not allow `@` at
the start of an unquoted value.

`contract.ParseManifest` is the kit's one block manifest parser for both
manifest versions, and blockverify and the handler read every manifest
through it. Under `mitstack/v1alpha` it reads the whole manifest strictly
into `contract.V1AlphaManifest`, every field the platform's decoder reads
and `spec.naming.appName`. Under `mitstack/v1beta` it reads the whole
manifest strictly into `contract.BlockManifest`. Under either version it
refuses any key those types do not name, naming the key's path. The kit ships the JSON Schema for `mitstack/v1beta` as
`contract/manifest.schema.json`, generated from the same types, and a contract
test fails when the committed file differs from what the types generate.
Regenerate it with
`go run ./cmd/manifest-schema > contract/manifest.schema.json`.

## Importing it

The module path is `github.com/mitstack-eu/operator-kit`. A block adds it the
usual way:

```
go get github.com/mitstack-eu/operator-kit@vX.Y.Z
```

The repository is private, so the fetch needs `GOPRIVATE=github.com/mitstack-eu`
in the environment and a credential able to read this repository (an SSH key
or a token configured for `github.com` in `~/.netrc` or `git config`).

## The typed contract

`contract.DecodeRequest` decodes a lifecycle webhook body into a
`contract.Request`, a Go struct whose JSON tags mirror envelope.schema.json
field for field. It also checks the schema's required set — operation,
nonce, createdAt, subscriptionId, blockId, orgId — and the operation enum,
so a malformed envelope fails with an error naming the JSON field before an
operator's own logic ever sees it, and when `cloudIdentity` or
`buildRequest` is present it checks their own required properties the same
way, naming them `cloudIdentity.provider` and similar. `contract.DecodeResponse`
does the same for a `contract.Response`, decoded against response.schema.json;
a field of the wrong type fails the same way, naming the field.

`Request.Identity()` returns a `contract.Identity`: the subset of a request
that names which subscription an operation targets and where its resource
lives — subscription, block, org, app, its resolved tool instances, its
prior outputs, and its cloud identity — independent of what it should
converge to. The instances belong here because a teardown has to address
the resource where it was provisioned: a block scoped to an AWS region
reads that region out of `Identity.Instances`, and `Delete` and `Observe`
are handed an `Identity` and nothing else by default — a Resource that opts
into `handler.RequestObserver` or `handler.HandoverResource` (below) sees
the verb's own `Desired`, secrets included, instead. `Request.Desired()`
returns a `contract.Desired`, which embeds `Identity` and adds the params, secrets
and bindings that describe what an operator should converge its resource
to, so `d.Instances` inside `Ensure` resolves through the embedded
`Identity` to the same map a teardown sees. Splitting a request this way
lets a validate/unsubscribe handler read only `Identity`, without pulling in
fields a teardown call never needs.

`contract.Observation` is what a validate or unsubscribe check found: a
`contract.State` (`Present`, `Absent`, or `Unknown`) plus whatever outputs
and status detail it gathered along the way. `Observation.Response()` turns
that into the `contract.Response` the webhook handler returns, mapping
`State` onto the response's `absent` pointer: `Present` sets it to `false`,
`Absent` sets it to `true`, and `Unknown` leaves it `nil` so the engine
fails open exactly as an operator that never implements the marker does.

## The Handler and Resource

A block implements `handler.Resource` — `Ensure`, `Observe`, `Delete` — and
`handler.New` wires it behind the trust boundary, the settle loop (a bounded
poll of `Observe` until the resource actually serves) and the
idempotency cache described below, calling whatever `Hooks` the block's
`Config` registers at `HookBeforeEnsure`, `HookAfterEnsure`, `HookBeforeDelete`
or `HookAfterDelete`.

A block declares its hooks in the manifest under `spec.lifecycle.hooks`, the
path held in the constant `contract.LifecycleHooksField`.
`contract.LifecycleHookPoints` is the closed set of names a block may declare
there. The handler and the platform's manifest validation both refuse a
name outside that set, and both refuse a name listed twice. Both sides test against
the same manifest golden, `contract.HooksManifestGolden`, so a change on
either side turns the other side's tests red. Its twin,
`contract.TypeBlockHooksManifestGolden`, is the same block written with
`type: block`, `format` and `store` and without a layer, and the handler
reads it exactly as it reads the first.

A request that carries no OBO token is refused with 401 on every verb —
subscribe, upgrade, validate, unsubscribe — before the Resource is ever
consulted. The token is the platform's own signed assertion of the
consuming org, app and subscription, and nothing in `Config` or the
manifest turns the check off.

An error the Resource or a hook returns is answered by its kind. A
`*handler.Refused` answers 400 with a `refused:<class>` status detail,
which the engine treats as permanent. Any other error answers 500, which
the engine retries under the same idempotency key, and the idempotency
cache never stores a 500, so the retry reaches the Resource again. A panic
in the Resource or a hook is answered the same way as that second kind: the
Handler recovers it and answers 500 with a fixed error naming the verb,
such as `handler: the block panicked during subscribe`. The response never
carries the panic value. The Handler recovers inside the idempotency cache's
single-flight call, so a delivery waiting on the same key receives that
same response rather than an empty one. The panic is logged with the block,
subscription and verb. A string or error panic value is logged with every
request secret scrubbed where it appears literally, and any other value is
logged only as its type, because formatting it could print a secret in a
form a literal scrub cannot find. A block cannot turn this recovery off.

The idempotency cache is keyed by subscription and by the request's
`idempotencyKey`, which the engine sets once per call and keeps the same
across every retry of that call. It stores only a settled answer, for an
hour: a settled 2xx or a 400 refusal is replayed to a later delivery of the
same key, and a delivery that overlaps one still in flight waits for it and
shares its answer. A 2xx that answers `pending:true` reaches the caller but
is never stored, because the engine re-drives the same key to learn when
the resource converges, and a stored pending answer would replay "still
pending" instead of observing it. An unsubscribe that answers
`absent:false` also reaches the caller but is never stored, because absence
must come from observing the resource, and the redelivery observes it
again. A subscribe or upgrade answer that carries `absent:false` is stored
as usual, since that is what a present resource answers. Two kinds of
request bypass the cache entirely: a validate, because it is a probe of the
resource's current state and a replayed observation could report a state
the resource has left, and any request whose `idempotencyKey` is empty.

A block whose resource sits behind a token — or otherwise needs the
request's own secrets to authenticate — implements `handler.RequestObserver`:

```go
type RequestObserver interface {
	ObserveWithRequest(ctx context.Context, d contract.Desired) (contract.Observation, error)
}
```

The Handler checks for it with a type assertion everywhere it would
otherwise call `Observe`: once on validate, and on every settle poll a
subscribe, upgrade or unsubscribe drives. A Resource that also implements
`RequestObserver` has `ObserveWithRequest` called instead, with that verb's
own `Desired` — secrets included. A Resource that does not implement it is
unaffected and keeps calling `Observe(ctx, Identity)` exactly as before. The
secrets reach `ObserveWithRequest` only for the duration of that one verb
call: the Handler stores them nowhere of its own, the idempotency cache
included, so a later, unrelated request never sees them.

`Delete` already has a route to the request's secrets, through
`DeleteWithRequest` below: `Request.Desired()` copies `Secrets` onto the
`Desired` it returns, the same way it copies `Params`, so a block that
implements `HandoverResource` sees the unsubscribe request's secrets there
too.

A block whose teardown needs the unsubscribe request's own params, or that
must return outputs from tearing down, also implements
`handler.HandoverResource`:

```go
type HandoverResource interface {
	DeleteWithRequest(ctx context.Context, d contract.Desired) (outputs map[string]string, err error)
}
```

The Handler checks for it with a type assertion before calling Delete. A
Resource that also implements HandoverResource has `DeleteWithRequest` called
instead, with the unsubscribe request's full `Desired` — Params included,
unlike the bare `Identity` Delete receives. A Resource that does not
implement it is unaffected and keeps calling `Delete(ctx, Identity)` exactly
as before. Returning an error from `DeleteWithRequest` aborts before
anything is destroyed: the Handler answers the error response immediately,
with no destroy action and no settle poll.

This is the shape the kit's own `handover` package exists for: a block reads
`d.Params["dataHandover"] == "snapshot"` to decide whether to snapshot data
before destroying it, and returns the resulting id under the key
`handover.OutputKey` ("dataSnapshotId") so it lands in the unsubscribe
response's `outputs`, alongside `absent:true`, once the resource settles
absent.

`DeleteWithRequest` must be idempotent. A settle timeout answers
`absent:false` with no outputs, and the platform's redrive calls
`DeleteWithRequest` again, possibly after an earlier call already destroyed
the resource; a second call must find the snapshot (or other handover
artifact) it already made, or report the resource gone, rather than fail.
Its outputs also reach the wire unscrubbed, the same way Observe's do, so
they must never carry secret material.

### Retaining a resource on unsubscribe

A block whose manifest declares `spec.lifecycle.retainsOnUnsubscribe: true`
is one that by design keeps the resource it created — a consumer's own app
repository, say — so a normal teardown's `Observe` poll would just keep
reporting `Present` forever, since nothing ever destroys the resource to
make it go absent. Such a block declares `retainsOnUnsubscribe` so the
Handler settles its unsubscribe without ever probing for absence: as soon as
the `beforeDelete` hook, `Delete` (or `DeleteWithRequest`) and the
`afterDelete` hook have all succeeded, it answers `absent:true` at once,
carrying any `HandoverResource` outputs, with a `statusDetail` explaining
that the block declares it retains its resource so absence was not
observed. `Observe` is never called for that unsubscribe.

A failed `Delete`, `DeleteWithRequest` or delete-side hook is unaffected by
the declaration: it still answers the ordinary error response, never
`absent:true`. A block that does not declare `retainsOnUnsubscribe` — the
field defaults to `false` — keeps the ordinary behaviour: the Handler polls
`Observe` up to `VerbBudget` times and answers `absent:false` if the budget
runs out still non-absent.

### The output-secret gate

Before any response leaves validate, a settled `Present` or a settled
`absent:true`, the Handler refuses one whose `Outputs` carry a value that
exactly matches one of the request's `secrets` values, over the merged
`Observe` and `HandoverResource` outputs. The refusal is a `*handler.Refused`
with `Class` `"secret-in-output"`, and its message names the offending
output key — never the value — so the refusal itself never becomes a second
leak.

The check is exact-value, not substring: an output that merely contains a
secret (an owner login embedded in a repository path, say) is left alone,
since scrubbing every occurrence would also mangle a legitimate output. This
is the gate's honest limit — it catches a secret returned verbatim as an
output value, and it cannot detect one embedded inside a longer value — so a
block must never put secret material in `Outputs`. Secret material belongs
in `SecretOutputs`, which the gate never inspects, since that field is where
a Resource is meant to return a secret on purpose. An empty secret value is
ignored: it names an unset optional secret rather than an actual one, and an
empty output value leaks nothing, so the two never combine into a false
refusal.

### Serving a block that declares its resources

A block whose `mitstack/v1beta` manifest lists its resources under
`spec.resources` implements `handler.DeclaredResource` once for each of them.
The interface has the same `Ensure`, `Observe` and `Delete` as
`handler.Resource`, scoped to one resource. You hand the implementations to
`handler.New` in `Config.Resources`, keyed by the names the manifest gives,
together with the manifest in `Config.Manifest`.

`handler.New` refuses a `Config` that sets both `Resource` and `Resources`,
and one that sets `Resources` with no manifest or with a manifest that is not
`mitstack/v1beta`. It refuses `Hooks` set beside `Resources`. It also
refuses a declared resource with no implementation and an implementation
under a name the manifest does not declare, quoting the name in each case.

The Handler runs each verb resource by resource, because the manifest's
order is creation order and a later resource may depend on an earlier one.

| Verb | What the Handler does |
|---|---|
| subscribe, upgrade | Ensures each resource in manifest order and polls its `Observe` until it reports `Present` before ensuring the next |
| unsubscribe | Deletes each resource in reverse manifest order and polls its `Observe` until it reports `Absent` before deleting the next |
| validate | Observes each resource once, in manifest order |

When every resource is present, subscribe and upgrade answer with the union
of the resources' `outputs` and `secretOutputs`, and a later resource wins a
key both produce. A resource that is still converging when the verb budget
runs out stops the verb there: subscribe and upgrade answer `pending:true`,
and unsubscribe answers `absent:false`. An error from any call stops the
verb the same way and is answered by its kind, as for a single `Resource`.
Validate answers present when every resource is present, `absent:true` when
every resource is absent, and `pending:true` for any other mix.

The Handler checks the request's context before each resource's first call.
Once the context is done, the verb stops before that resource and names it
in `statusDetail`. Subscribe, upgrade and validate then answer
`pending:true`, and unsubscribe answers `absent:false`. The `resources` map
holds only the resources the verb reached before it stopped.

Every answer, an error included, reports each resource the verb reached in
the response's `resources` map. A key under a resource means that call ran
for it, and a resource the verb never reached has no entry. This subscribe
stopped at an instance whose ensure was refused:

```json
{
  "outputs": null,
  "error": "refused:quota: no capacity in eu-west-1",
  "statusDetail": "refused:quota: no capacity in eu-west-1",
  "resources": {
    "network": {
      "ensure": {},
      "observe": {"state": "present", "outputs": {"vpcId": "vpc-0abc"}}
    },
    "instance": {
      "ensure": {
        "error": "refused:quota: no capacity in eu-west-1",
        "statusDetail": "refused:quota: no capacity in eu-west-1"
      }
    }
  }
}
```

Each entry's `error` and `statusDetail` are scrubbed of the request's
secrets, and each entry's `outputs` pass the output-secret gate above. A
block served over a single `Resource` reports no `resources` map and
answers exactly as it always has.

A declared resource that also implements `handler.ReportingResource` reports
what its ensure did. The Handler calls its `EnsureWithReport` in place of
`Ensure`, never both. The `contract.Ensured` it returns carries the identity
the ensure stored for the resource and the outputs it published. Both go on
that resource's `ensure` entry:

```json
"firewall": {
  "ensure": {
    "outputs": {"groupArn": "arn:aws:ec2:eu-west-1:123456789012:security-group/sg-0abc"},
    "identity": {"groupId": "sg-0abc"}
  },
  "observe": {"state": "present", "outputs": {"groupId": "sg-0abc"}}
}
```

An identity or output value equal to a request secret is refused with the
`secret-in-output` class, as an observe output is. The top-level `outputs`
stay the union of what each resource's observe found.

A request may name one declared resource under `resource`, and carry that
resource's inputs under `resourceInputs`. Each input is a scalar or a
mapping, kept as raw JSON. It may also carry the identity that resource's
ensure stored under `resourceIdentity`, an object of strings. The verb then
runs on that resource alone. Subscribe and upgrade ensure it and settle it
present, unsubscribe deletes it and settles it absent, and validate
observes it once. Its ensure receives a `Desired` whose `Resource`,
`ResourceInputs` and `ResourceIdentity` are the request's. The answer's
`resources` map holds that resource's entry alone, and the top-level
`outputs` are its own. A block served over a single `Resource` answers any
named request with a 400 and reaches no resource. The idempotency cache
keys on the name too, so one idempotency key naming different resources
runs each of them, and a repeat of the same named request replays.
`contract.DecodeRequest` refuses `resourceInputs`, `resourceIdentity` or
`resourceType` sent without a `resource`.

A resource the block once created can outlive its place in the manifest, so
the block still has to tear it down. The engine names such a resource with
the type it recorded for it under `resourceType`, beside its stored
identity:

```json
{
  "operation": "unsubscribe",
  "resource": "firewall",
  "resourceType": "aws.security-group",
  "resourceIdentity": {"groupId": "sg-0abc"}
}
```

You register a `handler.DeclaredResource` for each such type in
`Config.Types`, keyed by resource-type id, beside `Config.Resources`.
`handler.New` refuses a key the resource-type catalogue does not hold,
quoting it, and refuses `Types` beside a single `Resource`. An unsubscribe
naming `firewall` deletes it through the `aws.security-group` handler and
settles it absent, and a validate observes it once. The answer reports it
under `firewall` in the `resources` map. Its calls receive a `Desired`
whose `ResourceType` is the request's, so a `RequestDeleter` or
`RequestObserver` can find the resource by type and identity. The Handler
never ensures such a resource. Any other request naming a resource the
manifest does not declare answers 400 with the `undeclared-resource` class
and reaches no resource. That covers a subscribe or upgrade, a request with
no `resourceType`, and a type `Config.Types` holds no handler for. The
error quotes the name and the type, or says that no `resourceType` was
sent.

The Handler scrubs each secret input of the named resource from the answer,
the event it emits for the verb and every log line it writes, as it scrubs
`secretInputs`. An input is
secret when the manifest binds it to a param marked `secret: true`, at the
top level or inside a mapping. It is also secret when the resource's
catalogued type marks it secret, and then every string inside it is secret.

Some declared resources can only find what they created through the inputs
the engine recorded at subscribe and the identity their ensure stored. The
engine sends both back on a later request naming the resource. Such a
resource implements `handler.RequestObserver`, `handler.RequestDeleter` or
both:

```go
type RequestDeleter interface {
	DeleteWithRequest(ctx context.Context, d contract.Desired) error
}
```

The Handler calls `ObserveWithRequest` instead of `Observe` and
`DeleteWithRequest` instead of `Delete`, never the two for one call, and
hands each the verb's own `Desired`. For a firewall whose ensure stored
`{"groupId": "sg-0abc"}`, an unsubscribe naming `firewall` hands its
`DeleteWithRequest` and every settle poll a `Desired` whose
`ResourceIdentity["groupId"]` is `sg-0abc`. A request naming no resource
makes the same calls, with `Resource`, `ResourceInputs` and
`ResourceIdentity` empty. A declared resource that implements neither keeps
receiving the `Identity` alone. `handler.HandoverResource` declares a
`DeleteWithRequest` with a different signature, so assert
`var _ handler.RequestDeleter = (*T)(nil)` to have a mismatch caught at
compile time.

A block proves its declared resources in its own tests with
`kittest.AssertDeclaredResources`, which takes the manifest, the
implementations, any resources the app supplies and the subscription to
drive. It ensures each declared resource twice in manifest order, observing
it present after each ensure with the same outputs both times. It then
deletes each in reverse order and observes it absent. A resource the app
supplies must be present before the first ensure and still present after the
last delete, since a block never deletes what the app supplies. With
`Resource` set, the check drives that one declared resource alone, under a
`Desired` naming it. A resource that implements `handler.ReportingResource` is ensured
through `EnsureWithReport`, and a second ensure that reports a different
identity from the first is a problem. `ResourceInputs` and
`ResourceIdentity`, when set, go on the `Desired` every call is made for, as
the engine hands them on a later request, and so does `ResourceType`. Any
of them set with no `Resource` named, on the input or on its `Desired`, is
one problem naming the fields set, and nothing is driven, since the engine
never sends such a request. With `Resource` naming a resource the manifest
does not declare, the check deletes it through the `Types` handler for its
`ResourceType` and observes it absent, and never ensures it. With no
handler for that type, or with no `ResourceType`, it is one problem and
nothing is driven. A resource that implements `handler.RequestObserver` or
`handler.RequestDeleter` is observed or deleted through it with that
`Desired`. Each problem about a resource names it. blockverify's resources
suite requires a `TestDeclaredResources` test that calls
`kittest.AssertDeclaredResources`, and runs it.

## Testing

```
go vet ./... && go test ./...
```

The module is stdlib-only by design: no third-party dependency has ever been
needed, and none is added.

### Reading a manifest in a test

`kittest.ReadManifest` parses a block manifest file through
`contract.ParseManifest` and fails the test, naming the path, when the file
cannot be read or the parser refuses it. The `kittest.Manifest` it returns
embeds the parsed `contract.Manifest`, so `ID()`, `Version()`, `Block` and
`V1Alpha` stay reachable.

`Section` reads a value by dotted key path, such as `spec.lifecycle` or
`spec.lifecycle.verbBudgets.subscribe`. Each segment is a manifest key: the
json tag on the `mitstack/v1beta` types or the yaml tag on the
`mitstack/v1alpha` types. It returns the field's typed Go value, so
`spec.resources` under v1beta is a `[]contract.ManifestResource`, and it
returns false for a key the manifest leaves unset. A key the types do not
name, or a key below a scalar, fails the test rather than reading as unset.

`SpecScalar` reads `spec.<key>` as text, trimmed, so a folded `>` description
reads as one line. It renders a boolean or an integer with `fmt.Sprint`,
reads a blank string as unset, and fails the test when the key is a section.

```go
m := kittest.ReadManifest(t, "manifest.yaml")
lifecycle, ok := m.Section("spec.lifecycle")
description, ok := m.SpecScalar("description")
```

### Proving no secret leaks

`kittest.AssertNoSecretLeak` drives an operator's own handler with signed
webhook requests that carry seeded secrets. It fails the test for each place
a secret or an OBO token shows up: a `/hook` response, the standard logger's
output, or a line `GET /logs` serves. The OBO token is the on-behalf-of token
the platform signs for each request. `kittest.CheckNoSecretLeak` does the
same work without a `*testing.T` and returns the problems as strings. A
problem names the channel and shows a secret only in redacted form.

Seed each secret wherever the operator reads it: under `secrets`, as a
secret param, or as a secret resource input. Mark a request `Fails` when the
operator's verb answers it with an error, so that the verb's error path runs
and its error text is checked too.

The harness signs each request and adds a fresh `nonce`, `createdAt`, an
`idempotencyKey` when the body has none, and an OBO token for the body's
`orgId` and `subscriptionId`. It also reports a fixture whose passing would
prove nothing. A seeded secret that is empty or in no request body is a
problem. So is a fixture with no request marked `Fails`, and a `Fails`
request that answers below 400. A body the kit's envelope decoder refuses is
a problem, since no verb ran, and it drives no error path. A `/hook` that
answers 401 is a problem too, since the handler did not trust the harness's
signer and no verb ran. A `Serve` that replaces the standard logger's writer
is a problem, since the log output check would then read nothing. A
`GET /logs` that answers other than 200 or serves no lines is the last.

In the example below, `newHandler` is the operator's own constructor, taking
the JWKS URL and the `/logs` buffer, and `apiToken` is the seeded secret. The
kid and seed given to `kittest.NewSigner` only make the signing key
deterministic.

```go
func TestNoSecretLeak_LogsEndpoint(t *testing.T) {
	kittest.AssertNoSecretLeak(t, kittest.SecretLeak{
		Secrets: []string{apiToken},
		Serve: func(logs *webhook.LogBuffer) (http.Handler, *kittest.Signer) {
			signer := kittest.NewSigner(t, "sample-kid", 3)
			h, err := newHandler(signer.JWKSServer(t), logs)
			if err != nil {
				t.Fatalf("newHandler: %v", err)
			}
			return h, signer
		},
		Requests: []kittest.LeakRequest{{
			Name: "subscribe whose Ensure quotes the API token",
			Body: map[string]any{
				"operation":      "subscribe",
				"subscriptionId": "sub-1",
				"blockId":        "secretleak-sample",
				"orgId":          "org-1",
				"secrets":        map[string]any{"api": map[string]any{"token": apiToken}},
			},
			Fails: true,
		}},
	})
}
```

While it runs, the harness redirects the standard logger into its own
capture, the `/logs` buffer it hands to `Serve`, and the writer the standard
logger had before the call. It restores that writer when it returns. The
test must therefore not run in parallel with a test that logs.

## Release

Every push to main that changes the tree cuts the next patch tag
(`scripts/release-operator-kit.sh`, run from CI). A commit message carrying a
`kit-release: minor` or `kit-release: major` line raises that level instead,
and each raise zeroes the levels below it. A skipped or failed release run is
not lost: the next push resolves the base from the newest tag and cuts the
release the earlier push owed.

`go test ./contract/...` compares the resource-type catalogue with the golden
at `contract/goldens/catalogue/resourcetypes.json`, which holds the catalogue
as the previous release shipped it, and fails a breaking change. A breaking
change is a type removed or renamed, a type changing its kind, an output
removed or changing its secret mark, or a new required input.
`contract.CatalogueMajor` records the major version being released, and a test
holds it equal to the major the module path declares.

A breaking catalogue change takes `kit-release: major` and raises
`contract.CatalogueMajor` by one in the same change. From v2 on, Go also
requires the module path to end in `/v2` (or the matching major) for a major
release, so the same change moves the module path and every import. After a release whose catalogue changed, a follow-up change refreshes
the golden with
`go run ./cmd/catalogue-golden > contract/goldens/catalogue/resourcetypes.json`.
A change that alters the catalogue never refreshes the golden itself, since
the golden is what the comparison reads as the previous release.

## Recording a delivery on the board

Every push to main also records which board issues it delivered, through
`cmd/board-delivery`. The `boardkeys` package derives the keys a commit
delivers: a key counts only when it sits in a leading key list in the
commit's subject, terminated by a colon, or in a whole-line `Jira:` trailer
in the body. A key anywhere else in a subject, or in an ordinary body line,
is prose and delivers nothing.

`board-delivery` reads a local `board-synced` git tag as the anchor for its
range: the commits since that tag, or since the push event's `before` sha
when no tag exists yet, or the single pushed commit when neither is usable.
It posts the derived keys to the board's `/v1/delivered` endpoint once, and
only after that write succeeds does it move the `board-synced` tag to the
pushed commit and push it, so the next run picks up exactly where this one
left off. A push whose commits deliver no keys records nothing and still
succeeds. A push with keys but no `TRACKER_BASE_URL` configured fails the
run, since a delivery that silently skips the board looks the same as one
that reached it.

## Gating a pull request on a board key

Every pull request also runs `cmd/pr-keys`, which fails the `pr-key-gate`
check when neither its commits nor its own title carries a board key. Both
halves are checked because either can be the one that reaches main: a merge
commit or a rebase delivers the range's commits, while a squash delivers
only the pull request's title, folding every commit into that one line. A
key has to lead a commit subject's key list (`MIT-123: fix the thing`) or
sit on its own `Jira:` trailer line, and the same leading-list rule applies
to the title — a key that only appears inside a merge commit's default
branch name is not delivery intent, and the gate does not count it.
