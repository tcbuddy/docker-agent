package openai

import (
	"maps"
	"slices"

	"github.com/openai/openai-go/v3/shared"

	"github.com/docker/docker-agent/pkg/tools"
)

// ConvertParametersToSchema converts parameters to OpenAI Schema format.
// It also returns whether the schema is compatible with strict mode.
// Schemas that declare schema-form additionalProperties (e.g. Notion MCP)
// are not strict-compatible: rewriting them to additionalProperties: false
// would lose the dictionary value shape the model needs. The caller should
// set Strict=false on the tool definition in that case.
func ConvertParametersToSchema(params any) (shared.FunctionParameters, bool, error) {
	p, err := tools.SchemaToMap(params)
	if err != nil {
		return nil, false, err
	}

	strict := isStrictCompatible(p)

	if strict {
		return fixSchemaArrayItems(removeFormatFields(ensureTypeFields(makeAllRequired(p)))), true, nil
	}
	return fixSchemaArrayItems(removeFormatFields(ensureTypeFields(p))), false, nil
}

// isStrictCompatible reports whether the schema can use OpenAI strict mode.
// Strict mode requires every object node to have additionalProperties: false.
// Schema-form additionalProperties (a map) and additionalProperties: true are
// both incompatible.
func isStrictCompatible(schema map[string]any) bool {
	compatible := true
	walkSchema(schema, func(node map[string]any) {
		if !compatible {
			return
		}
		v, ok := node["additionalProperties"]
		if !ok {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			compatible = false
		case bool:
			if t {
				compatible = false
			}
		}
	})
	return compatible
}

// walkSchema calls fn on the given schema node, then recursively walks into
// properties, anyOf/oneOf/allOf variants, array items, and additionalProperties.
func walkSchema(schema map[string]any, fn func(map[string]any)) {
	fn(schema)

	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, v := range properties {
			if sub, ok := v.(map[string]any); ok {
				walkSchema(sub, fn)
			}
		}
	}

	for _, keyword := range []string{"anyOf", "oneOf", "allOf"} {
		if variants, ok := schema[keyword].([]any); ok {
			for _, v := range variants {
				if sub, ok := v.(map[string]any); ok {
					walkSchema(sub, fn)
				}
			}
		}
	}

	if items, ok := schema["items"].(map[string]any); ok {
		walkSchema(items, fn)
	}

	// additionalProperties can be a boolean or an object schema
	if additionalProps, ok := schema["additionalProperties"].(map[string]any); ok {
		walkSchema(additionalProps, fn)
	}
}

// makeAllRequired enforces OpenAI strict mode: every object property is
// listed in `required` (newly-required ones are made nullable) and every
// object node has `additionalProperties: false`.
func makeAllRequired(schema shared.FunctionParameters) shared.FunctionParameters {
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}

	walkSchema(schema, func(node map[string]any) {
		isObject := false
		if typeVal, ok := node["type"]; ok {
			switch t := typeVal.(type) {
			case string:
				isObject = t == "object"
			case []any:
				for _, v := range t {
					if s, ok := v.(string); ok && s == "object" {
						isObject = true
						break
					}
				}
			case []string:
				isObject = slices.Contains(t, "object")
			}
		}

		if isObject {
			node["additionalProperties"] = false
		}

		properties, ok := node["properties"].(map[string]any)
		if !ok {
			return
		}

		originallyRequired := map[string]bool{}
		if required, ok := node["required"].([]any); ok {
			for _, name := range required {
				originallyRequired[name.(string)] = true
			}
		}

		newRequired := []any{}
		for _, propName := range slices.Sorted(maps.Keys(properties)) {
			newRequired = append(newRequired, propName)
			if !originallyRequired[propName] {
				if propMap, ok := properties[propName].(map[string]any); ok {
					if t, ok := propMap["type"].(string); ok {
						propMap["type"] = []string{t, "null"}
					}
				}
			}
		}

		node["required"] = newRequired
	})

	return schema
}

// ensureTypeFields ensures every schema node that is a map has a "type" key.
// OpenAI Responses API requires all schema nodes to have an explicit type.
// Nodes with "properties" default to "object"; other nodes default to "object" as well.
func ensureTypeFields(schema shared.FunctionParameters) shared.FunctionParameters {
	if schema == nil {
		return nil
	}

	walkSchema(schema, func(node map[string]any) {
		if _, hasType := node["type"]; !hasType {
			node["type"] = "object"
		}
	})

	return schema
}

// removeFormatFields removes the "format" field from all nodes in the schema.
// OpenAI does not support the JSON Schema "format" keyword (e.g. "uri", "email", "date").
func removeFormatFields(schema shared.FunctionParameters) shared.FunctionParameters {
	if schema == nil {
		return nil
	}

	walkSchema(schema, func(node map[string]any) {
		delete(node, "format")
	})

	return schema
}

// In Docker Desktop 4.52, the MCP Gateway produces an invalid tools shema for `mcp-config-set`.
func fixSchemaArrayItems(schema shared.FunctionParameters) shared.FunctionParameters {
	propertiesValue, ok := schema["properties"]
	if !ok {
		return schema
	}

	properties, ok := propertiesValue.(map[string]any)
	if !ok {
		return schema
	}

	for _, propValue := range properties {
		prop, ok := propValue.(map[string]any)
		if !ok {
			continue
		}

		checkForMissingItems := false
		switch t := prop["type"].(type) {
		case string:
			checkForMissingItems = t == "array"
		case []string:
			checkForMissingItems = slices.Contains(t, "array")
		}
		if !checkForMissingItems {
			continue
		}

		if _, ok := prop["items"]; !ok {
			prop["items"] = map[string]any{"type": "object"}
		}
	}

	return schema
}
