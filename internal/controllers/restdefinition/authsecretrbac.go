package restdefinition

import (
	"context"
	"fmt"

	definitionv1alpha1 "github.com/krateoplatformops/oasgen-provider/apis/restdefinitions/v1alpha1"
	"github.com/krateoplatformops/oasgen-provider/internal/tools/deploy"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// syncAuthSecretRBAC collects every Configuration instance's usernameRef/passwordRef/tokenRef, provisions/
// refreshes RDC's per-namespace auth-secret RBAC to match, and updates cr.Status.AuthSecretRBACNamespaces/
// AuthSecretDigest in place. Called from Create and Update, after deploy.Deploy has provisioned RDC's own
// ServiceAccount (so SelfServiceAccountName can resolve it).
func (e *external) syncAuthSecretRBAC(ctx context.Context, cr *definitionv1alpha1.RestDefinition, gvr schema.GroupVersionResource) error {
	configurationGVK := getConfigurationGVK(cr)

	refs, err := deploy.CollectAuthSecretRefs(ctx, e.kube, configurationGVK)
	if err != nil {
		return fmt.Errorf("collecting auth secret references: %w", err)
	}

	selfSA, err := deploy.SelfServiceAccountName(gvr, types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}, RDCrbacConfigFolder)
	if err != nil {
		return fmt.Errorf("resolving RDC's own ServiceAccount identity: %w", err)
	}

	roleName := deploy.AuthSecretRoleName(gvr)
	provisioned, err := deploy.SyncAuthSecretRBAC(ctx, e.kube, roleName, refs, cr.Status.AuthSecretRBACNamespaces, selfSA)
	if err != nil {
		return fmt.Errorf("syncing auth secret RBAC: %w", err)
	}
	cr.Status.AuthSecretRBACNamespaces = provisioned

	digest, err := deploy.AuthSecretRefsDigest(refs)
	if err != nil {
		return fmt.Errorf("hashing auth secret references: %w", err)
	}
	cr.Status.AuthSecretDigest = digest
	return nil
}

// teardownAuthSecretRBAC removes RDC's auth-secret RBAC from every namespace recorded on the
// RestDefinition's status. Called from Delete; unconditional and IsNotFound-tolerant (via
// deploy.DeleteAuthSecretRBAC), so it is safe even if some or all of it was never provisioned.
func (e *external) teardownAuthSecretRBAC(ctx context.Context, cr *definitionv1alpha1.RestDefinition, gvr schema.GroupVersionResource) error {
	if len(cr.Status.AuthSecretRBACNamespaces) == 0 {
		return nil
	}
	roleName := deploy.AuthSecretRoleName(gvr)
	return deploy.DeleteAllAuthSecretRBAC(ctx, e.kube, roleName, cr.Status.AuthSecretRBACNamespaces)
}
