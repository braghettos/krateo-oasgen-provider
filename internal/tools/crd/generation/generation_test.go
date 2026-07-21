package generation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func ptrTrue() *bool { b := true; return &b }

func versionWithStatus(name, statusDesc string) apiextensionsv1.CustomResourceDefinitionVersion {
	return apiextensionsv1.CustomResourceDefinitionVersion{
		Name: name,
		Schema: &apiextensionsv1.CustomResourceValidation{
			OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"status": {Type: "object", Description: statusDesc},
				},
			},
		},
	}
}

func crdWith(group, kind string, versions ...apiextensionsv1.CustomResourceDefinitionVersion) apiextensionsv1.CustomResourceDefinition {
	return apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group:    group,
			Names:    apiextensionsv1.CustomResourceDefinitionNames{Kind: kind, Plural: "widgets"},
			Versions: versions,
		},
	}
}

// findVersion returns the named version, or nil.
func findVersion(crd *apiextensionsv1.CustomResourceDefinition, name string) *apiextensionsv1.CustomResourceDefinitionVersion {
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Name == name {
			return &crd.Spec.Versions[i]
		}
	}
	return nil
}

func TestSetServedStorage(t *testing.T) {
	crd := crdWith("g", "K",
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-0-0", Served: true, Storage: true},
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-1-0", Served: true, Storage: false},
	)
	SetServedStorage(&crd, "v1-0-0", false, false)
	assert.False(t, findVersion(&crd, "v1-0-0").Served)
	assert.False(t, findVersion(&crd, "v1-0-0").Storage)
	// untouched
	assert.True(t, findVersion(&crd, "v1-1-0").Served)
	// absent version is a no-op (no panic)
	SetServedStorage(&crd, "nope", true, true)
}

func TestAppendVersion_NewVersionInjectsVacuumAndFlipsFlags(t *testing.T) {
	base := crdWith("g", "K",
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-0-0", Served: true, Storage: true},
	)
	add := crdWith("g", "K",
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-1-0", Served: true, Storage: true},
	)

	out, err := AppendVersion(base, add)
	require.NoError(t, err)

	// exactly one vacuum injected
	vac := findVersion(out, VacuumVersionName)
	require.NotNil(t, vac, "vacuum must be injected")
	assert.False(t, vac.Served, "vacuum is never served")
	assert.True(t, vac.Storage, "vacuum is the storage version")
	assert.NotNil(t, vac.Schema.OpenAPIV3Schema.Properties["spec"].XPreserveUnknownFields)

	// three versions total: both served, neither is storage
	require.Len(t, out.Spec.Versions, 3)
	for _, name := range []string{"v1-0-0", "v1-1-0"} {
		v := findVersion(out, name)
		require.NotNil(t, v)
		assert.True(t, v.Served, "%s must be served", name)
		assert.False(t, v.Storage, "%s must not be storage (vacuum is)", name)
	}

	// input CRD (by value) is not mutated
	assert.Len(t, base.Spec.Versions, 1)
}

func TestAppendVersion_Idempotent(t *testing.T) {
	base := crdWith("g", "K",
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-0-0", Served: true, Storage: true},
	)
	// appending a version already present changes nothing
	out, err := AppendVersion(base, base)
	require.NoError(t, err)
	assert.Len(t, out.Spec.Versions, 1)
	assert.Nil(t, findVersion(out, VacuumVersionName), "no vacuum when nothing new was added")
}

func TestAppendVersion_SecondNewVersionDoesNotAddSecondVacuum(t *testing.T) {
	base := crdWith("g", "K",
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-0-0", Served: true, Storage: true},
	)
	after1, err := AppendVersion(base, crdWith("g", "K",
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-1-0"}))
	require.NoError(t, err)

	after2, err := AppendVersion(*after1, crdWith("g", "K",
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-2-0"}))
	require.NoError(t, err)

	vacs := 0
	for _, v := range after2.Spec.Versions {
		if v.Name == VacuumVersionName {
			vacs++
		}
	}
	assert.Equal(t, 1, vacs, "exactly one vacuum across repeated appends")
	assert.Len(t, after2.Spec.Versions, 4, "v1-0-0, v1-1-0, v1-2-0, vacuum")
}

func TestRemoveStaleVersions(t *testing.T) {
	crd := crdWith("g", "K",
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-0-0"},
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-1-0"},
		apiextensionsv1.CustomResourceDefinitionVersion{Name: VacuumVersionName, Storage: true},
	)
	// try to prune an old version AND (maliciously) the vacuum
	removed := RemoveStaleVersions(&crd, map[string]bool{"v1-0-0": true, VacuumVersionName: true})
	assert.True(t, removed)
	assert.Nil(t, findVersion(&crd, "v1-0-0"), "stale version removed")
	assert.NotNil(t, findVersion(&crd, "v1-1-0"), "unpruned version kept")
	assert.NotNil(t, findVersion(&crd, VacuumVersionName), "vacuum is never removed even if listed to prune")

	// nothing to prune → returns false
	assert.False(t, RemoveStaleVersions(&crd, map[string]bool{"absent": true}))
}

func TestAddVersionColumn(t *testing.T) {
	crd := crdWith("g", "K",
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-0-0", Served: true},
		apiextensionsv1.CustomResourceDefinitionVersion{Name: VacuumVersionName, Storage: true},
	)
	AddVersionColumn(&crd)
	AddVersionColumn(&crd) // idempotent

	served := findVersion(&crd, "v1-0-0")
	require.Len(t, served.AdditionalPrinterColumns, 1, "column added exactly once (idempotent)")
	assert.Equal(t, "VERSION", served.AdditionalPrinterColumns[0].Name)
	assert.Contains(t, served.AdditionalPrinterColumns[0].JSONPath, "oas-version")

	assert.Empty(t, findVersion(&crd, VacuumVersionName).AdditionalPrinterColumns, "vacuum is skipped")
}

func TestUpdateStatus(t *testing.T) {
	crd := crdWith("g", "K",
		versionWithStatus("v1-0-0", "old-status"),
		versionWithStatus("v1-1-0", "different-status"),
	)
	newVer := versionWithStatus("v1-1-0", "canonical-status")

	require.NoError(t, UpdateStatus(&crd, newVer))
	for _, name := range []string{"v1-0-0", "v1-1-0"} {
		st := findVersion(&crd, name).Schema.OpenAPIV3Schema.Properties["status"]
		assert.Equal(t, "canonical-status", st.Description, "%s status propagated", name)
	}

	// nil schema errors
	err := UpdateStatus(&crd, apiextensionsv1.CustomResourceDefinitionVersion{Name: "x"})
	require.Error(t, err)
}

func TestStatusEqual(t *testing.T) {
	a := crdWith("g", "K", versionWithStatus("v1-0-0", "same"))
	b := crdWith("g", "K", versionWithStatus("v9-9-9", "same"))
	c := crdWith("g", "K", versionWithStatus("v1-0-0", "different"))

	eq, err := StatusEqual(&a, &b)
	require.NoError(t, err)
	assert.True(t, eq, "identical status subschema (version name irrelevant) → equal")

	eq, err = StatusEqual(&a, &c)
	require.NoError(t, err)
	assert.False(t, eq, "different status subschema → not equal")

	// no status property anywhere → error
	noStatus := crdWith("g", "K", apiextensionsv1.CustomResourceDefinitionVersion{
		Name:   "v1-0-0",
		Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"}},
	})
	_, err = StatusEqual(&a, &noStatus)
	require.Error(t, err)
}

func TestGVKExists(t *testing.T) {
	crd := crdWith("github.krateo.io", "PullRequest",
		apiextensionsv1.CustomResourceDefinitionVersion{Name: "v1-0-0"},
		apiextensionsv1.CustomResourceDefinitionVersion{Name: VacuumVersionName},
	)
	assert.True(t, GVKExists(&crd, schema.GroupVersionKind{Group: "github.krateo.io", Kind: "PullRequest", Version: "v1-0-0"}))
	assert.False(t, GVKExists(&crd, schema.GroupVersionKind{Group: "github.krateo.io", Kind: "PullRequest", Version: "v2-0-0"}), "absent version")
	assert.False(t, GVKExists(&crd, schema.GroupVersionKind{Group: "other", Kind: "PullRequest", Version: "v1-0-0"}), "group mismatch")
	assert.False(t, GVKExists(&crd, schema.GroupVersionKind{Group: "github.krateo.io", Kind: "Other", Version: "v1-0-0"}), "kind mismatch")
}
