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
2. Capture `pool list -o json`, `device list -o json`, and `binding list -A -o json` before mutation. Structured output is the evidence source; tables are for humans.
3. Choose the ownership mode:
   - Audit: read only.
   - User-owned desired state: use `apply` so an identical manifest is rerunnable.
   - Temporary test relation: require absence, use `bind`, record its UID, exclude concurrent writers, and re-check the UID immediately before `unbind`.
   - GPU move: proceed only with explicit authorization and current device/allocation evidence. For temporary cleanup, record the device UID and use `device unassign` with both `--expected-uid` and `--expected-pool`.
4. Mutate the smallest object set. Prefer `bind`/`unbind`, `device assign`/`device unassign`, and `pool create`/`pool delete` over hand-edited manifests.
5. Verify controller-observed state. For temporary changes, clean up in reverse reference order and compare final maps with the baseline; for persistent changes, prove the intended delta and unchanged unrelated state.

Read [references/operations.md](references/operations.md) before executing mutations or diagnosing failures.

## Safety invariants

- Never assume a namespace exists or is disposable; `hami-cli` cannot establish that fact.
- Do not combine `binding list -A` with `--namespace`.
- Supported output formats are `table`, `name`, `json`, and `yaml`; there is no `wide` output.
- Repeating `bind` returns `AlreadyExists`. Use `apply` only when the caller owns the desired object; do not adopt an unrelated binding.
- `unbind` and `pool delete` are name-based deletes without UID preconditions. If concurrent replacement cannot be excluded, automated temporary cleanup is not ownership-safe.
- `unbind` affects future Pod authorization. It does not evict existing Pods or reclaim their GPUs.
- An empty allocation list does not exclude concurrent scheduling. Coordinate a scheduling freeze or obtain owner confirmation before moving a live GPU.
- `device unassign` without ownership flags clears the assignment observed by its internal read. Temporary cleanup must pass both `--expected-uid` and `--expected-pool` so an earlier replacement or reassignment fails closed; a later change conflicts with the command's optimistic patch.
- `pool delete` is asynchronous because reference-protection finalizers may delay removal. Wait for absence; never strip the finalizer to make a test pass.
- On conflicts, forbidden responses, unknown resource types, timeouts, or ambiguous writes, re-read state and fail closed. Do not bypass RBAC or TLS validation.
