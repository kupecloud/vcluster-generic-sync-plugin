---
title: Examples
description: Practical vcluster plugin configurations from minimal to full featured.
---

These examples are designed for Astro Starlight and can be pulled into a shared docs site. Start with the base values, then layer in the `config` blocks from each example.

## Available examples

- [Minimal (no syncers)](minimal.md) - Plugin installed and running, but no resources are synced.
- [Base plugin values](base-config.md) - Baseline Helm values and RBAC scaffolding.
- [Gateway API](gateway-api.md) - Mirror Gateways from host to virtual and sync HTTPRoutes back to host.
- [Widget label selection](label-selector.md) - Sync Widgets based on labels.
- [Widget namespace filtering](namespace-filtering.md) - Use global and per-resource namespace filters.
- [Widget patches](patches.md) - Use patch types for name and reference translation.
- [Full example](full-config.md) - Combine selectors, global filters, and patches.

## Common prerequisites

- Your plugin image is available to the cluster.
- RBAC rules cover the kinds you want to sync.
- CRDs exist on the host cluster for any `toHost` resources.

Use `test/testdata/widget-crd.yaml` if you want a simple CRD to experiment with.
