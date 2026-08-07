---
type: Runbook
title: oasgen-provider — testing
description: Running the two-module test suite the way CI does (build tags, -p 1, -race).
resource: github.com/krateo-platformops/oasgen-provider
tags: [kog, testing, ci]
timestamp: 2026-08-07T00:00:00Z
---

# Testing

This is a monorepo with two Go modules; run the suite per module
(`go/oasgen-provider/`, `go/rest-dynamic-controller/`). CI
(`.github/workflows/test.yaml`) runs, for each module:

```sh
go test -race -tags=unit,integration -p 1 -timeout 20m ./...
```

Two things there are load-bearing:

- **`-tags=unit,integration` is deliberate**: the envtest/kind suite in
  `internal/controllers` is only compiled under the `integration` tag, and dropping the
  tag silently *skips* those tests rather than failing.
- **`-p 1`** keeps packages that stand up clusters from racing each other.

For a quick unit-only iteration inside a module:

```sh
go test -tags=unit -cover ./...
```

`go/oasgen-provider/scripts/test.sh` is the local coverage wrapper
(`go test ./... -coverprofile` + `go tool cover -func`); note it runs untagged, so it
exercises only the untagged subset.
