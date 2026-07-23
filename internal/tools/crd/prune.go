package crd

import (
	"context"
	"errors"

	"github.com/krateoplatformops/oasgen-provider/internal/tools/crd/generation"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// errNoPrune is a sentinel used to skip the Update when a concurrent change removed everything prunable
// between the initial check and the retry. It is not a Kubernetes conflict, so it is not retried.
var errNoPrune = errors.New("no versions to prune")

// prunableVersions returns the served versions of crd that are SAFE to drop without migrating any instance:
// not the current version, not the vacuum storage version, and NOT in status.storedVersions (so no stored
// object was ever written as them). Because the vacuum becomes the sole storage version on the first append,
// every version appended after that is never a storage version and is therefore prunable here — bounding the
// accumulation from repeated version bumps. The one version that WAS the storage version before the first
// bump stays in storedVersions and is intentionally left (removing it would require a storage migration).
func prunableVersions(crd *apiextensionsv1.CustomResourceDefinition, currentVersion string) []string {
	stored := make(map[string]bool, len(crd.Status.StoredVersions))
	for _, v := range crd.Status.StoredVersions {
		stored[v] = true
	}
	var out []string
	for _, v := range crd.Spec.Versions {
		if v.Name == currentVersion || v.Name == generation.VacuumVersionName || stored[v.Name] {
			continue
		}
		out = append(out, v.Name)
	}
	return out
}

// PruneServedVersions removes the migration-free prunable served versions of the CRD (see prunableVersions)
// with optimistic concurrency, re-deciding against the fresh state on retry. It never touches live instances,
// storedVersions, or the vacuum/current version, so it is safe to call on every reconcile. Returns the names
// that were pruned (nil when there was nothing to do).
func PruneServedVersions(ctx context.Context, kubecli client.Client, gr schema.GroupResource, currentVersion string) ([]string, error) {
	live, err := Get(ctx, kubecli, gr)
	if err != nil || live == nil {
		return nil, err
	}
	if len(prunableVersions(live, currentVersion)) == 0 {
		return nil, nil // fast path: nothing prunable, avoid a write
	}

	var pruned []string
	err = applyMergedWithRetry(ctx, kubecli, gr, func(cur *apiextensionsv1.CustomResourceDefinition) error {
		names := prunableVersions(cur, currentVersion)
		if len(names) == 0 {
			return errNoPrune
		}
		prune := make(map[string]bool, len(names))
		for _, n := range names {
			prune[n] = true
		}
		ensureCRDTypeMeta(cur)
		generation.RemoveStaleVersions(cur, prune)
		pruned = names
		return nil
	})
	if errors.Is(err, errNoPrune) {
		return nil, nil
	}
	return pruned, err
}
