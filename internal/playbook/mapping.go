package playbook

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func applyMapping(raw string, input map[string]any, context map[string]any, outputs map[string]map[string]any) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("步骤参数不是对象")
	}
	resolved, err := resolve(doc, input, context, outputs)
	if err != nil {
		return nil, err
	}
	out, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("步骤参数不是对象")
	}
	return out, nil
}

func resolve(v any, input, context map[string]any, outputs map[string]map[string]any) (any, error) {
	switch typed := v.(type) {
	case string:
		path, ok := templatePath(typed)
		if !ok {
			return typed, nil
		}
		return lookup(path, input, context, outputs)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			next, err := resolve(item, input, context, outputs)
			if err != nil {
				return nil, err
			}
			out[key] = next
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			next, err := resolve(item, input, context, outputs)
			if err != nil {
				return nil, err
			}
			out[i] = next
		}
		return out, nil
	default:
		return v, nil
	}
}

func templatePath(raw string) (string, bool) {
	text := strings.TrimSpace(raw)
	if !strings.HasPrefix(text, "{{") || !strings.HasSuffix(text, "}}") {
		return "", false
	}
	return strings.TrimSpace(text[2 : len(text)-2]), true
}

func lookup(path string, input, context map[string]any, outputs map[string]map[string]any) (any, error) {
	parts := strings.Split(path, ".")
	switch {
	case len(parts) == 3 && parts[0] == "run" && parts[1] == "input":
		value, ok := input[parts[2]]
		if !ok {
			return nil, fmt.Errorf("找不到参数 %s", path)
		}
		return value, nil
	case len(parts) == 3 && parts[0] == "run" && parts[1] == "context":
		value, ok := context[parts[2]]
		if !ok {
			return nil, fmt.Errorf("找不到参数 %s", path)
		}
		return value, nil
	case len(parts) == 4 && parts[0] == "steps" && parts[2] == "output":
		stepOut, ok := outputs[parts[1]]
		if !ok {
			return nil, fmt.Errorf("找不到参数 %s", path)
		}
		value, ok := stepOut[parts[3]]
		if !ok {
			return nil, fmt.Errorf("找不到参数 %s", path)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("找不到参数 %s", path)
	}
}

func asUint(v any) (uint, bool) {
	switch typed := v.(type) {
	case float64:
		if typed < 1 {
			return 0, false
		}
		return uint(typed), true
	case int:
		if typed < 1 {
			return 0, false
		}
		return uint(typed), true
	case uint:
		if typed == 0 {
			return 0, false
		}
		return typed, true
	case json.Number:
		n, err := typed.Int64()
		if err != nil || n < 1 {
			return 0, false
		}
		return uint(n), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil || n < 1 {
			return 0, false
		}
		return uint(n), true
	default:
		return 0, false
	}
}

func asUintList(v any) ([]uint, bool) {
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return nil, false
	}
	out := make([]uint, 0, len(items))
	for _, item := range items {
		id, ok := asUint(item)
		if !ok {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

func asString(v any) string {
	text, _ := v.(string)
	return strings.TrimSpace(text)
}

func decodeObject(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		return map[string]any{}
	}
	return out
}
