---
type: Runbook
title: oasgen-provider — release
description: How a release ships — one plain-semver tag drives the test gate, the multi-platform image, and both OCI charts.
resource: oci://ghcr.io/krateo-platformops/charts/oasgen-provider
tags: [kog, release, ci]
timestamp: 2026-08-07T00:00:00Z
---

# Release

One version line: pushing a plain-semver tag `X.Y.Z` (**no `v` prefix** — the workflows
trigger on `[0-9]+.[0-9]+.[0-9]+`) releases everything.

## What the tag runs

1. **`test.yaml`** (release gate, also run on PRs and pushes to main): the full suite for
   both Go modules — `go test -race -tags=unit,integration -p 1 …` per module. A tag
   cannot publish an image that fails its own tests.
2. **`release-tag.yaml`** → the shared `component-image-build` reusable builds ONE
   multi-platform image (linux/amd64 + linux/arm64) from `go/oasgen-provider/`, pushed as
   `ghcr.io/krateo-platformops/oasgen-provider:X.Y.Z`. Only the provider publishes an
   image from this repo; the rest-dynamic-controller image ships from its own repo.
3. **`release-oci.yaml`** (the canonical org-wide package workflow) discovers every
   first-class chart and pushes both to
   `oci://ghcr.io/krateo-platformops/charts/`:
   - `oasgen-provider` `X.Y.Z`
   - `oasgen-provider-crds` `X.Y.Z` (same tag — one repo tag publishes app + CRD charts
     at one version, so the installer pins both at one version)

   `CHART_VERSION` placeholders become the tag; `APP_VERSION` becomes the latest semver
   tag of the app repo (normally the same tag). `workflow_dispatch` can override either.

## What the tag does NOT roll

`rdc.image.tag` in `helm/oasgen-provider/values.yaml` is a **hand-maintained pin** — the
`APP_VERSION` substitution touches `Chart.yaml` only. When a new rest-dynamic-controller
version should ship with the chart, bump the pin (and extend its rationale comment)
**before** tagging.

## PR checks

`release-pullrequest.yaml`: the image build (push=false), the shared test suite, the
CRD-drift guard (`go generate ./apis/...` must leave `crds/` clean — regenerate and
commit when changing `apis/`), and the docs-standard lint (`lint-docs`).

## After the release

- Verify both charts on GHCR:
  `helm show chart oci://ghcr.io/krateo-platformops/charts/oasgen-provider --version X.Y.Z`.
- Bump the installer pins (`oasgen-provider` and `oasgen-provider-crd` entries in the
  installer's `component-pins.yaml`) to pick the release up in platform deploys.
- Release notes live in GitHub Releases; notable changes are curated into
  [log.md](./log.md).
