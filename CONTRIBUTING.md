# Contributing

Thanks for contributing to `vcluster-generic-sync-plugin`.

## Development Setup

1. Install Go (version from `go.mod`).
2. Install `pre-commit`.
3. Install repo hooks:

```bash
pre-commit install
pre-commit install --hook-type commit-msg
```

## Before Opening a PR

Run the local quality checks:

```bash
make lint
make test
```

For end-to-end validation:

```bash
make e2e
```

## Commit Conventions

This repo uses Conventional Commits for release automation.

Examples:
- `feat: add status subresource detection caching`
- `fix: handle missing namespace on rewriteRef patch`
- `docs: clarify mirror mode behavior`

## Pull Request Guidelines

- Keep PRs focused and scoped.
- Add or update tests for behavior changes.
- Update docs when config/behavior changes.
- Ensure CI is green before requesting review.

## Reporting Bugs

Please open a GitHub issue with:
- Steps to reproduce
- Expected vs actual behavior
- Config snippet (sanitized)
- Relevant logs and versions
