package oas2jsonschema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fmResponseSchemaFixture is a raw API response shape where the fields a status wants are nested and/or
// named differently from the CR domain, so they are only reachable via a response fieldMapping.
func fmResponseSchemaFixture() *Schema {
	return &Schema{Type: []string{"object"}, Properties: []Property{
		{Name: "run", Schema: &Schema{Type: []string{"object"}, Properties: []Property{
			{Name: "info", Schema: &Schema{Type: []string{"object"}, Properties: []Property{
				{Name: "run_id", Schema: &Schema{Type: []string{"string"}}},
				{Name: "experiment_id", Schema: &Schema{Type: []string{"integer"}, Format: "int64"}},
			}}},
		}}},
		{Name: "role_name", Schema: &Schema{Type: []string{"string"}}},
		{Name: "direct_field", Schema: &Schema{Type: []string{"integer"}}},
	}}
}

func fmPrimaryTypeOf(t *testing.T, s *Schema, name string) string {
	t.Helper()
	for _, p := range s.Properties {
		if p.Name == name {
			require.NotNil(t, p.Schema, "schema for %q is nil", name)
			return getPrimaryType(p.Schema.Type)
		}
	}
	t.Fatalf("status field %q not present in generated status schema", name)
	return ""
}

// TestComposeStatusSchema_InResponseMapping is the core M1 oasgen-side test: status fields relocated by a
// response fieldMapping must resolve their type through the mapping's source path, not fall back to string.
func TestComposeStatusSchema_InResponseMapping(t *testing.T) {
	rc := &ResourceConfig{
		Identifiers:            []string{"run_id"},
		AdditionalStatusFields: []string{"experiment_id", "permission", "opaque", "direct_field"},
		Verbs: []Verb{
			{Action: "get", Method: "GET", Path: "/runs/{id}", FieldMapping: []FieldMappingEntry{
				// plain relocation: nested string -> flat status.run_id
				{InResponse: "run.info.run_id", InCustomResource: "status.run_id"},
				// plain relocation: nested integer -> flat status.experiment_id (the key case:
				// without mapping-aware resolution this would string-fallback)
				{InResponse: "run.info.experiment_id", InCustomResource: "status.experiment_id"},
				// alias: a string enum remap -> string
				{InResponse: "role_name", InCustomResource: "status.permission", ValueMappingType: "alias"},
				// jq: opaque output -> string fallback (documented, not "not found")
				{InResponse: "run.info.run_id", InCustomResource: "status.opaque", ValueMappingType: "jq"},
			}},
		},
	}
	g := NewOASSchemaGenerator(nil, DefaultGeneratorConfig(), rc)

	allStatusFields := append(append([]string{}, rc.Identifiers...), rc.AdditionalStatusFields...)
	statusSchema, warnings := g.composeStatusSchema(allStatusFields, fmResponseSchemaFixture())

	assert.Equal(t, "string", fmPrimaryTypeOf(t, statusSchema, "run_id"), "relocated string field keeps string type")
	assert.Equal(t, "integer", fmPrimaryTypeOf(t, statusSchema, "experiment_id"), "relocated nested integer must keep integer type, not string-fallback")
	assert.Equal(t, "string", fmPrimaryTypeOf(t, statusSchema, "permission"), "alias-mapped field is a string enum")
	assert.Equal(t, "string", fmPrimaryTypeOf(t, statusSchema, "opaque"), "jq-produced field falls back to string")
	assert.Equal(t, "integer", fmPrimaryTypeOf(t, statusSchema, "direct_field"), "unmapped field resolves directly and is unaffected")

	// The only warning expected is the jq-derived one; the alias/plain relocations must NOT warn.
	var jqWarn bool
	for _, w := range warnings {
		msg := w.Error()
		assert.NotContains(t, msg, "'run_id'", "run_id resolved via mapping should not warn")
		assert.NotContains(t, msg, "'experiment_id'", "experiment_id resolved via mapping should not warn")
		if strings.Contains(msg, "'opaque'") && strings.Contains(msg, "jq") {
			jqWarn = true
		}
	}
	assert.True(t, jqWarn, "a jq-derived status field should emit a documented type-unknown warning")
}

// TestComposeStatusSchema_NoMappingUnchanged pins backward compatibility: with no fieldMapping, resolution
// is identical to before — present fields resolve, absent fields string-fallback with a warning.
func TestComposeStatusSchema_NoMappingUnchanged(t *testing.T) {
	rc := &ResourceConfig{
		Identifiers:            []string{"direct_field"},
		AdditionalStatusFields: []string{"missing_field"},
		Verbs:                  []Verb{{Action: "get", Method: "GET", Path: "/x"}}, // no FieldMapping
	}
	g := NewOASSchemaGenerator(nil, DefaultGeneratorConfig(), rc)

	statusSchema, warnings := g.composeStatusSchema([]string{"direct_field", "missing_field"}, fmResponseSchemaFixture())

	assert.Equal(t, "integer", fmPrimaryTypeOf(t, statusSchema, "direct_field"))
	assert.Equal(t, "string", fmPrimaryTypeOf(t, statusSchema, "missing_field"), "unknown field string-fallback preserved")

	var missingWarn bool
	for _, w := range warnings {
		if strings.Contains(w.Error(), "'missing_field'") && strings.Contains(w.Error(), "not found") {
			missingWarn = true
		}
	}
	assert.True(t, missingWarn, "an unresolvable status field must still warn")
}
