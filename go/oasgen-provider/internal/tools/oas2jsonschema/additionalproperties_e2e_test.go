package oas2jsonschema

import (
	"testing"

	"github.com/pb33f/libopenapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// End-to-end through the real libopenapi adapter, using the OAS snippet from issue #45.
func TestAdditionalProperties_EndToEndFromOAS(t *testing.T) {
	spec := []byte(`
openapi: 3.0.3
info: {title: t, version: "1.0"}
paths: {}
components:
  schemas:
    Metadata:
      type: object
      properties:
        annotations:
          type: object
          additionalProperties: {type: string}
`)
	doc, err := libopenapi.NewDocument(spec)
	require.NoError(t, err)
	model, errs := doc.BuildV3Model()
	require.Empty(t, errs)

	sp, ok := model.Model.Components.Schemas.Get("Metadata")
	require.True(t, ok)
	domain := convertLibopenapiSchema(sp)
	require.NotNil(t, domain)

	var ann *Schema
	for _, p := range domain.Properties {
		if p.Name == "annotations" {
			ann = p.Schema
		}
	}
	require.NotNil(t, ann, "annotations property must convert")
	require.NotNil(t, ann.AdditionalProperties, "object-form additionalProperties must survive conversion")
	require.True(t, ann.AdditionalProperties.IsSchema())
	assert.Equal(t, []string{"string"}, ann.AdditionalProperties.Schema.Type)

	m, err := schemaToMap(domain, DefaultGeneratorConfig())
	require.NoError(t, err)
	props := m["properties"].(map[string]interface{})
	annMap := props["annotations"].(map[string]interface{})
	ap, ok := annMap["additionalProperties"].(map[string]interface{})
	require.True(t, ok, "emitted schema must carry a typed map, got %#v", annMap["additionalProperties"])
	assert.Equal(t, "string", ap["type"], "map[string]string must reach the CRD typed")
}
