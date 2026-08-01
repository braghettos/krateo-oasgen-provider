package restdefinition

import (
	"fmt"
	"strings"

	definitionv1alpha1 "github.com/krateoplatformops/oasgen-provider/apis/restdefinitions/v1alpha1"
	"github.com/krateoplatformops/oasgen-provider/internal/tools/oas2jsonschema"
)

// toDomainFieldMapping converts a RestDefinition verb's field mappings into the library-agnostic
// oas2jsonschema representation used by the generator. It maps every unified FieldMapping entry
// (carrying the value-transform kind) and also translates the deprecated request-only RequestFieldMapping
// entries into equivalent request-direction entries, so the generator sees a single, complete model.
func toDomainFieldMapping(v definitionv1alpha1.VerbsDescription) []oas2jsonschema.FieldMappingEntry {
	if len(v.FieldMapping) == 0 && len(v.RequestFieldMapping) == 0 {
		return nil
	}
	out := make([]oas2jsonschema.FieldMappingEntry, 0, len(v.FieldMapping)+len(v.RequestFieldMapping))
	for _, m := range v.FieldMapping {
		var vmType string
		if m.ValueMapping != nil {
			vmType = m.ValueMapping.Type
		}
		out = append(out, oas2jsonschema.FieldMappingEntry{
			InPath:           m.InPath,
			InQuery:          m.InQuery,
			InBody:           m.InBody,
			InResponse:       m.InResponse,
			InCustomResource: m.InCustomResource,
			ValueMappingType: vmType,
		})
	}
	// Legacy requestFieldMapping: request-direction only, no value transform.
	for _, m := range v.RequestFieldMapping {
		out = append(out, oas2jsonschema.FieldMappingEntry{
			InPath:           m.InPath,
			InQuery:          m.InQuery,
			InBody:           m.InBody,
			InCustomResource: m.InCustomResource,
		})
	}
	return out
}

// expandWildcardActions expands "*" wildcard to all available verb actions
func expandWildcardActions(actions []string, verbsDescription []definitionv1alpha1.VerbsDescription) ([]string, error) {
	// Check for mixed wildcard usage first
	hasWildcard := false
	hasOthers := false
	for _, action := range actions {
		if action == "*" {
			hasWildcard = true
		} else {
			hasOthers = true
		}
	}

	if hasWildcard && hasOthers {
		return nil, fmt.Errorf("invalid configuration: '*' wildcard cannot be mixed with specific actions in the list")
	}

	if hasWildcard {
		expandedActions := make([]string, 0, len(verbsDescription))
		for _, verb := range verbsDescription {
			expandedActions = append(expandedActions, verb.Action)
		}
		return expandedActions, nil
	}

	return actions, nil
}

// operationIDToken is the path-parameter token rest-dynamic-controller binds the extracted async operation
// handle to. It is hardcoded there (internal/controllers/async_handler.go: params["operationId"]), so the
// OAS document must declare the poll endpoint with a parameter of exactly this name.
const operationIDToken = "{operationId}"

// validateAsyncPollPaths checks every verb's async.poll.path against rest-dynamic-controller's runtime
// contract, so a violation is reported when the RestDefinition is processed instead of on the first poll
// after a create has already fired.
//
// Two things must hold simultaneously, and neither was checked anywhere before:
//
//  1. The poll path must be an EXACT key of the OAS paths object. The poll call goes through the same
//     client as every other call, which resolves the path by exact string lookup — so a path differing from
//     the OAS key by even a parameter NAME is "path not found" on every poll.
//  2. The path must contain the literal {operationId} token, because that is the parameter name the handle
//     is bound to.
//
// Together these mean the OAS document itself must declare the poll endpoint with a parameter named
// operationId. Vendor specs do not: Aruba's baremetal API declares .../monitor/{id}. An author who writes
// the OAS's own path gets an unresolved required parameter; one who writes .../{operationId} gets an exact
// lookup miss. Both spellings failed at runtime and both were accepted — the failure this converts into an
// error names which of the two is wrong.
func validateAsyncPollPaths(cr *definitionv1alpha1.RestDefinition, doc oas2jsonschema.OASDocument) error {
	if cr == nil || doc == nil {
		return nil
	}
	for _, v := range cr.Spec.Resource.VerbsDescription {
		if v.Async == nil {
			continue
		}
		pollPath := v.Async.Poll.Path
		if pollPath == "" {
			continue // required by the CRD; nothing useful to add here
		}
		if !strings.Contains(pollPath, operationIDToken) {
			return fmt.Errorf(
				"verb %q: async.poll.path %q does not contain the %s token, so the extracted operation handle "+
					"has nothing to bind to and every poll would fail; the OAS document must declare the poll "+
					"endpoint with a path parameter named operationId",
				v.Action, pollPath, operationIDToken)
		}
		if _, ok := doc.FindPath(pollPath); !ok {
			return fmt.Errorf(
				"verb %q: async.poll.path %q is not a path declared in the OAS document (paths are matched by "+
					"exact string, so a differing parameter name is a miss); declare the poll endpoint with a "+
					"path parameter named operationId, or rename the existing one to match",
				v.Action, pollPath)
		}
	}
	return nil
}
