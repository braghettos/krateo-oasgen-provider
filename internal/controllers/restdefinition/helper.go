package restdefinition

import (
	"fmt"

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
