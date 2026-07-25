package oas2jsonschema

import (
	"encoding/json"
	"os"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	"github.com/krateoplatformops/plumbing/crdgen"
)

// TestE2E_OAS2JSONSchema_Through_Crdgen proves the real oasgen path: an OpenAPI spec ->
// oas2jsonschema (libopenapi resolves $refs -> JSON Schema) -> the crdgen transpiler -> a CRD
// accepted by the apiserver's own validation (crdgen.Generate's gate). It also asserts no
// top-level spec property is dropped.
func TestE2E_OAS2JSONSchema_Through_Crdgen(t *testing.T) {
	cases := []struct{ file, path, kind string }{
		{"testdata/petstore/petstore.yaml", "/pet", "Pet"},
		{"testdata/petstore/petstore_allOf.yaml", "/pet", "PetAllOf"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			content, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := NewLibOASParser().Parse(content)
			if err != nil {
				t.Fatalf("parse OAS: %v", err)
			}
			gen := NewOASSchemaGenerator(doc, DefaultGeneratorConfig(), &ResourceConfig{
				Verbs: []Verb{{Action: ActionCreate, Method: "POST", Path: tc.path}},
			})
			result, err := gen.Generate()
			if err != nil {
				t.Fatalf("oas2jsonschema: %v", err)
			}
			if len(result.SpecSchema) == 0 {
				t.Fatal("empty spec schema from oas2jsonschema")
			}
			out, err := crdgen.Generate(crdgen.Options{
				Group: "test.krateo.io", Version: "v1", Kind: tc.kind,
				SpecSchema: result.SpecSchema, StatusSchema: result.StatusSchema,
			})
			if err != nil {
				t.Fatalf("crdgen (apiserver gate rejected the CRD): %v", err)
			}
			var srcSchema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			_ = json.Unmarshal(result.SpecSchema, &srcSchema)
			var crd apiextensionsv1.CustomResourceDefinition
			if err := yaml.Unmarshal(out, &crd); err != nil {
				t.Fatalf("unmarshal CRD: %v", err)
			}
			crdProps := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties
			for k := range srcSchema.Properties {
				if _, ok := crdProps[k]; !ok {
					t.Errorf("spec property %q from oas2jsonschema was DROPPED by crdgen", k)
				}
			}
			t.Logf("OK %s: %d source props -> %d CRD props, CRD %d bytes", tc.file, len(srcSchema.Properties), len(crdProps), len(out))
		})
	}
}
