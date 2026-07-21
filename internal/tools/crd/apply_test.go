package crd

import (
	"context"
	"fmt"
	"testing"

	"github.com/krateoplatformops/oasgen-provider/internal/tools/crd/generation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, apiextensionsv1.AddToScheme(s))
	return s
}

// genCRD builds a single-version generated CRD, as crdgen would emit (served+storage on the one version).
func genCRD(group, kind, plural, version, desc string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: plural + "." + group},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: kind, Plural: plural},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    version,
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"spec":   {Type: "object", Description: desc},
							"status": {Type: "object"},
						},
					},
				},
			}},
		},
	}
}

func findVer(crd *apiextensionsv1.CustomResourceDefinition, name string) *apiextensionsv1.CustomResourceDefinitionVersion {
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Name == name {
			return &crd.Spec.Versions[i]
		}
	}
	return nil
}

func specDesc(v *apiextensionsv1.CustomResourceDefinitionVersion) string {
	return v.Schema.OpenAPIV3Schema.Properties["spec"].Description
}

func vacuum(name string) apiextensionsv1.CustomResourceDefinitionVersion {
	return apiextensionsv1.CustomResourceDefinitionVersion{
		Name: name, Served: false, Storage: true,
		Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
			Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{"status": {Type: "object"}},
		}},
	}
}

func TestApplyOrUpdateCRD_Create(t *testing.T) {
	cli := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	newcrd := genCRD("github.krateo.io", "PullRequest", "pullrequests", "v1-0-0", "A")

	gvr, err := ApplyOrUpdateCRD(context.Background(), cli, newcrd)
	require.NoError(t, err)
	assert.Equal(t, schema.GroupVersionResource{Group: "github.krateo.io", Version: "v1-0-0", Resource: "pullrequests"}, gvr)

	got, err := Get(context.Background(), cli, gvr.GroupResource())
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Spec.Versions, 1)
	require.NotEmpty(t, got.Spec.Versions[0].AdditionalPrinterColumns, "VERSION column added on create")
	assert.Equal(t, "VERSION", got.Spec.Versions[0].AdditionalPrinterColumns[0].Name)
}

func TestApplyOrUpdateCRD_InPlaceBreaking(t *testing.T) {
	// live: v1-0-0 (schema A, served, non-storage) + vacuum (storage).
	live := genCRD("g", "K", "widgets", "v1-0-0", "A")
	live.Spec.Versions[0].Storage = false
	live.Spec.Versions = append(live.Spec.Versions, vacuum(generation.VacuumVersionName))
	cli := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(live).Build()

	// re-apply v1-0-0 with a BREAKING new spec schema B
	_, err := ApplyOrUpdateCRD(context.Background(), cli, genCRD("g", "K", "widgets", "v1-0-0", "B"))
	require.NoError(t, err)

	got, err := Get(context.Background(), cli, schema.GroupResource{Group: "g", Resource: "widgets"})
	require.NoError(t, err)
	v := findVer(got, "v1-0-0")
	require.NotNil(t, v)
	assert.Equal(t, "B", specDesc(v), "matching version's spec schema replaced in place (breaking allowed)")
	assert.True(t, v.Served, "served flag preserved")
	assert.False(t, v.Storage, "vacuum still holds storage; the served version is not flipped to storage")
	require.NotNil(t, findVer(got, generation.VacuumVersionName), "vacuum preserved")
	require.NotNil(t, got.Spec.Conversion)
	assert.Equal(t, apiextensionsv1.NoneConverter, got.Spec.Conversion.Strategy)
}

func TestApplyOrUpdateCRD_AppendNewVersion(t *testing.T) {
	// live: single version v1-0-0 (served+storage), no vacuum yet.
	live := genCRD("g", "K", "widgets", "v1-0-0", "A")
	cli := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(live).Build()

	_, err := ApplyOrUpdateCRD(context.Background(), cli, genCRD("g", "K", "widgets", "v1-1-0", "B"))
	require.NoError(t, err)

	got, err := Get(context.Background(), cli, schema.GroupResource{Group: "g", Resource: "widgets"})
	require.NoError(t, err)
	require.Len(t, got.Spec.Versions, 3, "v1-0-0, v1-1-0, vacuum")
	require.NotNil(t, findVer(got, "v1-0-0"))
	require.NotNil(t, findVer(got, "v1-1-0"))
	vac := findVer(got, generation.VacuumVersionName)
	require.NotNil(t, vac)
	assert.True(t, vac.Storage)
	assert.False(t, vac.Served)
	for _, n := range []string{"v1-0-0", "v1-1-0"} {
		v := findVer(got, n)
		assert.True(t, v.Served, "%s served", n)
		assert.False(t, v.Storage, "%s not storage (vacuum is)", n)
	}
	require.NotNil(t, got.Spec.Conversion)
	assert.Equal(t, apiextensionsv1.NoneConverter, got.Spec.Conversion.Strategy)
}

// The merge path uses optimistic concurrency: a conflicting Update must be retried (re-read + re-merge),
// not surfaced as an error. Inject a 409 on the first Update and assert ApplyOrUpdateCRD still succeeds and
// the change lands, with the vacuum preserved across the retry.
func TestApplyOrUpdateCRD_RetriesOnConflict(t *testing.T) {
	live := genCRD("g", "K", "widgets", "v1-0-0", "A")
	live.Spec.Versions[0].Storage = false
	live.Spec.Versions = append(live.Spec.Versions, vacuum(generation.VacuumVersionName))

	updateCalls := 0
	cli := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(live).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				updateCalls++
				if updateCalls == 1 {
					return apierrors.NewConflict(
						schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"},
						obj.GetName(), fmt.Errorf("simulated conflict"))
				}
				return c.Update(ctx, obj, opts...)
			},
		}).Build()

	_, err := ApplyOrUpdateCRD(context.Background(), cli, genCRD("g", "K", "widgets", "v1-0-0", "B"))
	require.NoError(t, err, "conflict must be retried, not returned")
	assert.GreaterOrEqual(t, updateCalls, 2, "first Update conflicted and the retry re-ran")

	got, err := Get(context.Background(), cli, schema.GroupResource{Group: "g", Resource: "widgets"})
	require.NoError(t, err)
	assert.Equal(t, "B", specDesc(findVer(got, "v1-0-0")), "in-place change applied after the retry")
	require.NotNil(t, findVer(got, generation.VacuumVersionName), "vacuum preserved through the retry")
}

// Regression guard for the original clobber bug: a full-PUT apply must not drop a sibling served version.
func TestApplyOrUpdateCRD_InPlaceKeepsSiblingVersion(t *testing.T) {
	live := genCRD("g", "K", "widgets", "v1-0-0", "A")
	live.Spec.Versions[0].Storage = false
	live.Spec.Versions = append(live.Spec.Versions,
		apiextensionsv1.CustomResourceDefinitionVersion{
			Name: "v1-1-0", Served: true, Storage: false,
			Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
				Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{"spec": {Type: "object", Description: "B"}, "status": {Type: "object"}},
			}},
		},
		vacuum(generation.VacuumVersionName),
	)
	cli := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(live).Build()

	// in-place update of v1-0-0 only
	_, err := ApplyOrUpdateCRD(context.Background(), cli, genCRD("g", "K", "widgets", "v1-0-0", "A2"))
	require.NoError(t, err)

	got, err := Get(context.Background(), cli, schema.GroupResource{Group: "g", Resource: "widgets"})
	require.NoError(t, err)
	assert.Equal(t, "A2", specDesc(findVer(got, "v1-0-0")), "v1-0-0 updated")
	sib := findVer(got, "v1-1-0")
	require.NotNil(t, sib, "sibling served version must survive the full-PUT apply")
	assert.Equal(t, "B", specDesc(sib), "sibling schema untouched")
	require.NotNil(t, findVer(got, generation.VacuumVersionName), "vacuum survives")
}
