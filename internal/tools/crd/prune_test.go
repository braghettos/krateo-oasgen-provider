package crd

import (
	"context"
	"testing"

	"github.com/krateoplatformops/oasgen-provider/internal/tools/crd/generation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPruneServedVersions(t *testing.T) {
	// After bumps v1-0-0 → v1-1-0 → v1-2-0: v1-0-0 was the initial storage version (in storedVersions),
	// v1-1-0/v1-2-0 were only ever served, vacuum holds storage. Only v1-1-0 is safely prunable.
	live := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "widgets.g"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "g",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: "K", Plural: "widgets"},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1-0-0", Served: true},
				{Name: "v1-1-0", Served: true},
				{Name: "v1-2-0", Served: true},
				{Name: generation.VacuumVersionName, Storage: true},
			},
		},
		Status: apiextensionsv1.CustomResourceDefinitionStatus{StoredVersions: []string{"v1-0-0", generation.VacuumVersionName}},
	}
	cli := fake.NewClientBuilder().WithScheme(testScheme(t)).WithStatusSubresource(live).WithObjects(live).Build()
	gr := schema.GroupResource{Group: "g", Resource: "widgets"}

	pruned, err := PruneServedVersions(context.Background(), cli, gr, "v1-2-0")
	require.NoError(t, err)
	assert.Equal(t, []string{"v1-1-0"}, pruned, "only v1-1-0 is migration-free prunable")

	got, err := Get(context.Background(), cli, gr)
	require.NoError(t, err)
	assert.Nil(t, findVer(got, "v1-1-0"), "v1-1-0 removed")
	assert.NotNil(t, findVer(got, "v1-0-0"), "v1-0-0 kept — it is in storedVersions (would need migration)")
	assert.NotNil(t, findVer(got, "v1-2-0"), "current version kept")
	assert.NotNil(t, findVer(got, generation.VacuumVersionName), "vacuum kept")

	// idempotent: nothing left to prune
	pruned2, err := PruneServedVersions(context.Background(), cli, gr, "v1-2-0")
	require.NoError(t, err)
	assert.Empty(t, pruned2)
}

func TestPruneServedVersions_NothingPrunable(t *testing.T) {
	// single-version CRD (no bump yet): nothing to prune, no write.
	live := genCRD("g", "K", "widgets", "v1-0-0", "A")
	live.Status.StoredVersions = []string{"v1-0-0"}
	cli := fake.NewClientBuilder().WithScheme(testScheme(t)).WithStatusSubresource(live).WithObjects(live).Build()

	pruned, err := PruneServedVersions(context.Background(), cli, schema.GroupResource{Group: "g", Resource: "widgets"}, "v1-0-0")
	require.NoError(t, err)
	assert.Empty(t, pruned)
}
