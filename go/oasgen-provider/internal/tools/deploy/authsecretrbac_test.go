package deploy

import (
	"context"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var widgetConfigurationGVK = schema.GroupVersionKind{Group: "widgets.example.io", Version: "v1alpha1", Kind: "WidgetConfiguration"}

func newConfigScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := rbacv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	s.AddKnownTypeWithName(widgetConfigurationGVK, &unstructured.Unstructured{})
	listGVK := widgetConfigurationGVK
	listGVK.Kind = listGVK.Kind + "List"
	s.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})
	return s
}

func newConfigInstance(ns, name string, basicUser, basicPass, bearerToken *map[string]interface{}) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(widgetConfigurationGVK)
	u.SetNamespace(ns)
	u.SetName(name)
	auth := map[string]interface{}{}
	if basicUser != nil || basicPass != nil {
		basic := map[string]interface{}{}
		if basicUser != nil {
			basic["usernameRef"] = *basicUser
		}
		if basicPass != nil {
			basic["passwordRef"] = *basicPass
		}
		auth["basic"] = basic
	}
	if bearerToken != nil {
		auth["bearer"] = map[string]interface{}{"tokenRef": *bearerToken}
	}
	_ = unstructured.SetNestedMap(u.Object, auth, "spec", "authentication")
	return u
}

func secretRef(ns, name, key string) map[string]interface{} {
	return map[string]interface{}{"namespace": ns, "name": name, "key": key}
}

func TestCollectAuthSecretRefs_AcrossInstancesAndNamespaces(t *testing.T) {
	s := newConfigScheme(t)
	tokenRef := secretRef("ns2", "gh-token", "token")
	userRef := secretRef("ns1", "gh-creds", "username")
	passRef := secretRef("ns1", "gh-creds", "password")
	inst1 := newConfigInstance("ns1", "cfg1", &userRef, &passRef, nil)
	inst2 := newConfigInstance("ns2", "cfg2", nil, nil, &tokenRef)

	kube := fake.NewClientBuilder().WithScheme(s).WithObjects(inst1, inst2).Build()

	got, err := CollectAuthSecretRefs(context.Background(), kube, widgetConfigurationGVK)
	if err != nil {
		t.Fatalf("CollectAuthSecretRefs: %v", err)
	}
	if len(got["ns1"]) != 1 || got["ns1"][0] != "gh-creds" {
		t.Fatalf("expected ns1: [gh-creds], got %v", got["ns1"])
	}
	if len(got["ns2"]) != 1 || got["ns2"][0] != "gh-token" {
		t.Fatalf("expected ns2: [gh-token], got %v", got["ns2"])
	}
}

func TestCollectAuthSecretRefs_NoConfigurationKind(t *testing.T) {
	s := newConfigScheme(t)
	kube := fake.NewClientBuilder().WithScheme(s).Build()

	got, err := CollectAuthSecretRefs(context.Background(), kube, schema.GroupVersionKind{})
	if err != nil {
		t.Fatalf("expected no error for an empty GVK, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil result for an empty GVK, got %v", got)
	}
}

func TestCollectAuthSecretRefs_NoInstances(t *testing.T) {
	s := newConfigScheme(t)
	kube := fake.NewClientBuilder().WithScheme(s).Build()

	got, err := CollectAuthSecretRefs(context.Background(), kube, widgetConfigurationGVK)
	if err != nil {
		t.Fatalf("CollectAuthSecretRefs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no secret refs, got %v", got)
	}
}

func rbacScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := rbacv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func TestSyncAuthSecretRBAC_ProvisionsPerNamespace(t *testing.T) {
	kube := fake.NewClientBuilder().WithScheme(rbacScheme(t)).Build()
	sa := types.NamespacedName{Namespace: "rdc-system", Name: "rdc"}

	bySecretNamespace := map[string][]string{
		"ns1": {"gh-creds"},
		"ns2": {"gh-token"},
	}

	provisioned, err := SyncAuthSecretRBAC(context.Background(), kube, "widgets-v1alpha1-authsecrets", bySecretNamespace, nil, sa)
	if err != nil {
		t.Fatalf("SyncAuthSecretRBAC: %v", err)
	}
	if len(provisioned) != 2 || provisioned[0] != "ns1" || provisioned[1] != "ns2" {
		t.Fatalf("expected [ns1 ns2], got %v", provisioned)
	}

	for _, ns := range []string{"ns1", "ns2"} {
		var role rbacv1.Role
		if err := kube.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "widgets-v1alpha1-authsecrets"}, &role); err != nil {
			t.Fatalf("expected Role in %s: %v", ns, err)
		}
		var binding rbacv1.RoleBinding
		if err := kube.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "widgets-v1alpha1-authsecrets"}, &binding); err != nil {
			t.Fatalf("expected RoleBinding in %s: %v", ns, err)
		}
		if len(binding.Subjects) != 1 || binding.Subjects[0].Name != sa.Name || binding.Subjects[0].Namespace != sa.Namespace {
			t.Fatalf("unexpected RoleBinding subjects in %s: %+v", ns, binding.Subjects)
		}
	}
}

func TestSyncAuthSecretRBAC_RemovesStaleNamespaces(t *testing.T) {
	kube := fake.NewClientBuilder().WithScheme(rbacScheme(t)).Build()
	sa := types.NamespacedName{Namespace: "rdc-system", Name: "rdc"}
	roleName := "widgets-v1alpha1-authsecrets"

	// First sync provisions ns1 and ns2.
	provisioned, err := SyncAuthSecretRBAC(context.Background(), kube, roleName,
		map[string][]string{"ns1": {"a"}, "ns2": {"b"}}, nil, sa)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Second sync only needs ns1 now: ns2's RBAC must be torn down.
	provisioned, err = SyncAuthSecretRBAC(context.Background(), kube, roleName,
		map[string][]string{"ns1": {"a"}}, provisioned, sa)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(provisioned) != 1 || provisioned[0] != "ns1" {
		t.Fatalf("expected [ns1], got %v", provisioned)
	}

	var role rbacv1.Role
	if err := kube.Get(context.Background(), client.ObjectKey{Namespace: "ns2", Name: roleName}, &role); !errors.IsNotFound(err) {
		t.Fatalf("expected ns2's Role to be removed, got err %v", err)
	}
	if err := kube.Get(context.Background(), client.ObjectKey{Namespace: "ns1", Name: roleName}, &role); err != nil {
		t.Fatalf("expected ns1's Role to remain: %v", err)
	}
}

func TestDeleteAllAuthSecretRBAC(t *testing.T) {
	kube := fake.NewClientBuilder().WithScheme(rbacScheme(t)).Build()
	sa := types.NamespacedName{Namespace: "rdc-system", Name: "rdc"}
	roleName := "widgets-v1alpha1-authsecrets"

	if _, err := SyncAuthSecretRBAC(context.Background(), kube, roleName,
		map[string][]string{"ns1": {"a"}, "ns2": {"b"}}, nil, sa); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if err := DeleteAllAuthSecretRBAC(context.Background(), kube, roleName, []string{"ns1", "ns2"}); err != nil {
		t.Fatalf("DeleteAllAuthSecretRBAC: %v", err)
	}

	var role rbacv1.Role
	for _, ns := range []string{"ns1", "ns2"} {
		if err := kube.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: roleName}, &role); !errors.IsNotFound(err) {
			t.Fatalf("expected %s's Role to be removed, got err %v", ns, err)
		}
	}
}

func TestDeleteAuthSecretRBAC_IdempotentWhenNeverProvisioned(t *testing.T) {
	kube := fake.NewClientBuilder().WithScheme(rbacScheme(t)).Build()
	if err := DeleteAuthSecretRBAC(context.Background(), kube, "never-provisioned", "ns1"); err != nil {
		t.Fatalf("expected teardown of never-provisioned RBAC to succeed, got %v", err)
	}
}

func TestAuthSecretRoleName_DeterministicAndDistinct(t *testing.T) {
	a := schema.GroupVersionResource{Group: "widgets.example.io", Version: "v2", Resource: "widgets"}
	b := schema.GroupVersionResource{Group: "widgets.example.io", Version: "v2", Resource: "gadgets"}
	if AuthSecretRoleName(a) != AuthSecretRoleName(a) {
		t.Fatal("expected deterministic naming")
	}
	if AuthSecretRoleName(a) == AuthSecretRoleName(b) {
		t.Fatal("expected distinct resources to get distinct role names")
	}
}
