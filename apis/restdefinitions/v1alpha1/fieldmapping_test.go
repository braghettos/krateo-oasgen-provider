package v1alpha1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleVerbWithFieldMapping builds a "get" verb exercising the full unified fieldMapping surface:
// a response relocation with a Tier-1 alias, a response entry with a Tier-2 inline jq transform, a
// request entry, and both document-level transforms (inline and module-ref).
func sampleVerbWithFieldMapping() VerbsDescription {
	return VerbsDescription{
		Action: "get",
		Method: "GET",
		Path:   "/repos/{owner}/{repo}/teams/{team_slug}",
		FieldMapping: []FieldMappingItem{
			{
				// nested-object flatten + Tier-1 alias (teamrepo role_name -> spec.permission)
				InResponse:       "role_name",
				InCustomResource: "spec.permission",
				ValueMapping: &ValueMapping{
					Type: "alias",
					Aliases: []ValueAlias{
						{CustomResourceValue: "read", APIValue: "pull"},
						{CustomResourceValue: "write", APIValue: "push"},
					},
				},
			},
			{
				// Tier-2 per-field jq (non-bijective conditional)
				InResponse:       "owner.login",
				InCustomResource: "spec.owner",
				ValueMapping: &ValueMapping{
					Type: "jq",
					JQ:   &JQProgram{Inline: `if . == "read" then "pull" else . end`},
				},
			},
			{
				// request-direction entry, no transform
				InBody:           "permission",
				InCustomResource: "spec.permission",
			},
		},
		RequestTransform:  &JQProgram{Ref: "configmap://default/gh-transforms/req.jq", Entrypoint: "shape"},
		ResponseTransform: &JQProgram{Inline: `del(.required_signatures)`},
	}
}

// TestFieldMapping_JSONRoundTrip asserts the new types serialize with the expected JSON keys and
// survive a marshal/unmarshal round-trip unchanged — i.e. the CRD-facing wire contract is stable.
func TestFieldMapping_JSONRoundTrip(t *testing.T) {
	vd := sampleVerbWithFieldMapping()

	raw, err := json.Marshal(vd)
	require.NoError(t, err)

	for _, key := range []string{
		`"fieldMapping"`, `"inResponse"`, `"inCustomResource"`, `"valueMapping"`,
		`"aliases"`, `"customResourceValue"`, `"apiValue"`, `"jq"`, `"inline"`,
		`"requestTransform"`, `"responseTransform"`, `"ref"`, `"entrypoint"`,
	} {
		assert.Containsf(t, string(raw), key, "expected JSON to contain key %s", key)
	}

	var back VerbsDescription
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, vd, back, "VerbsDescription must survive a JSON round-trip unchanged")
}

// TestFieldMapping_OmitEmpty guarantees backward compatibility: a legacy verb that only uses
// requestFieldMapping must not emit any of the new keys, so existing RestDefinitions/CRs are byte-for-byte
// unaffected by the new optional fields.
func TestFieldMapping_OmitEmpty(t *testing.T) {
	legacy := VerbsDescription{
		Action: "create",
		Method: "POST",
		Path:   "/things",
		RequestFieldMapping: []RequestFieldMappingItem{
			{InBody: "name", InCustomResource: "spec.name"},
		},
	}
	raw, err := json.Marshal(legacy)
	require.NoError(t, err)
	for _, key := range []string{`"fieldMapping"`, `"valueMapping"`, `"requestTransform"`, `"responseTransform"`} {
		assert.NotContainsf(t, string(raw), key, "legacy verb must not emit new key %s", key)
	}
	assert.Contains(t, string(raw), `"requestFieldMapping"`)
}

// TestFieldMapping_DeepCopyIndependence exercises the generated deepcopy for the new pointer types
// (ValueMapping, JQProgram) reachable from the RestDefinition root: mutating a deep copy must never
// leak back into the original, or reconcile-time normalization could corrupt cached state.
func TestFieldMapping_DeepCopyIndependence(t *testing.T) {
	rd := &RestDefinition{
		Spec: RestDefinitionSpec{
			OASPath:       "https://example.com/oas.yaml",
			ResourceGroup: "example.kog.krateo.io",
			Resource: Resource{
				Kind:             "Team",
				VerbsDescription: []VerbsDescription{sampleVerbWithFieldMapping()},
			},
		},
	}

	cp := rd.DeepCopy()
	require.NotSame(t, rd, cp)

	// Mutate deep-copied nested pointer fields.
	cp.Spec.Resource.VerbsDescription[0].FieldMapping[0].ValueMapping.Aliases[0].APIValue = "MUTATED"
	cp.Spec.Resource.VerbsDescription[0].ResponseTransform.Inline = "MUTATED"

	// The original must be untouched.
	assert.Equal(t, "pull",
		rd.Spec.Resource.VerbsDescription[0].FieldMapping[0].ValueMapping.Aliases[0].APIValue,
		"deepcopy must not share the ValueMapping/alias pointer with the original")
	assert.Equal(t, `del(.required_signatures)`,
		rd.Spec.Resource.VerbsDescription[0].ResponseTransform.Inline,
		"deepcopy must not share the JQProgram pointer with the original")
}
