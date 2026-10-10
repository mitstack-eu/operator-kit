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

Under `mitstack/v1beta` a block names what it requires and what it creates by
exact type, as ADR 0085 sets. Each `requires` or `resources` entry is a type
id, or a mapping of `type`, `name` and, for a requirement, `many`. The `type`
is one id or a list of ids:

```yaml
requires:
  - github.repository
  - { name: host, type: [aws.ec2-instance, hetzner.server] }
resources:
  - aws.vpc
  - { name: deploy-role, type: aws.iam-role }
  - { name: app-container, type: [aws.ec2-container, hetzner.server-container] }
provides: [app-container, deploy-role]
```

`provides` lists the names of the resources the block offers to the rest of
the app. It replaces the capability slugs that older manifests list.

An unnamed resource is named by its type id, and an unnamed requirement is
referenced by it. An entry that lists more than one type needs a name, and
two entries that would share a name or a reference are refused. The parser
refuses the retired `kind`, `in`, `on` and `inputs` keys, naming the key and
the entry. It judges the type id grammar only, so it needs no type snapshot.

Whether the platform holds a type is a question for the type snapshot the
platform serves at `GET /v1/resource-types/snapshots/{version}`.
`contract.ParseTypeSnapshot` reads one, and `contract.CheckManifestTypes`
refuses an entry listing a type the snapshot does not hold.
`contract.CheckBinding` judges a binding a block's code reads, such as
`requires.host.outputs.hostId` or `resources.deploy-role.outputs.roleArn`. It
refuses a reference the block lacks, and an output that any listed type does
not declare in the snapshot. Neither reads the kit's own catalogue.

A snapshot type may carry `"endsWithSubscription": true`, read into
`SnapshotType.EndsWithSubscription`, and a type without the field is
unmarked. The mark is for a resource whose absence cannot be observed, such
as a scaffold contribution's files in an app repository the platform keeps.
Such a resource ends when the block's unsubscribe succeeds.

`ManifestRequirement.Types` and `ManifestResource.Types` hold the listed ids,
and `ManifestResource.Type` holds the one id of an entry that lists one.
`ManifestRequirement.Kind` and `In`, `ManifestResource.Inputs` and `On`, and
`contract.InputValue` are deprecated, stay empty after a parse, and go in the
next major release.

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
`spec.resources` creates and orders them itself, through its block-level
handler. That is the one `handler.Resource` the block's operator gives the
kit in `Config.Resource` to create, update and delete all its resources. The
operator also hands `handler.New` one `handler.Observer` per declared
resource in `Config.Observers`, keyed by the names the manifest gives:

```go
type Observer interface {
	ObserveResource(ctx context.Context, r contract.ResourceRef) (contract.Observation, error)
}
```

An observer asks the provider what stands right now, and never answers from
what an ensure remembered. Its `contract.ResourceRef` carries the resource's
name, the type it was created as, the identity that addresses it when that
is known, and the verb's `Desired`.

| Verb | What the Handler does |
|---|---|
| subscribe, upgrade | Runs one `Ensure`, then observes each declared resource in manifest order until it reports `Present` |
| unsubscribe | Runs one `Delete`, or `DeleteWithRequest` for a `handler.RequestDeleter`, then observes each declared resource in reverse order until it reports `Absent`, skipping one whose type `Config.Types` marks `endsWithSubscription` |
| validate | Observes each declared resource once, and names the resources an upgrade would replace |

A block-level handler that implements `handler.ResourceReporter` reports
what its ensure created, and the Handler calls `EnsureResources` in place of
`Ensure`. The shared observers find a resource only by the identity reported
here, so a block that uses them implements it:

```go
type ResourceReporter interface {
	EnsureResources(ctx context.Context, d contract.Desired) ([]contract.CreatedResource, error)
}

type CreatedResource struct {
	Name     string            // the manifest's name for the resource
	Type     string            // the exact type it was created as
	Identity map[string]string // the values that address it, such as {"instanceId": "i-0abc"}
}
```

Each observer is handed the identity and type reported for its resource.
When nothing was reported, it gets what the engine recorded under the
request's `recordedResources`, and failing that, no identity and the one
type its entry lists. A recorded type reaches the observer even when the
entry no longer lists it, since the resource was created as that type. A
reported resource the manifest does not declare, or of a type its entry does
not list, answers 400 quoting it. A handler that implements
`handler.ReplacementPlanner` names on validate the resources an upgrade would
replace, and the answer carries them under `replaces`.

Every observe is handed `Desired.VerbAt`, the time of the verb it settles,
and reads only what was made after it. After `Ensure` or `Delete` that is
the Handler's clock once the call returned. On a validate it is the request's
`settlesAt`, the time of the state-changing verb the validate settles, and
otherwise the request's `createdAt`. A validate request and its answer look
like this:

```json
{"operation": "validate", "settlesAt": "2026-10-07T12:00:00Z",
 "recordedResources": {"aws.ebs-volume": {"type": "aws.ebs-volume", "identity": {"volumeId": "vol-0abc"}}}}
```

```json
{"outputs": null, "absent": false, "replaces": ["aws.ebs-volume"],
 "resources": {"aws.ebs-volume": {"observe": {"state": "present", "type": "aws.ebs-volume", "identity": {"volumeId": "vol-0abc"}}}}}
```

The answer's `resources` map holds an `observe` entry per resource with its
state, outputs, identity and `type`. The top-level `outputs` and
`secretOutputs` are the union of the resources `spec.provides` names.

In that union a later resource wins a key two resources share, so two
`aws.iam-role` resources would lose one `roleArn`. The answer therefore also
carries `resourceSecretOutputs`, which maps each provided resource's name to
its own secret outputs:

```json
{"resourceSecretOutputs": {
  "deploy-role": {"roleArn": "arn:aws:iam::111111111111:role/deploy"},
  "ecr-push-role": {"roleArn": "arn:aws:iam::111111111111:role/ecr-push"}}}
```

A resource with no secret output has no entry there. No secret output ever
appears in `resources`, because the engine records that map.

A resource not present within the verb budget answers `pending:true` naming
it, and unsubscribe answers `absent:true` only when every resource observes absent.
The exception is a resource whose type `Config.Types` marks
`endsWithSubscription`. The Handler counts it absent once the delete
succeeds and never observes it, so its entry carries a `delete` outcome of
state `absent` and no `observe`:

```json
{"absent": true,
 "resources": {"github.scaffold-contribution": {"delete": {"state": "absent", "type": "github.scaffold-contribution",
   "statusDetail": "type github.scaffold-contribution ends with its subscription, so the resource is absent once the delete succeeds, unobserved"}}}}
```

A failed delete still answers the ordinary error, and a block given no
`Config.Types`, or a snapshot leaving the type unmarked, observes the
resource as any other. A block passes `Config.Types` the snapshot its
manifest is judged against.
A request naming one resource is served for validate alone, observing that
resource with the request's `resourceIdentity` and `resourceType`. A named
subscribe, upgrade or unsubscribe answers 400. The request's
`requirementTypes` say which exact type met each requirement.

A validate can name a resource that an earlier version declared and the
current manifest no longer does. `Config.TypeObservers` serves that case. It
holds observers keyed by resource-type id, and the Handler hands the request's
`resourceType` and `resourceIdentity` to the observer filed under that type. A
name the manifest does not declare and no type observer holds answers 400,
quoting the resource and its type. So does a named subscribe, upgrade or
unsubscribe.

`handler.New` refuses a declared resource with no observer, an observer the
manifest does not declare, `Observers` beside `Resources`, and `Observers`
without a `mitstack/v1beta` manifest. It refuses `TypeObservers` on the same
two grounds, and also refuses a key that is not a resource-type id. The trust
boundary, idempotency, secret scrubbing, events and error classes work as for any other block.

The `observe` package holds the kit's shared observers, one per type, each
over a small API interface the operator implements. They include
`EC2Instance`, `EgressProxy`, `S3Bucket`, `VPC`, `SecurityGroup`, `EBSVolume`,
`ECRRepository`, `IAMRole`, `Route53Record`, `SSMAssociation`, `EC2Container`,
`EC2PostgresDatabase`, `HetznerServer`, `HetznerPrimaryIP`, `HetznerFirewall`,
`HetznerNetwork`, `GitHubRepository`, `GitHubCommit`, and `NodeReported`. A
host probe never reads a failed probe as absence: `HostCommander` returns a
nonzero exit as an error, and empty output is unknown.

`EC2PostgresDatabase` probes the Postgres container postgres-db runs for
each subscription, named by the identity's `instanceId` and `container`, the
way postgres-db does. Its probe prints `PRESENT=1`, `PRESENT=0` when Docker
answers without the container, or `PRESENT=unknown` when Docker does not
answer, and then for a present container `READY=` and `pg_isready`'s exit
status. The container is present once `READY=0`, absent on `PRESENT=0`, and
otherwise unknown. It reports `dbName` only when the identity carries
`database`. `EC2PostgresDatabase` and `NodeReported` report presence and
identity only, so a block that provides such a resource reports the type's
other required outputs from its own observer.

`NodeReported` serves a resource the platform has no route to, such as a
`kubernetes.namespace`, from node reports. A node report is the report a
node of the Hetzner cluster sends the platform, read at
`GET /v1/subscriptions/{id}/node-reports`. Reports are stored under the
cluster's own subscription, so the identity names it as
`clusterSubscription`, beside the object's `kind`, `name` and optional
`namespace`; without it, `NodeReported` reads the observing subscription's
own reports. An object matches only when it also carries the observing
subscription's id, since an object is labelled by the subscription that
created it. A report's body lists those objects under
`subscriptionResources`, and may carry
`madeAt` and `subscriptionResourcesComplete`. A report counts when its
`madeAt`, or its receipt time when it has none, is after `VerbAt`, and the
newest counting report decides. An object it lists is present. An object it
does not list is absent only when the report carries a `madeAt` after
`VerbAt` and `subscriptionResourcesComplete: true`. Otherwise it is unknown.

`observe.AWSTagged` and `observe.HetznerLabelled` list everything carrying a
subscription's id, under the AWS tag `mitstack:subscription`
(`observe.AWSSubscriptionTag`) or the Hetzner label
`mitstack.dev/subscription` (`observe.HetznerSubscriptionLabel`).

A block proves its guards in its own tests with `kittest.AssertBlock`. It
takes a `kittest.Block`: the manifest, the type snapshot, the block-level
handler, the observers, a `kittest.Provider` and the subscription. It serves
the block through `handler.New` and signed requests, so it holds the block
to the Handler's own rules. After subscribe, every declared resource must
observe present with an identity, and every provided one with the outputs
its type marks required. Everything the provider lists under the
subscription's id must match a declared resource by type and count. A
resource the provider loses must then observe not present. With `Upgrade`
set, a resource the upgrade replaces must be named in the `replaces` of the
validate before it, and the problem says so when its type is stateful.
`Upgrade` nil while a declared type is stateful is a problem. After
unsubscribe every declared resource must observe absent, except one whose
type the snapshot marks `endsWithSubscription`, since the check serves the
handler with `Config.Types` set to that snapshot. A requirement
listing more than one type runs the check once per listed type, with
`Requirements` giving each type's outputs, and each problem names the type.
`AssertBlock` logs which guards ran. blockverify's resources suite requires
a `TestDeclaredResources` test that calls `kittest.AssertBlock`, runs it, and
names in its pass detail the guards that ran.

### Serving a block one handler per resource

This mode is deprecated: `Config.Resources`, `handler.DeclaredResource`,
`handler.ReportingResource`, a request running ensure or delete on one named
resource, and `resourceInputs` go in the next major release.

A block whose `mitstack/v1beta` manifest lists its resources under
`spec.resources` may implement `handler.DeclaredResource` once for each of them.
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
key both produce. Validate answers the same union when every resource is
present.

That merge would leave two `aws.iam-role` resources with one `roleArn`
between them. So subscribe and upgrade, and validate when every resource is
present, also answer `resourceSecretOutputs`, which maps each resource's name
to its own secret outputs.

A resource that is still converging when the verb budget
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
`outputs` are its own. A name the manifest does not declare answers 400
naming it, and no resource is reached. A block served over a single
`Resource` answers any named request the same way. The idempotency cache
keys on the name too, so one idempotency key naming different resources
runs each of them, and a repeat of the same named request replays.
`contract.DecodeRequest` refuses `resourceInputs` or `resourceIdentity` sent
without a `resource`.

The Handler scrubs each secret input of the named resource from the answer,
the event it emits for the verb and every log line it writes, as it scrubs
`secretInputs`. An input is secret when the resource's catalogued type marks
it secret, and then every string inside it is secret.

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
`Desired` naming it, and a name the manifest does not declare is one
problem. A resource that implements `handler.ReportingResource` is ensured
through `EnsureWithReport`, and a second ensure that reports a different
identity from the first is a problem. `ResourceInputs` and
`ResourceIdentity`, when set, go on the `Desired` every call is made for, as
the engine hands them on a later request. Either one set with no `Resource`
named, on the input or on its `Desired`, is one problem naming the fields
set, and nothing is driven, since the engine never sends such a request. A
resource that implements `handler.RequestObserver` or
`handler.RequestDeleter` is observed or deleted through it with that
`Desired`. Each problem about a resource names it. blockverify's resources
suite also accepts a `TestDeclaredResources` test that calls
`kittest.AssertDeclaredResources`.

### Deploying a declared resource through GitOps

The `gitops` package serves a declared resource that deploys through Argo CD,
the controller on the cluster that applies whatever the GitOps repository
holds. It commits the block's rendered manifests to that repository and judges
the deploy from Argo CD's notification, which the platform relays. A
`gitops.Resource` implements `handler.DeclaredResource` along with the
reporting, request-observer and request-deleter interfaces. The Handler
therefore drives it per call, with that call's own `Desired`.

You give a `gitops.Resource` three functions: one returns the
`gitops.Repository` the resource lives in, one returns its notification source
and one renders its files. `gitops.NotificationsFromContext` builds that
source from the events callback on the request. The events callback is the URL
and token the platform puts in every lifecycle request, and the package reuses
them to read the notifications the platform relayed.

```go
resource := &gitops.Resource{
	Repository: func(ctx context.Context, d contract.Desired) (gitops.Repository, error) {
		return repoFor(ctx, d)
	},
	Notifications: func(ctx context.Context, _ contract.Desired) (gitops.Notifications, error) {
		return gitops.NotificationsFromContext(ctx)
	},
	Render: renderNamespace,
}
```

Its ensure commits the rendered files and reports the app, the revision and
the paths as the resource's identity. When the recorded identity names a path
the render no longer has, as after an upgrade renames or moves a file, the
same commit removes that path, in whatever directory it stands. Only the
resource's own ensure writes that record, so it names only paths the resource
wrote. The ensure removes anything only when the record's app is the render's
own app, so a record of another app, or no record, removes nothing. When the
commit would change nothing, the ensure reports the revision its record names,
as long as that revision still holds every rendered file, and head otherwise.

A rendered file that carries any of the request's secrets, verbatim or
base64-encoded on its own, is refused as `secret-in-manifest` before anything
is written. That check is a backstop. A secret encoded inside a larger value,
such as the token in a `.dockerconfigjson` auth field, passes it. So does a
secret in raw or URL-safe base64, or in any other encoding. A block
therefore never renders secret material, and a secret reaches a slice through
External Secrets instead (platform ADR 0072).

Its observe reads the app's newest notification, whatever revision it names,
and judges it only when the files at that revision match the render. Say the
ensure committed revision `a1b2` and the newest notification names a later
commit `c3d4`: observe reads the files at `c3d4`, finds the rendered files
there, and lets the sync of `c3d4` decide the deploy. It works this way
because Argo CD may sync a later revision than the one committed, so the
notification for a change can name a later commit. A live sync on a scratch
cluster with two commits inside one poll interval would show whether that
happens, and it has not been observed yet. A revision whose sync reports phase
`Succeeded` and health `Healthy` is deployed, while the sync status and the
images are never read.

Observe answers unknown in three more cases. The first is when head holds the
rendered files only in part, or with other content. The second is when Argo CD
has reported no deploy of the app yet. The third is when the newest
notification names a revision that does not yet carry the change at head. A
resource that was never ensured is therefore absent only in an empty
repository. In a repository that holds other apps' commits, it stays unknown
until Argo CD reports on the app. A sync in phase `Error` or `Failed`, or with
health `Degraded`, makes observe return an error carrying the phase, the
health and the message.

`gitops.Check` and `gitops.Wait` take the other approach and judge only the
notification for the exact revision you hand them. Use them when you need the
outcome of one particular commit, since a notification for any other revision
leaves them pending. `Wait` polls until the deploy succeeds or fails, and a
failed deploy is an outcome rather than an error.

A block-level handler ships its whole desired state with `gitops.Deploy`. It
takes a `gitops.BlockDeploy` holding the app, every file, the commit message,
the request's secret values and the poll interval. It commits every file once
through `CommitManifests` and waits on that revision, or on head when nothing
changed. The `gitops.Deployed` it returns carries the revision, whether it
committed, and Argo CD's verdict, so a failed sync comes back in
`Deployed.Outcome` for the block to refuse:

```go
func (b *block) Ensure(ctx context.Context, d contract.Desired) error {
	out, err := gitops.Deploy(ctx, b.repo, b.notes, gitops.BlockDeploy{
		App:     d.AppID,
		Files:   render(d),
		Message: "deploy " + d.AppID,
		Secrets: gitops.SecretValues(d),
		Poll:    5 * time.Second,
	})
	if err != nil {
		return err
	}
	if out.Outcome.Verdict == gitops.VerdictFailed {
		return handler.Refuse("deploy-failed", "Argo CD's sync of %s failed: %s", out.Revision, out.Outcome.Notification.Message)
	}
	return nil
}
```

Its delete removes the paths the record names, without rendering, and falls
back to the rendered paths when there is no record. Paths already gone commit
nothing, so deleting a resource that was never created succeeds.
`gitops.Revert` restores every path one revision changed to its content at
that revision's parent.

The platform answers a block only the notifications of the cluster its
`kubernetes-cluster` requirement binds, within the block's own org. A block
whose requirement binds no cluster, or several, reads none, so its observe
answers unknown. A cluster that hosts apps of several orgs needs a scoping
rule of its own, which the platform does not provide. A notification looks
like this:

```json
{"app":"app-alpha","project":"app-alpha","sync":"Synced","revision":"9ee9b7e28c000b16a89b7b717e5f3b9fa6fcf611","health":"Healthy","phase":"Succeeded","message":"successfully synced (all tasks run)","images":null,"reconciledAt":"2026-09-28T10:47:23Z"}
```

`gitops.NewGitHubRepository` gives the kit's implementation of
`gitops.Repository` over GitHub, and it commits to the repository's `main`. It
takes the API base from the request's `github` instance, resolved through
`ghapi.BaseFromEnv`, and the token from the request's `github` secret. Apart from
the empty revision `""`, every revision it reads at or commits on must be a
full lowercase hex SHA of 40 or 64 digits, and anything else is refused before
a request goes out. File content
must be UTF-8, because the Git Data API carries it as a JSON string. A commit
whose parent is no longer the tip lands nothing, so `CommitManifests` and
`Revert` retry it on a fresh head a bounded number of times. In tests, `gitopstest.NewRepository`
and `gitopstest.NewNotifications` give in-memory fakes of both.

### Building an SSM host

An operator that runs scripts on an EC2 instance through AWS Systems Manager
(SSM) builds its host with `hostexec.SSMHost`. The kind it passes is the
`execKind` its app-host binding gives it. `SSMHost` accepts only the `aws-ssm`
kind and refuses any other with an error that matches `hostexec.ErrNotSSM` and
repeats nothing from the host.

`SSMHost` takes the target, credential and region as adjacent strings, so a
caller passes them in that order, and a block that sets no credential passes
`""`. The target and region are left for `hostexec.Run` to judge.

### Driving a CloudFormation stack

An operator names its CloudFormation stack with `awsnames.StackName`, which
prefixes `mitstack-` to the id, turns every character outside the ASCII letters
and digits `[A-Za-z0-9]` into a dash through `awsnames.SanitiseCFNName`, and
cuts the result to CloudFormation's limit of 128 characters.

The operator drives the stack through `cfnstack.New`. It writes a short adapter
from its own AWS SDK client to `cfnstack.Client`, which keeps the kit free of
any dependency outside the standard library. `EnsureStack` creates the stack
and updates it instead when the create fails with an error whose `ErrorCode()`
is `AlreadyExistsException`, which the SDK's typed exception satisfies. An
update with nothing to change succeeds, and so does deleting a stack that does
not exist.

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

## Judging the comments in a pull request

`cmd/commentcheck` judges the Go and TypeScript comments a pull request adds,
comparing the working tree with the pull request's base commit. This
repository's `comment check` job runs it on every pull request. For a block,
`publish-block.yml` runs it in the conformance job once the block pins a kit
release that carries the command.

The check fails when an added comment holds a ticket id, which is `MIT-` or
`MS-` followed by digits, or a `file:line` reference. A comment says what the
code does, and the ticket belongs in the commit subject.

A file's comment share is the fraction of its lines that are comment lines,
meaning lines whose first non-blank character lies inside a comment. The check
also fails when a file the pull request adds, changes or renames holds a share
above its target, whatever the file held at the base. The target is 7% for a
production file and 6% for a test file, where a test file is a `_test.go` file,
a `.test` or `.spec` TypeScript file, or anything under `__tests__`. A file the
pull request leaves alone is not judged, nor is a deleted or generated file.

You can run the check before you push, against the commit your branch started
from:

```sh
base="$(git merge-base origin/main HEAD)"
go run ./cmd/commentcheck -base "$base" .
go run ./cmd/commentcheck -base "$base" -same-code .
go run ./cmd/commentcheck -report cmd/commentcheck
```

The second command proves a change only removes or rewrites comments, and it
fails naming any file whose code differs from the base. The third prints the
comment share of a file or directory and needs no base. The check reads the
working tree as the head, so a file you moved without `git mv` is judged as a
new file until you stage the move.
