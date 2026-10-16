package provider

import (
	"reflect"
	"testing"
)

func TestNormalizedToolObjectSchemaWrapsPropertiesMap(t *testing.T) {
	parameters := map[string]any{
		"query": map[string]any{"type": "string"},
		"limit": map[string]any{"type": "string"},
	}

	schema := normalizedToolObjectSchema(parameters, []string{"query"})
	if schema["type"] != "object" {
		t.Fatalf("expected object schema, got %#v", schema["type"])
	}

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map, got %#v", schema["properties"])
	}
	if !reflect.DeepEqual(properties, parameters) {
		t.Fatalf("unexpected properties payload: %#v", properties)
	}

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("expected []string required, got %#v", schema["required"])
	}
	if !reflect.DeepEqual(required, []string{"query"}) {
		t.Fatalf("unexpected required fields: %#v", required)
	}
}

func TestNormalizedToolObjectSchemaUnwrapsExistingObjectSchema(t *testing.T) {
	parameters := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"download": map[string]any{"type": "string"},
			"analyze":  map[string]any{"type": "string"},
		},
		"required": []any{"download"},
	}

	schema := normalizedToolObjectSchema(parameters, nil)
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map, got %#v", schema["properties"])
	}
	if _, ok := properties["properties"]; ok {
		t.Fatalf("schema was wrapped twice: %#v", properties)
	}

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("expected []string required, got %#v", schema["required"])
	}
	if !reflect.DeepEqual(required, []string{"download"}) {
		t.Fatalf("unexpected required fields: %#v", required)
	}
}
