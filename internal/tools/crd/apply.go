package crd

import (
	"context"
	"fmt"

	"github.com/krateoplatformops/oasgen-provider/internal/tools/crd/generation"
	"github.com/krateoplatformops/oasgen-provider/internal/tools/kube"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
// It always PUTs a FULLY MERGED CRD, so oasgen's GET-then-PUT kube.Apply never drops a sibling version.
// Returns the target GVR (whose Version is newcrd's sole version name).
func ApplyOrUpdateCRD(ctx context.Context, kubecli client.Client, newcrd *apiextensionsv1.CustomResourceDefinition) (schema.GroupVersionResource, error) {
	if len(newcrd.Spec.Versions) == 0 {
		return schema.GroupVersionResource{}, fmt.Errorf("generated CRD %s has no versions", newcrd.Name)
	}
	gvr := schema.GroupVersionResource{
		Group:    newcrd.Spec.Group,
		Version:  newcrd.Spec.Versions[0].Name,
		Resource: newcrd.Spec.Names.Plural,
	}
	// Surface the per-instance served version in kubectl; done on newcrd before any branch so create applies
	// it and append carries it onto the new version.
	generation.AddVersionColumn(newcrd)
	ensureCRDTypeMeta(newcrd)

	live, err := Get(ctx, kubecli, gvr.GroupResource())
	if err != nil {
		return gvr, fmt.Errorf("getting CRD %s: %w", gvr.GroupResource().String(), err)
	}

	// Create: no live CRD yet.
	if live == nil {
		if err := kube.Apply(ctx, kubecli, newcrd, kube.ApplyOptions{}); err != nil {
			return gvr, fmt.Errorf("creating CRD %s: %w", newcrd.Name, err)
		}
		return gvr, nil
	}
	ensureCRDTypeMeta(live)

	// STAGE 3 BLOCKER: the two merge branches below read `live` above, mutate it, then PUT the whole object
	// via kube.Apply — which is last-write-wins (it copies the server's current resourceVersion onto our
	// already-merged object, so a concurrent writer that added a sibling version between our Get and the PUT
	// is silently clobbered). This is harmless while only the create branch is reachable (Stage 2), but BEFORE
	// these branches go live in Stage 3 the merge+apply must move to optimistic concurrency: Update with the
	// resourceVersion from the read that produced `live`, and on a 409 re-Get + re-merge + retry.
	gvk := schema.GroupVersionKind{Group: newcrd.Spec.Group, Kind: newcrd.Spec.Names.Kind, Version: gvr.Version}

	// In-place replace of an existing version's schema (breaking allowed). Preserve every other version and
	// the live served/storage flags — only the schema is swapped.
	if generation.GVKExists(live, gvk) {
		replaceVersionSchema(live, gvr.Version, newcrd.Spec.Versions[0])
		generation.AddVersionColumn(live)
		setNoneConversion(live)
		if err := kube.Apply(ctx, kubecli, live, kube.ApplyOptions{}); err != nil {
			return gvr, fmt.Errorf("updating CRD %s version %s in place: %w", live.Name, gvr.Version, err)
		}
		return gvr, nil
	}

	// Append a new served version alongside the vacuum storage version.
	merged, err := generation.AppendVersion(*live, *newcrd)
	if err != nil {
		return gvr, fmt.Errorf("appending version %s to CRD %s: %w", gvr.Version, live.Name, err)
	}
	setNoneConversion(merged)
	generation.SetServedStorage(merged, gvr.Version, true, false)
	generation.AddVersionColumn(merged)
	ensureCRDTypeMeta(merged)
	if err := kube.Apply(ctx, kubecli, merged, kube.ApplyOptions{}); err != nil {
		return gvr, fmt.Errorf("appending version to CRD %s: %w", merged.Name, err)
	}
	return gvr, nil
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
