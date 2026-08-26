# hami-cli Resource-Pool Operations

Use these command patterns after selecting a kube context and confirming that the requested mutations are authorized.

## Command prefix and baseline

Keep the target explicit on every cluster call:

```bash
HAMI_CONTEXT=aliyun-hangzhou-ecs
HAMI=(hami-cli --context "$HAMI_CONTEXT" --request-timeout 20s)

"${HAMI[@]}" version
"${HAMI[@]}" pool list -o json
"${HAMI[@]}" device list -o json
"${HAMI[@]}" pool bindings -A -o json
```

Record at least these stable baseline mappings:

- pool names;
- binding namespace, name, and `spec.poolRef.name`;
- device name and `spec.poolRef.name`;
- target object UID when a later delete decision depends on ownership.

Ignore `resourceVersion`, timestamps, and controller status when comparing whether unrelated desired state changed.

## Run from a container image

When the CLI is delivered as a container image, remember that the image entrypoint is already `hami-cli`. Mount the kubeconfig read-only, keep the target context explicit, and do not expose unrelated host directories:

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

If the selected cluster server uses `127.0.0.1` or `localhost`, it points to the container itself. Replace only the network endpoint with the host gateway that the container runtime provides. On macOS with Docker Desktop or OrbStack, the generic form is:

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

Take `HAMI_TLS_SERVER_NAME` from the selected kubeconfig cluster's `tls-server-name`, or from the original name covered by the certificate's Subject Alternative Names. Never replace correct server identity with `--insecure-skip-tls-verify`. Linux Docker may additionally require `--add-host host.docker.internal:host-gateway`.

If kubeconfig refers to external CA, client-certificate, or key files, mount each required file read-only and preserve a path that is valid inside the container. Embedded certificate data needs no extra mount. A distroless image cannot run external kubeconfig `exec` authentication plugins; use a self-contained credential supported by the environment or run the binary on the host.

The image also bundles this skill. Install it without mounting kubeconfig, and expose only a writable skill destination:

```bash
mkdir -p "$HOME/.agents"

docker run --rm \
  --user "$(id -u):$(id -g)" \
  --env HOME=/hami-home \
  --mount type=bind,src="$HOME/.agents",dst=/hami-home/.agents \
  "$HAMI_IMAGE" \
  skill install --scope user --agent codex --yes
```

The writable `HOME` and matching UID/GID keep installed files owned by the host user. Do not mark the skill destination mount read-only.

## Audit relationships

Start a human investigation from the pool and drill down to its associated namespaces and devices:

```bash
HAMI_POOL=training
"${HAMI[@]}" pool describe "$HAMI_POOL"
```

`pool describe` matches associations by `spec.poolRef.name` across all namespaces. It is a human-readable convenience view; retain structured `pool bindings` output when the result is audit evidence or input to automation.

List every namespace-to-pool relationship:

```bash
"${HAMI[@]}" pool bindings -A
"${HAMI[@]}" pool bindings --all-pools -o json
```

List every namespace relationship for the target pool:

```bash
"${HAMI[@]}" pool bindings "$HAMI_POOL"
"${HAMI[@]}" pool bindings "$HAMI_POOL" -o json
```

`pool bindings` always spans namespaces and rejects an explicit `--namespace`.
When only one namespace is relevant, filter the structured result without
changing the cluster query scope:

```bash
HAMI_NAMESPACE=team-a
"${HAMI[@]}" pool bindings "$HAMI_POOL" -o json |
  jq --arg namespace "$HAMI_NAMESPACE" '{items: [.items[] | select(.metadata.namespace == $namespace)]}'
```

A relationship is controller-confirmed when the object has the expected namespace/name/pool, `status.observedGeneration` matches `metadata.generation`, and its current `Bound` condition is `True`.

## Manage a declared relationship

Use `apply` when the caller owns the desired object and rerunning the same declaration should converge:

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

The binding name must equal `spec.poolRef.name`. If the manifest omits its namespace, kubeconfig or `--namespace` supplies it. An explicit manifest namespace that conflicts with an explicit CLI namespace is rejected. Server-Side Apply conflicts are ownership evidence; do not delete/recreate or force ownership.

The manifest may omit `metadata.labels`: `hami-cli apply` injects the required `resource.hami.io/pool` label from `spec.poolRef.name` before sending the object. This behavior is specific to `hami-cli`; a manifest sent by another client must satisfy the server's label invariant itself.

Revoke future access with:

```bash
"${HAMI[@]}" pool unbind training --namespace team-a
"${HAMI[@]}" pool bindings training -o json
```

Success requires the target relationship to be absent after the delete. Existing Pods may continue running.

## Manage a temporary relationship

For a create-test-delete cycle, do not adopt an existing object:

1. Read `pool bindings POOL -o json` and require the target namespace relationship to be absent.
2. Run `pool bind POOL --namespace NAMESPACE` once.
3. Read the object, record its UID, and wait for the current generation to become `Bound=True`.
4. Exclude concurrent binding writers for the cleanup window; immediately before cleanup, re-read and require the same UID.
5. Run `pool unbind POOL --namespace NAMESPACE`, prove absence, and compare the pool relationship map with the baseline.

If `pool bind` returns `AlreadyExists` or times out ambiguously, re-read state and do not delete the object unless ownership is independently proven.

The UID re-read detects earlier replacement but is not an atomic delete precondition: `pool unbind` deletes by namespace/name. If another actor can replace the object between the read and delete, do not automate cleanup; current `hami-cli` cannot make that deletion ownership-safe.

## Manage pool and device ownership

Create a pool with optional aggregate CPU and memory hard quotas, then inspect
the declared state and device candidates:

```bash
HAMI_POOL=training
"${HAMI[@]}" pool create "$HAMI_POOL" \
  --cpu-quota 128 \
  --memory-quota 512Gi
"${HAMI[@]}" pool describe "$HAMI_POOL"
"${HAMI[@]}" pool list -o json
"${HAMI[@]}" device list -o json
```

Both quota flags are optional and accept Kubernetes quantities. An omitted
dimension is unlimited. Zero is a valid hard ceiling that rejects new non-zero
usage; it does not mean unlimited. For every controlled dimension, admitted
workload containers must declare equal, non-zero requests and limits.

Set or change the pool defaults applied to workloads that do not already carry
the corresponding scheduler annotations:

```bash
"${HAMI[@]}" pool update "$HAMI_POOL" \
  --node-scheduler-policy spread \
  --gpu-scheduler-policy topology-aware,mutex \
  --cpu-quota 192 \
  --memory-quota 768Gi
"${HAMI[@]}" pool describe "$HAMI_POOL"
```

Any scheduler-policy or quota flag may be used on its own. The update preserves
every omitted field and rejects a concurrent ResourcePool change instead of
overwriting it. Remove one quota dimension explicitly without changing the
other:

```bash
"${HAMI[@]}" pool update "$HAMI_POOL" --cpu-quota unlimited
```

`pool describe` reports the configured ceilings and the manager-owned used and
reserved ledger when status is available. The CLI never writes `status.quota`.

Before `pool create`, require the name to be absent. For a temporary lifecycle, exclude other writers for that pool name from the absence check through confirmed deletion. After a successful create, read and record the pool UID. An `AlreadyExists` or ambiguous create result does not establish ownership; do not later delete that pool without independent proof.

The pool UID re-read is not an atomic delete precondition for plain `pool delete`, which deletes by name. If another actor can replace the pool before the UID is recorded or between the final read and delete, do not automate temporary cleanup. The `--force` mode described below adds UID guards, but it is intended for destructive pool teardown rather than ownership-safe reversal of a temporary test.

A newly created empty pool is correctly observed when its current generation has `Accepted=True`, zero discovered/healthy devices, and `Ready=False` with reason `NoHealthyDevices`. Do not wait for `Ready=True` until a healthy device has actually been assigned and discovered.

Assign by object name, or by the NVIDIA node/device identity pair:

```bash
"${HAMI[@]}" device assign GPUDEVICE_NAME --pool "$HAMI_POOL"
"${HAMI[@]}" device assign --node GPU_NODE --device-id GPU_UUID --pool "$HAMI_POOL"
```

Before assignment, record the device UID, pool reference, discovery/health status, allocations, and resource version. The dedicated command uses an optimistic update; a conflict means the inventory changed and must be read again. For a temporary lifecycle, select only an originally unassigned spare device and require exclusive scheduling coordination. Do not move the only healthy, allocated, or previously assigned GPU merely to smoke-test the command.

If the lifecycle includes a temporary binding, create it through the temporary relationship workflow and record its UID. Clean up only writes whose ownership and original state were recorded:

```bash
"${HAMI[@]}" pool unbind "$HAMI_POOL" --namespace team-a
"${HAMI[@]}" device unassign GPUDEVICE_NAME \
  --expected-uid "$HAMI_DEVICE_UID" \
  --expected-pool "$HAMI_POOL"
"${HAMI[@]}" pool delete "$HAMI_POOL"
```

Run those commands only after their guards succeed:

- Unbind only a session-created binding after the same-UID re-read and under exclusive coordination.
- Unassign only a session-modified device whose original pool was empty. Set `HAMI_DEVICE_UID` from the pre-assignment structured read and pass both ownership flags shown above. The command rejects a different current UID or pool, and its optimistic patch rejects changes after the internal read.
- Delete only a session-created pool with the same recorded UID after all references are absent.

`--expected-uid` and `--expected-pool` must be used together. Omitting both retains the unguarded manual behavior and is not ownership-safe for temporary cleanup in a shared cluster.

For an intentional permanent move, verify the requested final assignment instead of running temporary cleanup. If a planned workflow must restore a non-empty original pool, use `device assign --pool ORIGINAL_POOL` only after re-reading the same device UID and obtaining explicit authorization; never replace it with `unassign`.

For an intentional teardown of a pool and all of its current references, use the explicit force mode:

```bash
"${HAMI[@]}" pool delete "$HAMI_POOL" --force
```

Force mode reads the target pool and both reference collections, revalidates that the pool still has the same UID, deletes every `ResourcePoolBinding` whose `spec.poolRef.name` matches it, and clears the matching `GPUDevice.spec.poolRef` values and pool labels with optimistic patches. It repeats that snapshot cleanup for up to five passes until a complete pass finds no references, then requests deletion of the same pool UID. A listed reference disappearing concurrently is treated as completed cleanup. The target pool disappearing is accepted only after an empty reference snapshot; a contradictory non-empty snapshot fails closed. Unrelated bindings, devices, and pools are preserved. Plain `pool delete` retains the admission-protected behavior and does not clean references.

This mode revokes future namespace access and GPU ownership in one command, but it does not evict existing Pods or delete workloads. It stops on the first non-idempotent cleanup failure or when references still appear after five passes, and it does not request pool deletion in either case. Cleanup operations that already succeeded are not rolled back. After any error, re-read the complete binding and device maps before deciding whether to retry.

After `pool delete`, use bounded condition polling on `pool list -o name` until the pool is absent. A finalizer may intentionally retain the object while references settle. If the bound expires, capture the terminating object from `pool list -o yaml` and report the remaining finalizer/reference state; do not patch finalizers away.

One bounded absence check is:

```bash
HAMI_POOL_ABSENT=false
for HAMI_ATTEMPT in {1..30}; do
  if ! HAMI_POOL_NAMES=$("${HAMI[@]}" pool list -o name); then
    sleep 2
    continue
  fi
  if grep -Fqx "resourcepool.resource.hami.io/$HAMI_POOL" <<<"$HAMI_POOL_NAMES"; then
    sleep 2
    continue
  fi
  HAMI_POOL_ABSENT=true
  break
done

if [[ "$HAMI_POOL_ABSENT" != true ]]; then
  "${HAMI[@]}" pool list -o yaml || true
  printf 'ResourcePool %s absence was not proven before the deadline\n' "$HAMI_POOL" >&2
  exit 1
fi
```

The loop treats a list failure as unknown state rather than absence. Claim that the pool is terminating only when a successful final read shows its deletion timestamp or finalizers.

## Verify bounded convergence

`--request-timeout` bounds one API call, not controller reconciliation. For every write, use a separate overall deadline or bounded attempt count. Each attempt must first complete a successful structured read and then evaluate the expected predicate; read failures are unknown state, not success.

For example, verify a temporary binding with its recorded UID:

```bash
HAMI_BINDING_VERIFIED=false
for HAMI_ATTEMPT in {1..30}; do
  if HAMI_BINDINGS=$("${HAMI[@]}" pool bindings "$HAMI_POOL" -o json) &&
    jq -e --arg pool "$HAMI_POOL" --arg uid "$HAMI_BINDING_UID" '
      any(.items[];
        .metadata.name == $pool and
        .spec.poolRef.name == $pool and
        .metadata.uid == $uid and
        .status.observedGeneration == .metadata.generation and
        any(.status.conditions[]?; .type == "Bound" and .status == "True"))
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

Use the same successful-read pattern for:

- pool creation: same UID, current `observedGeneration`, `Accepted=True`, and the expected empty or populated readiness state;
- device assignment: same UID, expected `spec.poolRef`, and current `status.observedGeneration`;
- binding/device/pool cleanup: target absence plus unchanged unrelated baseline mappings.

An expired bound is an unverified result. Stop forward mutation, run only ownership-safe cleanup, and report the last successful snapshot.

## Interpret failures

| Symptom | Meaning and action |
| --- | --- |
| `AlreadyExists` from `pool bind` | The create-only command did not converge. Re-read; use `apply` only for an object the caller owns. |
| `NotFound` for resource types | The target cluster lacks the resource-pool CRDs or API discovery is failing. The CLI does not install them. |
| `forbidden` | RBAC or admission denied the operation. Report the exact verb/resource/namespace; do not bypass policy. |
| Server-Side Apply conflict | Another manager owns a requested field. Preserve that ownership unless the user explicitly authorizes a takeover path. |
| GPU update conflict | Device inventory or ownership changed concurrently. Re-read before deciding whether to retry. |
| Delete acknowledged but object remains | Deletion is pending finalizer processing. Verify references and wait for absence. |
| Timeout or lost connection after a write | The result is ambiguous. Read by exact name/namespace/UID before retrying or cleaning up. |

## Evidence and claims

Report:

- CLI version and resource-pool API revision when available;
- kube context and namespace scope;
- exact pre-state and post-state relationship/device maps;
- each mutation and its controller-observed result;
- cleanup outcome and any object still terminating;
- whether GPU assignment or workload execution was intentionally skipped.

CRD state proves API and controller behavior only. Admission, scheduling, container visibility, and GPU workload behavior require separate workload evidence.
