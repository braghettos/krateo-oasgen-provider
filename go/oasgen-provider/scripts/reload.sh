#!/bin/bash
#
# Local dev loop: build the provider into the kind cluster and (re)install it VIA THE CHART.
#
# The chart is the only place the RDC templates live. This repo used to carry a second copy under
# manifests/rdc/ purely so this script could apply it. That copy never shipped — the Dockerfile copies
# only go.mod/go.sum/main.go/apis/ and internal/ — so it was a reference that drifted from the copy
# that does ship, and three separate bugs came out of the divergence. krateo-core-provider spawns its
# CDCs by the identical mechanism and has never carried such a copy.
#
# Consequence worth knowing: the RDC templates can no longer be edited from this repo. Change them in
# krateo-oasgen-provider-chart (chart/assets/rdc/) and point CHART at that working tree — see
# docs/development/workflow.md, which covers the one wrinkle (a chart working tree carries CI
# placeholders that helm refuses to install).

set -euo pipefail

NAMESPACE="${NAMESPACE:-demo-system}"
RELEASE="${RELEASE:-oasgen-provider}"
CHART="${CHART:-oci://ghcr.io/braghettos/krateo/krateo-oasgen-provider}"

PROJECT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." &>/dev/null && pwd)
cd "$PROJECT_DIR"

./scripts/build.sh

# Locally generated CRDs, not the chart's: when you are changing apis/, this repo's crds/ is the copy
# under development. `make generate` regenerates them.
kubectl apply -f crds/

# image.tag=latest matches what build.sh (ko with KO_DOCKER_REPO=kind.local) produces, and
# pullPolicy=Never stops the kubelet trying to pull that tag from a registry it does not exist in.
helm upgrade --install "$RELEASE" "$CHART" \
    --create-namespace --namespace "$NAMESPACE" \
    --set image.repository=kind.local/oasgen-provider \
    --set image.tag=latest \
    --set image.pullPolicy=Never \
    --wait --timeout 5m

kubectl -n "$NAMESPACE" get deploy -l "app.kubernetes.io/instance=$RELEASE"
