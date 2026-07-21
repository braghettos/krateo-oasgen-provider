package crd

import (
	"context"
	"fmt"

	"github.com/krateoplatformops/oasgen-provider/internal/tools/crd/generation"
	"github.com/krateoplatformops/oasgen-provider/internal/tools/kube"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Get returns the CRD for the given group-resource, or (nil, nil) when it does not exist.
func Get(ctx context.Context, kubecli client.Client, gr schema.GroupResource) (*apiextensionsv1.CustomResourceDefinition, error) {
	if err := registerEventually(); err != nil {
		return nil, err
	}
	res := &apiextensionsv1.CustomResourceDefinition{}
	if err := kubecli.Get(ctx, client.ObjectKey{Name: gr.String()}, res); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return res, nil
}

// ApplyOrUpdateCRD reconciles a freshly generated single-version CRD (newcrd) into the live, possibly
// multi-version, CRD WITHOUT clobbering other versions:
//
//   - live absent             → create newcrd as-is.
//   - live already has this version → replace ONLY that version's schema (spec+status) in place, preserving
//     every other version, the vacuum, and the live served/storage topology. Breaking same-version changes
//     are allowed (this is oasgen's deliberate divergence from core-provider, which does status-only here).
//   - live lacks this version → append the version alongside the non-served "vacuum" storage version.
//
// Conversion is set to None (the vacuum storage version provides lossless cross-version storage; no webhook).
// The merge path uses optimistic concurrency (read → merge → Update with the read's resourceVersion, retry on
// conflict), so a concurrent sibling-version change is re-merged on the next attempt rather than clobbered by
// a stale full PUT. Returns the target GVR (whose Version is newcrd's sole version name).
func ApplyOrUpdateCRD(ctx context.Context, kubecli client.Client, newcrd *apiextensionsv1.CustomResourceDefinition) (schema.GroupVersionResource, error) {
	if len(newcrd.Spec.Versions) == 0 {
		return schema.GroupVersionResource{}, fmt.Errorf("generated CRD %s has no versions", newcrd.Name)
	}
	gvr := schema.GroupVersionResource{
		Group:    newcrd.Spec.Group,
		Version:  newcrd.Spec.Versions[0].Name,
		Resource: newcrd.Spec.Names.Plural,
	}
	ensureCRDTypeMeta(newcrd)
	generation.AddVersionColumn(newcrd)

	live, err := Get(ctx, kubecli, gvr.GroupResource())
	if err != nil {
		return gvr, fmt.Errorf("getting CRD %s: %w", gvr.GroupResource().String(), err)
	}

	// Create: no live CRD yet. A concurrent create of the SAME CRD is prevented upstream by the group+kind
	// uniqueness guard (two RestDefinitions may not target the same kind), so last-write-wins on create is
	// acceptable here.
	if live == nil {
		if err := kube.Apply(ctx, kubecli, newcrd, kube.ApplyOptions{}); err != nil {
			return gvr, fmt.Errorf("creating CRD %s: %w", newcrd.Name, err)
		}
		return gvr, nil
	}

	// Merge into the live CRD with optimistic concurrency: re-read inside the retry and decide in-place vs
	// append against the FRESH state, then Update with that read's resourceVersion.
	gvk := schema.GroupVersionKind{Group: newcrd.Spec.Group, Kind: newcrd.Spec.Names.Kind, Version: gvr.Version}
	err = applyMergedWithRetry(ctx, kubecli, gvr.GroupResource(), func(cur *apiextensionsv1.CustomResourceDefinition) error {
		ensureCRDTypeMeta(cur)
		if generation.GVKExists(cur, gvk) {
			// In-place: swap ONLY this version's schema (breaking allowed); other versions + vacuum untouched.
			replaceVersionSchema(cur, gvr.Version, newcrd.Spec.Versions[0])
		} else {
			// Append: add this version alongside the non-served vacuum storage version.
			merged, aerr := generation.AppendVersion(*cur, *newcrd)
			if aerr != nil {
				return aerr
			}
			cur.Spec = merged.Spec
			generation.SetServedStorage(cur, gvr.Version, true, false)
		}
		setNoneConversion(cur)
		generation.AddVersionColumn(cur)
		return nil
	})
	if err != nil {
		return gvr, fmt.Errorf("merging version %s into CRD %s: %w", gvr.Version, gvr.GroupResource().String(), err)
	}
	return gvr, nil
}

// applyMergedWithRetry re-reads the CRD by group-resource, applies mergeFn, and Updates it with optimistic
// concurrency (the read's resourceVersion), retrying on conflict. Because it re-reads and re-merges on every
// attempt, a concurrent change (e.g. another served version added) is merged on top rather than clobbered by
// a stale full PUT.
func applyMergedWithRetry(ctx context.Context, kubecli client.Client, gr schema.GroupResource, mergeFn func(*apiextensionsv1.CustomResourceDefinition) error) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur := &apiextensionsv1.CustomResourceDefinition{}
		if err := kubecli.Get(ctx, client.ObjectKey{Name: gr.String()}, cur); err != nil {
			return err
		}
		if err := mergeFn(cur); err != nil {
			return err
		}
		return kubecli.Update(ctx, cur)
	})
}

// replaceVersionSchema swaps ONLY the OpenAPIV3Schema of the named version with the generated one, leaving
// its served/storage flags, name, and printer columns intact — the live version topology (e.g. a vacuum
// holding storage) must be preserved.
func replaceVersionSchema(live *apiextensionsv1.CustomResourceDefinition, version string, gen apiextensionsv1.CustomResourceDefinitionVersion) {
	for i := range live.Spec.Versions {
		if live.Spec.Versions[i].Name == version {
			live.Spec.Versions[i].Schema = gen.Schema
			return
		}
	}
}

// setNoneConversion sets conversion strategy None (no webhook; the vacuum storage version provides lossless
// cross-version storage).
func setNoneConversion(crd *apiextensionsv1.CustomResourceDefinition) {
	crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{Strategy: apiextensionsv1.NoneConverter}
}

// ensureCRDTypeMeta stamps the CRD GVK onto TypeMeta. Objects read via a typed client Get do not carry it,
// but oasgen's kube.Apply reads GVK off the object to build its GET, so it must be present.
func ensureCRDTypeMeta(crd *apiextensionsv1.CustomResourceDefinition) {
	crd.SetGroupVersionKind(apiextensionsv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"))
}
