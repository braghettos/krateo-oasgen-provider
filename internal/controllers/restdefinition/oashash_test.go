package restdefinition

import (
	"context"
	"testing"

	definitionv1alpha1 "github.com/krateoplatformops/oasgen-provider/apis/restdefinitions/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestOASContentDigest guards the content hash used for OAS drift detection: it must be deterministic for
// identical bytes and change for different bytes (otherwise an edited OAS would never be seen as drift).
func TestOASContentDigest(t *testing.T) {
	a := oasContentDigest([]byte("openapi: 3.0.0\n"))
	b := oasContentDigest([]byte("openapi: 3.0.0\n"))
	c := oasContentDigest([]byte("openapi: 3.1.0\n"))

	if a != b {
		t.Fatalf("digest is not deterministic: %q != %q", a, b)
	}
	if a == c {
		t.Fatal("digest is not content-sensitive: different bytes hashed to the same value")
	}
	if len(a) != 64 {
		t.Fatalf("expected a 64-char hex sha256, got %d chars", len(a))
	}
}

// TestFetchOASBytes_ConfigMapDriftDetection is the regression guard for the issue this fix addresses: an edit
// to the OAS ConfigMap (with oasPath unchanged) must change the resolved OAS content hash, so Observe reports
// drift. It exercises the real fetch path (configmap://) against a fake client, then mutates the ConfigMap.
func TestFetchOASBytes_ConfigMapDriftDetection(t *testing.T) {
	ctx := context.Background()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding corev1 to scheme: %v", err)
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "oas-cm", Namespace: "demo-system"},
		Data:       map[string]string{"openapi.yaml": "openapi: 3.0.0\ninfo:\n  title: v1\n"},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

	e := &external{kube: kube} // fetchOASBytes only uses e.kube
	cr := &definitionv1alpha1.RestDefinition{}
	cr.Spec.OASPath = "configmap://demo-system/oas-cm/openapi.yaml"

	b1, err := e.fetchOASBytes(ctx, cr)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	h1 := oasContentDigest(b1)

	// Simulate editing the OAS ConfigMap in place — oasPath is unchanged.
	cm.Data["openapi.yaml"] = "openapi: 3.0.0\ninfo:\n  title: v2\n"
	if err := kube.Update(ctx, cm); err != nil {
		t.Fatalf("updating configmap: %v", err)
	}

	b2, err := e.fetchOASBytes(ctx, cr)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	h2 := oasContentDigest(b2)

	if h1 == h2 {
		t.Fatal("OAS content hash did not change after the ConfigMap was edited (oasPath unchanged) — drift would be missed")
	}
}
