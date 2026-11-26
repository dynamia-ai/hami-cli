---
name: managing-hami-resource-pools
description: Use hami-cli to inspect or change HAMi Enterprise resource pools, quotas, scheduling defaults, NVIDIA or Ascend device assignments, explicit namespace bindings, and regex namespace grants. Covers RE2 preview, declarative apply, teardown, and live-cluster validation.
---

# Managing HAMi resource pools

Use `hami-cli version` and the relevant subcommand's `--help` to identify the available commands. Pass an explicit `--context` and a bounded `--request-timeout` on cluster calls. The CLI operates on existing Kubernetes infrastructure. Namespace provisioning, CRD or manager installation, and Pod-level validation require separate tooling and authorization. Use `kubectl` only within the user's authorization.

| Task | Commands and procedure |
| --- | --- |
| Audit pools, bindings, or devices | [Capture a baseline](references/operations.md#set-target-context-and-capture-a-baseline) with the relevant list commands. `pool describe NAME` summarizes associations. |
| Create a pool or change quotas and scheduling defaults | [Use pool create, update, and describe](references/operations.md#manage-pools-quotas-and-scheduling-policies). Check the generation before using the quota ledger. |
| Assign or restore a device | [Inspect device inventory and ownership](references/operations.md#manage-pool-and-device-ownership). Use the object name or the exact node, device type, and device ID. |
| Grant or revoke access for one namespace | [Manage explicit bindings](references/operations.md#manage-explicit-namespace-bindings) with `pool bind` and `unbind`. Check overlapping regex grants when reporting effective access. |
| Preview or change a namespace regex | [Use pool regex-bindings](references/operations.md#manage-regex-namespace-bindings). Preview reads existing Namespace names. Creating or updating a rule grants access to matching existing and future namespaces. |
| Apply caller-owned desired state | [Apply manifests](references/operations.md#apply-declarative-manifests) for ResourcePool, ResourcePoolBinding, or an existing GPUDevice. Use dedicated commands for regex rules. |
| Clean up a test or remove a pool and its references | [Choose the deletion procedure](references/operations.md#delete-pools-and-clean-up-references). Reserve `pool delete --force` for an authorized teardown of all matching references. |
| Run the CLI or install the bundled skill from an image | [Run from a container image](references/operations.md#run-from-a-container-image). |

## Before and after a write

- Read the affected objects as JSON. For a full lifecycle, save the pool, explicit binding, regex rule (when served), and device maps for comparison. A preview-only task needs just the namespace read. Keep an audit read-only.
- For a persistent change, verify the requested delta and leave it in place. When checking namespace access, inspect both explicit bindings and matching regex rules for the pool. Unbinding a namespace does not evict running Pods.
- For a temporary object, establish absence and protect its identity from creation through UID capture. Record the UID after a successful create. An existing object, duplicate rule, or ambiguous create result gives no ownership evidence. Before cleanup, verify that the object still belongs to the test and exclude replacement between that check and the CLI's own delete. `pool unbind` deletes by name. Neither pool nor rule deletion accepts a caller-supplied UID.
- Before an authorized device move, check allocations and workloads with the owner or coordinate a scheduling freeze. For a temporary test, use an originally unassigned spare device. Record its UID before assignment and pass both `--expected-uid` and `--expected-pool` when unassigning it.
- [Verify convergence](references/operations.md#verify-bounded-convergence) before claiming success. Separate a stored spec, current controller status, and observed workload behavior. On a conflict, denial, or ambiguous result, re-read what the identity can access. Report missing evidence and perform only cleanup whose ownership you can establish.
