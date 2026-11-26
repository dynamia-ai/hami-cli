# hami-cli resource-pool operations

Use the section for the requested operation. The examples use Bash and `jq`. Substitute the authorized context and object names. In scripts, use `set -euo pipefail` so a failed read or write stops the sequence.

## Set target context and capture a baseline

Set the context once. These four list commands form the baseline for a full pool lifecycle. For a narrower task, read the collections needed to identify its targets, references, and effects.

```bash
HAMI_CONTEXT=TARGET_CONTEXT
HAMI=(hami-cli --context "$HAMI_CONTEXT" --request-timeout 20s)

"${HAMI[@]}" version
"${HAMI[@]}" pool list -o json
"${HAMI[@]}" device list -o json
"${HAMI[@]}" pool bindings -A -o json
"${HAMI[@]}" pool regex-bindings list -o json
```

The list commands accept `-o table|name|json|yaml`. Use JSON for evidence. `pool describe` and regex preview have their own text output.

If the regex kind is unavailable, record that in the baseline. Rule CRUD needs the CRD. Preview still works with permission to list Namespaces. A forbidden or failed discovery request leaves the corresponding state unknown.

For a full lifecycle, record these baseline mappings:

- Pool names, quotas, and workload scheduling defaults
- Explicit binding namespace, name, and `spec.poolRef.name`
- Regex rule ID (`metadata.name`), `spec.poolRef.name`, and `spec.namespaceRegex` when the CRD exists
- Device object name, physical identity, and `spec.poolRef.name`
- Target object UID whenever a later cleanup step depends on ownership

Ignore `resourceVersion`, timestamps, and controller status when checking whether unrelated desired state changed.

## Manage pools, quotas, and scheduling policies

### Create a pool

Before creating a temporary pool, confirm the name is absent from `pool list -o json` and coordinate use of that name through confirmed deletion.

Create a pool with optional aggregate CPU and memory ceilings:

```bash
HAMI_POOL=training
"${HAMI[@]}" pool create "$HAMI_POOL" \
  --cpu-quota 128 \
  --memory-quota 512Gi
"${HAMI[@]}" pool describe "$HAMI_POOL"
```

After a successful create, read `pool list -o json` and record the new pool UID. An `AlreadyExists` response or an ambiguous timeout does not establish ownership. Do not delete that pool later without independent proof.

`pool create` applies these quota rules:

- `--cpu-quota` and `--memory-quota` accept optional Kubernetes quantities. The CLI rejects negative values before calling the API.
- An omitted dimension is unlimited. `pool create` rejects the literal `unlimited`.
- Zero (`0`) is a valid hard ceiling that blocks new non-zero usage. Zero does not mean unlimited.
- CPU and memory quota accounting uses Pod limits. For each controlled dimension, specify a positive workload limit. Requests are optional for this accounting and may differ from limits.
- `pool create` sets the requested ceilings in `spec.quota`. The manager owns `status.quota`.

Account for these server-side creation rules:

- The API server rejects recreating a pool name while a non-terminal Pod still references an earlier pool with that name. Choose a new pool name or wait for those Pods to terminate. Never remove manager-owned Pod membership metadata.
- A newly created empty pool is healthy from the controller perspective when its current generation reports `Accepted=True`, zero discovered and healthy devices, and `Ready=False` with reason `NoHealthyDevices`. Do not wait for `Ready=True` until a healthy device is assigned and discovered.

### Update quotas and workload scheduling defaults

Use `pool update` to set or change workload scheduling defaults and quotas while preserving omitted fields:

```bash
"${HAMI[@]}" pool update "$HAMI_POOL" \
  --node-scheduler-policy spread \
  --gpu-scheduler-policy topology-aware,mutex \
  --cpu-quota 192 \
  --memory-quota 768Gi \
  --wait-for-current \
  --wait-timeout 30s
"${HAMI[@]}" pool describe "$HAMI_POOL"
```

`pool update` enforces these flag and reconciliation rules:

- Specify at least one scheduling-policy or quota flag.
- `--node-scheduler-policy` accepts `binpack` or `spread`.
- `--gpu-scheduler-policy` accepts an ordered, comma-separated list from `binpack`, `spread`, `topology-aware`, `mutex`, and `numa`.
- Workload defaults apply to Pods that do not already carry the corresponding scheduler annotations.
- `pool update` validates flags before reading the pool and sends a merge patch with an optimistic lock. If a conflict occurs, re-read the pool before retrying.
- `--wait-for-current` waits after the patch succeeds until manager-owned quota status reflects the written generation. When all quotas are removed or unset, it waits until `status.quota` is absent and `status.observedGeneration` matches the written generation.
- `--wait-timeout` defaults to `30s`, must be greater than zero, and requires `--wait-for-current`. It bounds total reconciliation wait time independently of `--request-timeout`.
- If another actor replaces the pool or writes a newer generation during the wait, `--wait-for-current` fails closed.
- A `--wait-for-current` timeout or fail-closed error does not roll back the spec patch, which already succeeded before polling started. Re-read the pool before retrying.

Remove one quota dimension without changing the other:

```bash
"${HAMI[@]}" pool update "$HAMI_POOL" \
  --cpu-quota unlimited \
  --wait-for-current
```

Setting both dimensions to `unlimited`, or removing the last remaining quota dimension, clears `spec.quota`.

If quota enablement is rejected because Pod admissions are in flight, wait for them to settle and re-read the pool before retrying.

### Inspect a pool

Use `pool describe` for a human-readable summary of one pool, its quota ledger, explicit bindings, regex rules, and devices:

```bash
"${HAMI[@]}" pool describe "$HAMI_POOL"
```

Check these details when reading `pool describe` output:

- Trust reported CPU and memory used and reserved values when the same read shows `Quota ledger` as `current (generation N)`, or `status.quota.observedGeneration` equal to `metadata.generation` in JSON. A pending or stale ledger cannot confirm available quota.
- `Explicit namespace bindings:` lists explicit `ResourcePoolBinding` objects matching `spec.poolRef.name` across all namespaces (`<none>` when empty).
- `Regex bindings:` appears right after `Explicit namespace bindings:` and before `Devices:` when matching `ResourcePoolRegexBinding` rules exist. If the regex CRD is absent or no rules match, `pool describe` omits that section.
- `Devices:` lists `GPUDevice` objects matching `spec.poolRef.name`, including the `DEVICE-TYPE` column (`<none>` when empty).
- Describe needs `get` on the pool and `list` on bindings across namespaces, devices, and regex rules when served. It propagates a forbidden list error. For a quota-only read, `pool list -o json` returns the spec and ledger without querying these associations.

## Manage pool and device ownership

Inspect discovered physical devices across the cluster or on one node:

```bash
"${HAMI[@]}" device list
"${HAMI[@]}" device list --node NODE_NAME -o json
```

`device list` and `pool describe` include a `DEVICE-TYPE` column so same-node device IDs remain distinct across device backends.

### Assign a device

After checking the move conditions below, choose one command. Use the bare `metadata.name` or the exact physical identity from `device list -o json` (`spec.identity.nodeName`, `spec.identity.deviceType`, and `spec.identity.deviceID`). The object-name form cannot be combined with identity flags.

```bash
"${HAMI[@]}" device assign GPUDEVICE_NAME --pool "$HAMI_POOL"
"${HAMI[@]}" device assign --node GPU_NODE --device-id GPU_UUID --pool "$HAMI_POOL"
"${HAMI[@]}" device assign --node NPU_NODE --device-type Ascend310P \
  --device-id '01-aBc_0' --pool "$HAMI_POOL"
```

Follow these device-identity rules:

- `--device-type` defaults to `NVIDIA`. The CLI rejects empty values or surrounding whitespace.
- For Ascend, pass the exact, case-sensitive scheduler `commonWord` from `spec.identity.deviceType`, such as `Ascend310P` or `Ascend910B`.
- Pass `--device-id` verbatim from `spec.identity.deviceID`, preserving case, leading zeros, and separators. It does not need to be a UUID. The Ascend ID above is illustrative. Copy the exact value reported by your cluster.
- `device assign` sends a merge patch with an optimistic lock. On conflict, re-read the device before retrying.

### Ascend server prerequisites and allocation semantics

Ascend assignment requires cluster-side Ascend support and the matching enabled scheduler profile. Use `device list -o json` to find the device and its exact type. The `--device-type` flag selects a device from inventory.

Check these allocation and safety conditions before moving any device:

- Record the device UID, pool reference, discovery and health state, allocations, and `resourceVersion` before writing.
- For Ascend hard-split or unknown mode, inventory omits `status.corePercent`, and hard-split allocations can report zero core percent. Zero or absence does not prove idle hardware.
- `status.vgpuCount` counts sharing slots, while each `GPUDevice` represents one physical device.
- An empty allocation list does not exclude concurrent scheduling. Coordinate a scheduling freeze or obtain owner confirmation before moving a live device.
- For a temporary lifecycle, select only an originally unassigned spare device under exclusive scheduling coordination. Do not move the only healthy, allocated, or previously assigned device to smoke-test a command.

### Unassign or restore a device

For temporary cleanup of an originally unassigned device, choose one form. Use the UID from the pre-assignment read and the temporary pool name.

```bash
"${HAMI[@]}" device unassign GPUDEVICE_NAME \
  --expected-uid "$HAMI_DEVICE_UID" \
  --expected-pool "$HAMI_POOL"

"${HAMI[@]}" device unassign --node NPU_NODE --device-type Ascend310P \
  --device-id '01-aBc_0' \
  --expected-uid "$HAMI_DEVICE_UID" \
  --expected-pool "$HAMI_POOL"
```

`device unassign` applies these ownership guard rules:

- Pass `--expected-uid` and `--expected-pool` together with non-empty values. The CLI rejects passing only one flag or passing empty strings.
- If the current UID or pool differs, `device unassign` fails closed before patching. If another actor changes the device after the internal read, the optimistic patch conflicts.
- Omitting both flags clears whatever pool the internal read observes. Do not use the unguarded form for temporary cleanup in a shared cluster.
- To restore a non-empty original pool as part of an authorized move, verify the recorded UID and interim assignment, then run `device assign DEVICE_NAME --pool ORIGINAL_POOL` and read back the result. Coordinate ownership through the write. Assign accepts no caller-supplied UID or previous-pool precondition. Leave a requested permanent move in place.

## Manage explicit namespace bindings

### Audit explicit bindings

List explicit namespace bindings across all pools or for one pool:

```bash
"${HAMI[@]}" pool bindings -A
"${HAMI[@]}" pool bindings --all-pools -o json
"${HAMI[@]}" pool bindings "$HAMI_POOL"
"${HAMI[@]}" pool bindings "$HAMI_POOL" -o json
```

`pool bindings` requires either one pool name or `-A` (`--all-pools`), never both. Both forms require `list` on ResourcePoolBindings across all namespaces and reject `--namespace`. The name output can repeat the same binding name in different namespaces. Filter the JSON by namespace for an unambiguous result.

```bash
HAMI_NAMESPACE=team-a
"${HAMI[@]}" pool bindings "$HAMI_POOL" -o json |
  jq --arg namespace "$HAMI_NAMESPACE" '{items: [.items[]? | select(.metadata.namespace == $namespace)]}'
```

An explicit binding is controller-confirmed when three facts match: its namespace, name, and `spec.poolRef.name` match the target, `status.observedGeneration` equals `metadata.generation`, and its `Bound` condition has status `True`.

### Grant or remove an explicit binding

Use the authorized, existing namespace. With cluster-scoped `list namespaces` permission, a preview of its exact name can confirm existence at the time of the read. Disposability requires separate evidence.

To grant access, `pool bind` sends a create request for a ResourcePoolBinding in the selected namespace. Verify its namespace, name, pool, and current Bound condition.

```bash
"${HAMI[@]}" pool bind "$HAMI_POOL" --namespace "$HAMI_NAMESPACE"
"${HAMI[@]}" pool bindings "$HAMI_POOL" -o json
```

To remove that explicit grant, use the same namespace and pool. Verify the binding is absent. Before reporting that the namespace lost pool access, also check the remaining regex rules for this pool. Unbind does not evict existing Pods.

```bash
"${HAMI[@]}" pool unbind "$HAMI_POOL" --namespace "$HAMI_NAMESPACE"
"${HAMI[@]}" pool bindings "$HAMI_POOL" -o json
```

### Temporary explicit binding workflow

1. Coordinate with other writers for the target namespace and binding name during creation and UID capture. Confirm the relationship is absent in `pool bindings POOL -o json`.
2. Run `pool bind POOL --namespace NAMESPACE` once. On success, read the binding and save its non-empty UID as `HAMI_BINDING_UID`. Wait for [the current Bound condition](#verify-bounded-convergence). A duplicate or ambiguous create result gives no ownership evidence.
3. Before cleanup, re-read the namespace, name, pool and recorded UID. Keep the identity under exclusive coordination from that read through `pool unbind`, which deletes by namespace and name. If a replacement could be deleted, leave the binding in place and report it.
4. Verify absence and compare the remaining relationships with the saved map.

## Manage regex namespace bindings

A ResourcePoolRegexBinding is a cluster-scoped rule that grants one pool to matching existing and future namespaces. Use `pool bindings` to inspect explicit bindings and `pool regex-bindings list --pool POOL` to inspect the rules for the same pool.

### Preview matching namespaces

Preview needs the cluster-scoped RBAC verb `list` on core `namespaces`. It works without the pool, regex CRD, manager, or rule-write permission. It validates 1–256 **bytes** of Go RE2 syntax and matches the entire namespace name, including for alternatives. Quote the expression in the shell.

```bash
HAMI_POOL=training
HAMI_PATTERN='team-a|team-b'
"${HAMI[@]}" pool regex-bindings create "$HAMI_POOL" "$HAMI_PATTERN" --preview
```

If `team-a` and `team-b` exist, the output is sorted. Names such as `prefix-team-a` and `team-b-suffix` are excluded.

```text
Pool: training
Matching namespaces: 2
team-a
team-b
```

The count includes namespaces that already have a grant. Zero matches still allows a rule to affect namespaces created later. A forbidden list fails without a count. Preview makes no binding or pool queries and does not run admission or duplicate checks. A preview request ends here.

### Create, inspect, or edit a rule

To manage rules, the cluster needs the ResourcePoolRegexBinding CRD and a Resource Pool Manager that supports regex grants. Running create without `--preview` submits the rule for admission.

| CLI operation | API requests the CLI makes on ResourcePoolRegexBindings |
| --- | --- |
| `list` or `list --pool POOL` | `list` (the CLI filters by pool locally) |
| `create POOL REGEX` | `list`, `create`, plus `get` to resolve a generated-name collision |
| `update ID REGEX` | `get`, `list`, `update` |
| `delete ID` | `get`, `delete` |

For an authorized grant, review the pattern's full scope, including future matches. Use the preview above when namespace listing is available. Capture the resource name from the successful create. Use a Bash script with error handling as described above.

```bash
HAMI_CREATED=$("${HAMI[@]}" pool regex-bindings create "$HAMI_POOL" "$HAMI_PATTERN")
HAMI_RULE_REF=${HAMI_CREATED% created}
HAMI_RULE_ID=${HAMI_RULE_REF##*/}
"${HAMI[@]}" pool regex-bindings list --pool "$HAMI_POOL" -o json
```

Create prints `resourcepoolregexbinding.resource.hami.io/rprb-… created`. List sorts by ID and accepts `-o table|name|json|yaml`. Keep the ID returned by create. Identical expressions within the same pool are rejected when detected. Different pools can use the same expression. The name allocator probes another ID if a previous edit left its candidate name occupied. A recreated rule may reuse a name with a new UID.

For an authorized edit of the example grant to cover only `team-a`, use the captured ID. For an existing rule, select its ID from the structured list by pool and current expression. Read back the unchanged UID and pool, plus the new expression.

```bash
"${HAMI[@]}" pool regex-bindings update "$HAMI_RULE_ID" 'team-a'
"${HAMI[@]}" pool regex-bindings list --pool "$HAMI_POOL" -o json
```

Update uses the version returned by its internal read and preserves the rule's pool and ID. Moving a grant to another pool takes separate creation and removal operations. Plan their order for the authorized access change.

To remove the selected grant, use the bare ID or its full resource name from create or `list -o name`. Both forms also work for update. A `deleted` message acknowledges the delete. Verify absence if the task requires it.

```bash
"${HAMI[@]}" pool regex-bindings delete "$HAMI_RULE_REF"
"${HAMI[@]}" pool regex-bindings list --pool "$HAMI_POOL" -o json
```

Delete gets the rule and then deletes with both the UID and resourceVersion from that read. An initial NotFound is an error. A subsequent NotFound during delete is accepted, and a changed version conflicts. The command has no caller-supplied UID guard.

### Temporary regex rule workflow

1. Record the baseline rules for the target pool. Use a test expression absent from that pool whose entire grant is within the authorized scope. For example, a test limited to `team-a` can create `team-a` and update it to `(?:team-a)`. Both match only that namespace. Coordinate creation and UID capture so another writer cannot substitute a same-name object.
2. After successful creation, select the exact returned ID in JSON and record its UID, pool and expression. After a duplicate or ambiguous result, re-read and leave the rule alone unless independent evidence establishes ownership.
3. If testing update, use an expression within the authorized scope and verify the new `spec.namespaceRegex` on the same UID in the structured list.
4. Under exclusive coordination, re-read the ID, UID, pool, and expected final expression immediately before deletion. Skip deletion on an ownership mismatch. Verify absence and compare the remaining rules with the baseline.

## Apply declarative manifests

For a caller-owned explicit binding, save this YAML as `binding.yaml`. The example grants the existing namespace `team-a` access to the existing pool `training`.

```yaml
apiVersion: resource.hami.io/v1alpha1
kind: ResourcePoolBinding
metadata:
  name: training
  namespace: team-a
spec:
  poolRef:
    name: training
```

```bash
"${HAMI[@]}" apply -f binding.yaml
"${HAMI[@]}" pool bindings training -o json
```

`apply -f` accepts only `apiVersion: resource.hami.io/v1alpha1` and supports these kinds:

| Kind | Supported by `apply` | Decoding and write behavior |
| --- | --- | --- |
| `ResourcePool` | Yes | Unstructured decode preserves extension fields in the supported API version. Applied with Server-Side Apply (`fieldManager: hami-cli`). |
| `ResourcePoolBinding` | Yes | Strict decode rejects unknown fields. Applied with Server-Side Apply (`fieldManager: hami-cli`). `metadata.name` must equal `spec.poolRef.name`. |
| `GPUDevice` | Existing objects only | Strict decode rejects unknown fields. Requires current `metadata.resourceVersion` and exact `spec.identity`. Patches `spec.poolRef` with an optimistic lock and derives the device-type and pool labels. Omitting `spec.poolRef` unassigns the device. Refresh the object before another edit. |
| `ResourcePoolRegexBinding` | No | Rejected with `unsupported kind`. Use `pool regex-bindings create`, `update`, and `delete`. |

Observe these additional `apply` rules:

- For `ResourcePoolBinding`, the manifest may omit `metadata.labels`. `hami-cli apply` injects `resource.hami.io/pool` from `spec.poolRef.name` automatically. Other clients must supply that label themselves.
- For `ResourcePoolBinding`, if the manifest omits `metadata.namespace`, `--namespace` or the kubeconfig context supplies it. If both the manifest and CLI `--namespace` specify different namespaces, `apply` rejects the request.
- Use `-f -` for stdin. Apply processes multi-document input in order, prints one acknowledgement per successful document, and stops at the first error. Earlier writes persist. Inspect their state before retrying. For a device, extract the individual object from `device list -o json`, set its `apiVersion` and `kind: GPUDevice`, and review the intended pool assignment. A list object is not an accepted manifest.
- Server-Side Apply conflicts indicate another field manager owns the requested field. Preserve that ownership instead of forcing takeover or deleting and recreating the object.

## Delete pools and clean up references

### Temporary cleanup in reverse reference order

Clean up only writes this session owns. Skip steps for resources the task left untouched.

1. Remove session-created explicit bindings and regex grants using their temporary workflows. Confirm the target objects are gone.
2. For an originally unassigned spare device, use `device unassign DEVICE_NAME --expected-uid UID --expected-pool POOL`. Verify the same device has no pool reference or pool label.
3. If the session created the pool, verify that explicit bindings, regex grants (when served), and device assignments are absent. Under exclusive coordination re-check its UID, run `pool delete NAME` once, and verify the final maps. If a reference belongs to another actor, preserve it and report the pool that remains.

By default (`--wait=true`), plain `pool delete NAME` reads the current pool UID and sends it as a delete precondition. Plain `pool delete NAME --wait=false` deletes by name with no UID precondition. Both leave references untouched, so admission may reject the delete while references exist. Both return an error for an absent pool.

### Intentional destructive pool teardown

To intentionally delete a pool along with all of its current references, use `--force`:

```bash
"${HAMI[@]}" pool delete "$HAMI_POOL" --force
```

`pool delete --force` performs these steps:

- It reads the target pool first (requiring a UID), then lists all three reference collections: `ResourcePoolBinding`, `ResourcePoolRegexBinding` (when the CRD is installed), and `GPUDevice`.
- Before each cleanup pass, it re-reads the pool and confirms the original UID. A mismatch stops that pass. Writes from earlier passes remain.
- It deletes matching explicit bindings with a UID precondition and matching regex rules with UID and `resourceVersion` preconditions. It clears matching `GPUDevice.spec.poolRef` and pool labels with optimistic patches while preserving unrelated labels.
- It repeats listing and cleanup for up to five passes until a full snapshot finds zero references, then deletes the same pool UID.
- It accepts a listed reference disappearing concurrently. If the pool has disappeared at the UID re-check, cleanup succeeds only with an empty reference snapshot. A still-present, terminating pool can have its remaining references cleaned.
- After requesting pool deletion, it waits up to `2m` (or `--wait-timeout`) for the original UID to disappear unless you pass `--wait=false`. If the target pool already disappeared concurrently before the final delete call, it prints `deleted` directly.
- It stops without requesting pool deletion on the first non-idempotent cleanup error or if references remain after five passes. Earlier cleanup is not rolled back, so re-read explicit bindings, regex rules, and devices before retrying.
- It preserves unrelated bindings, regex rules, devices, and pools, and it does not evict running Pods or delete workloads.
- Do not use `--force` for temporary test cleanup.

### Wait for pool deletion and handle finalizers

After an accepted delete, the default command prints `deletion requested` and waits up to `2m` for the original UID to disappear before printing `deleted`. To allow five minutes, use `pool delete NAME --wait-timeout 5m`. To return after the request is accepted, choose `pool delete NAME --wait=false`. Issue one delete for the target.

Follow these waiting and timeout rules:

- `--wait` defaults to `true`, and `--wait-timeout` defaults to `2m`. The CLI rejects `--wait-timeout` when `--wait=false`, and `--wait-timeout` must be positive when waiting.
- Waiting finishes when reading the pool returns `NotFound` or returns a pool with a different UID (meaning the original UID disappeared and a new pool reused the name). If your workflow requires the name itself to be absent, verify `pool list -o json` afterward.
- If waiting times out, `pool delete` returns an error without printing `deleted`, but the cluster continues processing the accepted deletion request. A finalizer such as `resource.hami.io/reference-protection` may still retain the pool while references settle. Capture the terminating object with `pool list -o yaml`, check remaining references, and never strip finalizers.

When a later step requires the name to be absent, poll the structured list. A same-name replacement keeps the predicate false.

```bash
HAMI_POOL_ABSENT=false
for HAMI_ATTEMPT in {1..30}; do
  if HAMI_POOLS=$("${HAMI[@]}" pool list -o json) &&
    jq -e --arg pool "$HAMI_POOL" '
      .items | type == "array" and all(.[]; .metadata.name != $pool)
    ' <<<"$HAMI_POOLS" >/dev/null; then
    HAMI_POOL_ABSENT=true
    break
  fi
  sleep 2
done

if [[ "$HAMI_POOL_ABSENT" != true ]]; then
  "${HAMI[@]}" pool list -o yaml || true
  printf 'ResourcePool %s absence was not proven before the deadline\n' "$HAMI_POOL" >&2
  exit 1
fi
```

The loop requires a successful list. A non-empty `metadata.deletionTimestamp` identifies a terminating pool. A live pool can already have a protection finalizer. Never remove it to complete cleanup.

## Verify bounded convergence

`--request-timeout` bounds a single API request, not controller reconciliation. Prefer `--wait-for-current` with `--wait-timeout` for `pool update`, and use default `--wait` with `--wait-timeout` for `pool delete`. For other writes, or after `pool delete --wait=false`, use a bounded attempt loop that succeeds only after a clean structured read satisfies the target predicate.

Verify a temporary explicit binding against its recorded UID:

```bash
HAMI_BINDING_VERIFIED=false
for HAMI_ATTEMPT in {1..30}; do
  if HAMI_BINDINGS=$("${HAMI[@]}" pool bindings "$HAMI_POOL" -o json) &&
    jq -e --arg pool "$HAMI_POOL" --arg namespace "$HAMI_NAMESPACE" --arg uid "$HAMI_BINDING_UID" '
      any(.items[]?;
        .metadata.namespace == $namespace and
        .metadata.name == $pool and
        .spec.poolRef.name == $pool and
        .metadata.uid == $uid and
        (.metadata.generation // 0) > 0 and
        .status.observedGeneration == .metadata.generation and
        (.metadata.generation as $generation |
          any(.status.conditions[]?; .type == "Bound" and .status == "True" and
              (.observedGeneration // $generation) == $generation)))
    ' <<<"$HAMI_BINDINGS" >/dev/null; then
    HAMI_BINDING_VERIFIED=true
    break
  fi
  sleep 2
done

if [[ "$HAMI_BINDING_VERIFIED" != true ]]; then
  printf 'ResourcePoolBinding did not reach the verified state\n' >&2
  exit 1
fi
```

Apply the same successful-read pattern to other mutations:

- Pool creation: same UID, current `status.observedGeneration`, `Accepted=True`, and expected empty or populated readiness state.
- Device assignment: same UID, expected `spec.poolRef.name`, and current `status.observedGeneration`.
- Regex rule creation or update: expected rule ID, recorded UID (unchanged on update), expected `spec.poolRef.name`, and expected `spec.namespaceRegex` in `pool regex-bindings list --pool POOL -o json`.
- Binding, regex rule, or pool deletion: target absent and unrelated baseline mappings unchanged.
- Device unassignment: same device UID remains, `spec.poolRef` and pool label absent, and unrelated baseline mappings unchanged.

An expired polling bound is an unverified result. Stop forward writes, run only ownership-safe cleanup, and report the last successful snapshot.

## Run from a container image

When `hami-cli` runs from a container image, the image entrypoint is already `hami-cli`. Mount only the kubeconfig read-only and pass an explicit context:

```bash
HAMI_IMAGE=IMAGE_REFERENCE
HAMI_KUBECONFIG=/absolute/path/to/kubeconfig
HAMI_CONTEXT=TARGET_CONTEXT

docker run --rm \
  --user "$(id -u):$(id -g)" \
  --mount type=bind,src="$HAMI_KUBECONFIG",dst=/kubeconfig,readonly \
  "$HAMI_IMAGE" \
  --kubeconfig /kubeconfig \
  --context "$HAMI_CONTEXT" \
  --request-timeout 20s \
  pool list -o json
```

If the kubeconfig server address uses `127.0.0.1` or `localhost`, that address points to the container itself. Override only the host endpoint with the container runtime's host gateway and preserve TLS verification. On macOS with Docker Desktop or OrbStack:

```bash
HAMI_API_PORT=6443
HAMI_TLS_SERVER_NAME=NAME_FROM_KUBECONFIG_OR_CERTIFICATE

docker run --rm \
  --user "$(id -u):$(id -g)" \
  --mount type=bind,src="$HAMI_KUBECONFIG",dst=/kubeconfig,readonly \
  "$HAMI_IMAGE" \
  --kubeconfig /kubeconfig \
  --context "$HAMI_CONTEXT" \
  --request-timeout 20s \
  --server "https://host.docker.internal:${HAMI_API_PORT}" \
  --tls-server-name "$HAMI_TLS_SERVER_NAME" \
  pool bindings -A -o json
```

Take `HAMI_TLS_SERVER_NAME` from the selected kubeconfig cluster's `tls-server-name` or from a name in the server certificate's Subject Alternative Names. Never substitute `--insecure-skip-tls-verify`. Linux Docker may also require `--add-host host.docker.internal:host-gateway`.

If the kubeconfig references external CA, client-certificate, or key files, mount each file read-only at a path valid inside the container. Embedded certificate data needs no extra mount. A distroless image cannot run external kubeconfig `exec` authentication plugins. Use a self-contained credential or run the host binary instead.

To install the bundled skill from the container image without mounting kubeconfig, expose only a writable skill destination:

```bash
mkdir -p "$HOME/.agents"

docker run --rm \
  --user "$(id -u):$(id -g)" \
  --env HOME=/hami-home \
  --mount type=bind,src="$HOME/.agents",dst=/hami-home/.agents \
  "$HAMI_IMAGE" \
  skill install --scope user --agent codex --yes
```

Matching the host UID and GID with a writable `HOME` keeps installed files owned by the host user. Do not mount the skill destination read-only.

## Interpret failures

| Symptom | Meaning and action |
| --- | --- |
| `AlreadyExists` from `pool bind` | The create-only command did not converge. Re-read state, and use `apply -f` only if the caller owns the binding. |
| Duplicate namespace regex error | Another rule in the same pool already uses the exact expression. List rules for that pool instead of recreating it. |
| Invalid namespace regex or byte-length error | The expression is empty, exceeds 256 bytes, or is not valid Go RE2 syntax. Fix the pattern locally and retry. |
| `forbidden` on `pool regex-bindings create --preview` | The identity lacks cluster-scoped `list namespaces` RBAC. Report the denial and do not guess which namespaces match. |
| `forbidden` on another read or write | RBAC or admission denied the call. Report the exact verb, resource, and namespace without bypassing policy. |
| `NotFound` or no-match error for a resource type | The cluster lacks that CRD (such as `ResourcePoolRegexBinding` on older clusters) or API discovery failed. `hami-cli` does not install CRDs. |
| `unsupported kind` from `apply -f` | `apply` supports only `ResourcePool`, `ResourcePoolBinding`, and existing `GPUDevice`. Manage `ResourcePoolRegexBinding` through `pool regex-bindings`. |
| `GPUDevice` apply reports a missing or stale version, identity mismatch, or absent device | Read the current inventory and extract the target object before retrying. Apply cannot create a device. |
| Server-Side Apply conflict | Another field manager owns a requested field. Preserve that ownership unless the user authorizes a takeover path. |
| Optimistic-lock conflict on `pool update`, `device assign`, `device unassign`, `pool regex-bindings update` or `delete`, or `pool delete --force` | The object changed concurrently. Re-read current state before deciding whether to retry. |
| `pool create` rejected for same-name non-terminal Pods | Choose another name or wait for the Pods to terminate. Preserve manager-owned Pod membership metadata. |
| Quota enablement rejected during in-flight admissions | Wait for admissions to settle, then re-read the target pool before retrying. |
| `pool delete` times out waiting or returns after `--wait=false` while the pool remains | Deletion was requested and the cluster continues processing it, often behind a reference-protection finalizer. Inspect remaining references and `pool list -o yaml`, wait for absence, and never strip finalizers. |
| Timeout or lost connection after a write | The write outcome is ambiguous. Read by exact name, namespace, or UID before retrying or cleaning up. |

## Report evidence and claims

Include these items in your final report:

- `hami-cli` version and resource-pool API revision when available
- Kube context and namespace scope
- The relevant pre-state, post-state, and any missing reads. For a full lifecycle include the pool, explicit binding, regex rule, and device maps.
- Each write, its persisted result, and current controller status where the kind exposes it
- Cleanup outcome and any object still terminating
- Any operation intentionally skipped, such as device moves or workload checks

CRD state proves API and controller behavior only. Pod admission, scheduling, container visibility, and GPU workload behavior require separate workload evidence.
