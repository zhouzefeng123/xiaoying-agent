package provider

import "strings"

func normalizedToolProperties(parameters map[string]any) map[string]any {
	if len(parameters) == 0 {
		return map[string]any{}
	}

	props, ok := parameters["properties"].(map[string]any)
	if !ok {
		return parameters
	}

	typeName, _ := parameters["type"].(string)
	if trimmed := strings.TrimSpace(typeName); trimmed != "" && !strings.EqualFold(trimmed, "object") {
		return parameters
	}

	return props
}

func normalizedToolRequired(parameters map[string]any, required []string) []string {
	if len(required) > 0 {
		return append([]string(nil), required...)
	}

	rawRequired, ok := parameters["required"]
	if !ok {
		return []string{}
	}

	switch values := rawRequired.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			name, ok := value.(string)
			if ok && strings.TrimSpace(name) != "" {
				result = append(result, name)
			}
		}
		return result
	default:
		return []string{}
	}
}

func normalizedToolObjectSchema(parameters map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": normalizedToolProperties(parameters),
		"required":   normalizedToolRequired(parameters, required),
	}
}
