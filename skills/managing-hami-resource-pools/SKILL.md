---
name: managing-hami-resource-pools
description: Use when inspecting or changing HAMi Enterprise ResourcePool, GPUDevice, or ResourcePoolBinding objects with hami-cli, including namespace access, GPU pool ownership, cleanup, or live-cluster validation.
---

# Managing HAMi Resource Pools

## Outcome

Use `hami-cli` as the Kubernetes client for resource-pool operations. Select the target context explicitly, read existing state before mutation, preserve objects owned by other actors, and make claims only from post-operation evidence.

## Scope

Use this skill for audits, namespace access, pool lifecycle, GPU ownership, cleanup, and live validation without falling back to `kubectl`.

`hami-cli` does not create namespaces, install CRDs/controllers, or prove workload-level GPU behavior. If the requested outcome needs an unsupported operation, report that boundary instead of silently substituting another client. Use `kubectl` only when the user separately authorizes it.

## Operating pattern

1. Run `hami-cli version` and relevant `--help` output. Use the selected `--context` and a bounded `--request-timeout` on every cluster call.
2. Use `pool describe NAME` for a human-readable view of one pool, its CPU and memory quota ledger, and its associated namespaces and devices. Capture `pool list -o json`, `device list -o json`, and `pool bindings -A -o json` before mutation. Structured output is the evidence source; tables are for humans.
3. Choose the ownership mode:
   - Audit: read only.
   - User-owned desired state: use `apply` so an identical manifest is rerunnable.
   - Temporary test relation: require absence, use `pool bind`, record its UID, exclude concurrent writers, and re-check the UID immediately before `pool unbind`.
   - GPU move: proceed only with explicit authorization and current device/allocation evidence. For temporary cleanup, record the device UID and use `device unassign` with both `--expected-uid` and `--expected-pool`.
4. Mutate the smallest object set. Prefer `pool bind`/`pool unbind`, `device assign`/`device unassign`, and `pool create`/`pool update`/`pool delete` over hand-edited manifests. Use `pool delete --force` only for an intentional pool teardown that should also remove every binding and device assignment referencing that pool.
5. Verify controller-observed state. For temporary changes, clean up in reverse reference order and compare final maps with the baseline; for persistent changes, prove the intended delta and unchanged unrelated state.

Read [references/operations.md](references/operations.md) before executing mutations or diagnosing failures.

## Safety invariants

- Never assume a namespace exists or is disposable; `hami-cli` cannot establish that fact.
- `pool bindings` requires either one pool name or `-A`/`--all-pools`; do not combine them.
- `pool bindings` always spans namespaces and rejects an explicit `--namespace`; filter its structured output when only one namespace is relevant.
- Supported output formats are `table`, `name`, `json`, and `yaml`; there is no `wide` output.
- Repeating `pool bind` returns `AlreadyExists`. Use `apply` only when the caller owns the desired object; do not adopt an unrelated binding.
- `pool unbind` and plain `pool delete` are name-based deletes without UID preconditions. `pool delete --force` reads the target first and uses UID preconditions for the pool and each binding plus optimistic patches for devices, but it is still a destructive teardown rather than temporary-cleanup ownership proof.
- `pool unbind` affects future Pod authorization. It does not evict existing Pods or reclaim their GPUs.
- `pool delete --force` deletes matching namespace bindings and clears matching GPU assignments, but it does not evict existing Pods. It re-lists both reference types after each cleanup pass, accepts a listed reference disappearing concurrently, and accepts the pool disappearing only after an empty reference snapshot. It fails if references remain after five passes. Other cleanup failures stop pool deletion; earlier successful cleanup is not rolled back, so re-read all references before retrying.
- An empty allocation list does not exclude concurrent scheduling. Coordinate a scheduling freeze or obtain owner confirmation before moving a live GPU.
- `device unassign` without ownership flags clears the assignment observed by its internal read. Temporary cleanup must pass both `--expected-uid` and `--expected-pool` so an earlier replacement or reassignment fails closed; a later change conflicts with the command's optimistic patch.
- `pool delete` is asynchronous because reference-protection finalizers may delay removal. Wait for absence; never strip the finalizer to make a test pass.
- `pool create` and `pool update` write only `spec.quota`; the manager owns `status.quota`. CPU and memory values are Kubernetes quantities, zero is a hard zero ceiling, and the literal `unlimited` removes only the selected dimension. Quota accounting uses Pod limits; every controlled dimension requires a positive limit, while requests are optional and independent. Updates use an optimistic lock, so a conflict requires a fresh read rather than an overwrite. Treat used and reserved values as current only when `pool describe` reports a current quota ledger.
- `pool create` is rejected while a non-terminal Pod still refers to an earlier pool with the same name. Use a new name or wait for those Pods to terminate; do not delete their manager-owned membership metadata.
- Enabling quota can be rejected while Pod admissions are in flight. Re-read the pool and retry only after those admissions settle; do not bypass the transition barrier.
- On conflicts, forbidden responses, unknown resource types, timeouts, or ambiguous writes, re-read state and fail closed. Do not bypass RBAC or TLS validation.
