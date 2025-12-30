---
title: Testing
description: How to run unit, integration, and end-to-end tests.
---

The repo includes unit tests, integration tests, and an end-to-end suite that provisions a Kind cluster and installs vcluster.

## Quick commands

```bash
# Unit + integration
make test
# or

go test ./...
```

```bash
# E2E (creates kind + installs vcluster by default)
make e2e
# or

go test -tags=e2e ./test/e2e -v
```

## DevSpace workflow

For fast inner-loop development, use DevSpace to sync the plugin binary into the vcluster pod:

```bash
make dev
```

To rebuild the binary (DevSpace will sync and restart):

```bash
make dev-build
```

To clean up the DevSpace environment:

```bash
make dev-purge
```

## E2E details

See [E2E Testing](e2e.md) for environment variables, cluster lifecycle controls, and test coverage.
