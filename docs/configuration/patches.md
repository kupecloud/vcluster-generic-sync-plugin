---
title: Patches
description: Translate names, namespaces, and selectors in synced objects.
---

Patches are used to translate references between virtual and host naming conventions. They are **not** general-purpose mutations; they only rewrite names/namespaces/label selectors.

## Patch types

| Type | Description | Example path |
| --- | --- | --- |
| `rewriteName` | Rewrite a name field | `spec.secretRef.name` |
| `rewriteNamespace` | Rewrite a namespace field | `spec.secretRef.namespace` |
| `rewriteRef` | Rewrite `{name, namespace}` reference objects | `spec.backendRefs[*]` |
| `rewriteHostRef` | Rewrite namespace only (host-native reference) | `spec.parentRefs[*]` |
| `rewriteLabelSelector` | Rewrite selector label keys | `spec.selector` |
| `none` | Copy value as-is (no translation) | `spec.issuerRef.name` |

## Path syntax

Paths are dot-separated and support arrays:

- `spec.backendRefs[*].name`
- `spec.namespaces[0]`
- `spec.rules[*].backendRefs[*]`

`[*]` applies a patch to all array elements.

## What can be patched

- **Reference fields** inside `spec` (e.g., `secretRef`, `backendRefs`, `parentRefs`).
- **Label selectors** (`matchLabels` and `matchExpressions.key`).

Patches do **not** allow arbitrary spec mutation. If you need JSONPatch/merge semantics, the patch engine would need new patch types.

## Limitations

### Name Translation with `-x-` Separator

vCluster uses `-x-` as a separator in translated names, following the format: `{name}-x-{namespace}-x-{vcluster}`.

**This creates an ambiguity when BOTH the resource name AND namespace contain `-x-`:**

For example, if you have:
- Name: `my-x-service`
- Namespace: `team-x-prod`
- vCluster: `dev`

The translated name would be: `my-x-service-x-team-x-prod-x-dev`

When reverse-translating, the plugin cannot reliably determine where the original name ends and namespace begins because there are multiple `-x-` sequences.

**Supported scenarios (work correctly):**
- Name contains `-x-`, namespace does not: `foo-x-bar` in `default` → works
- Namespace contains `-x-`, name does not: `myservice` in `team-x-prod` → works
- Neither contains `-x-`: `myservice` in `default` → works

**Unsupported scenario:**
- BOTH name and namespace contain `-x-`: May produce incorrect results

**Workaround:** The plugin stores original references in annotations when applying patches. This annotation-based lookup is used first and handles the ambiguous case correctly. However, if the annotation is lost or not present, the reverse translation falls back to heuristic parsing.

## Example

```yaml
syncResources:
  - apiVersion: gateway.networking.k8s.io/v1
    kind: HTTPRoute
    direction: toHost
    statusSync: true
    patches:
      - path: spec.parentRefs[*]
        type: rewriteHostRef
      - path: spec.rules[*].backendRefs[*]
        type: rewriteRef
      - path: spec.rules[*].matches[*].headers[*].name
        type: none
```
