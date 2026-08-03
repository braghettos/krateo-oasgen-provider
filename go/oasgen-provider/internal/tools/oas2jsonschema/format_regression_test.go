package oas2jsonschema

import (
	"encoding/json"
	"testing"
)

// TestNumericFormatPreservedInSpecSchema guards the int64 -> int32 downgrade:
// a numeric "format" (notably int64) must be emitted into the generated spec
// schema, not just appended to the description. Without it, downstream CRD
// generation defaults integers to int32 and the kube-apiserver rejects any
// value above 2^31-1 (e.g. byte-sized fields such as disk size or memory).
//
// It also guards against a discriminator-enum collapse: two distinct nested
// "type" enums must each keep their own values.
func TestNumericFormatPreservedInSpecSchema(t *testing.T) {
	resourceConfig := &ResourceConfig{
		Verbs: []Verb{{Action: "create", Path: "/disks", Method: "post"}},
	}
	mockDoc := &mockOASDocument{
		Paths: map[string]*mockPathItem{
			"/disks": {Ops: map[string]Operation{
				"post": &mockOperation{RequestBody: RequestBodyInfo{
					Content: map[string]*Schema{"application/json": {
						Type: []string{"object"},
						Properties: []Property{
							{Name: "size", Schema: &Schema{Type: []string{"integer"}, Format: "int64", Description: "bytes"}},
							{Name: "ncpus", Schema: &Schema{Type: []string{"integer"}, Format: "int32"}},
							{Name: "disk_backend", Schema: &Schema{
								Type: []string{"object"},
								Properties: []Property{
									{Name: "type", Schema: &Schema{Type: []string{"string"}, Enum: []interface{}{"distributed", "local"}}},
									{Name: "disk_source", Schema: &Schema{
										Type: []string{"object"},
										Properties: []Property{
											{Name: "type", Schema: &Schema{Type: []string{"string"}, Enum: []interface{}{"blank", "snapshot", "image"}}},
										},
									}},
								},
							}},
						},
					}},
				}},
			}},
		},
	}

	res, err := NewOASSchemaGenerator(mockDoc, DefaultGeneratorConfig(), resourceConfig).Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var doc map[string]interface{}
	if err := json.Unmarshal(res.SpecSchema, &doc); err != nil {
		t.Fatalf("unmarshal spec schema: %v", err)
	}
	props, _ := doc["properties"].(map[string]interface{})

	// #45: int64 / int32 formats survive.
	if got := format(props, "size"); got != "int64" {
		t.Errorf("size: expected format int64, got %q\nschema: %s", got, res.SpecSchema)
	}
	if got := format(props, "ncpus"); got != "int32" {
		t.Errorf("ncpus: expected format int32, got %q", got)
	}

	// #46: each nested "type" keeps its own enum (no collapse).
	backend, _ := props["disk_backend"].(map[string]interface{})
	bprops, _ := backend["properties"].(map[string]interface{})
	assertEnum(t, "disk_backend.type", bprops["type"], []string{"distributed", "local"})
	source, _ := bprops["disk_source"].(map[string]interface{})
	sprops, _ := source["properties"].(map[string]interface{})
	assertEnum(t, "disk_source.type", sprops["type"], []string{"blank", "snapshot", "image"})
}

func format(props map[string]interface{}, name string) string {
	p, _ := props[name].(map[string]interface{})
	f, _ := p["format"].(string)
	return f
}

func assertEnum(t *testing.T, path string, raw interface{}, want []string) {
	t.Helper()
	m, _ := raw.(map[string]interface{})
	enumRaw, _ := m["enum"].([]interface{})
	if len(enumRaw) != len(want) {
		t.Errorf("%s: expected enum %v, got %v", path, want, enumRaw)
		return
	}
	for i, w := range want {
		if got, _ := enumRaw[i].(string); got != w {
			t.Errorf("%s: enum[%d] = %q, want %q (full %v)", path, i, got, w, enumRaw)
		}
	}
}
