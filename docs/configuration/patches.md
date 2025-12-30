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

- Resource names and namespaces containing the literal string `-x-` may not reverse-translate correctly when using `rewriteName` or `rewriteRef` patches. This is because vcluster uses `-x-` as a separator in translated names (format: `{name}-x-{namespace}-x-{vcluster}`).

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
