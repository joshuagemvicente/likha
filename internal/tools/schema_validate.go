package tools

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"unicode/utf8"
)

type schemaValidation struct {
	schema *compiledSchema
	ctx    context.Context
	work   int
	fatal  error
}

func (s *compiledSchema) validate(ctx context.Context, value any) error {
	v := schemaValidation{schema: s, ctx: ctx}
	err := v.node(s.root, value, "$", 0, true)
	if v.fatal != nil {
		return v.fatal
	}
	return err
}

func (v *schemaValidation) step(depth int) error {
	v.work++
	if v.fatal != nil {
		return v.fatal
	}
	if err := v.ctx.Err(); err != nil {
		return err
	}
	if depth > jsonDepthLimit*2 || v.work > validationWorkLimit {
		v.fatal = errors.New("schema validation exceeded its safe complexity limit")
		return v.fatal
	}
	return nil
}

func (v *schemaValidation) node(schema, value any, path string, depth int, strictHere bool) error {
	if err := v.step(depth); err != nil {
		return err
	}
	if allowed, ok := schema.(bool); ok {
		if !allowed {
			return atPath(path, "value is not permitted by this schema")
		}
		if v.schema.strict && strictHere {
			if object, ok := value.(map[string]any); ok && len(object) != 0 {
				return atPath(path, "unrecognized object fields are not permitted")
			}
		}
		return nil
	}
	s := schema.(map[string]any) // All reachable schemas were checked at registration.
	if ref, ok := s["$ref"].(string); ok {
		target, err := v.schema.resolveRef(ref)
		if err != nil {
			return err
		}
		if err := v.node(target, value, path, depth+1, !v.schema.strict && strictHere); err != nil {
			return err
		}
		if v.schema.legacyRef {
			if v.schema.strict && strictHere {
				return v.strictFields(target, value, path, depth+1)
			}
			return nil
		}
	}
	if types, exists := s["type"]; exists {
		allowed, _ := schemaTypes(types)
		matched := false
		for _, name := range allowed {
			matched = matched || matchesType(name, value)
		}
		if !matched {
			return atPath(path, "value has an invalid JSON type")
		}
	}
	if values, ok := s["enum"].([]any); ok {
		matched := false
		fingerprint := jsonFingerprint(value)
		for _, candidate := range values {
			if err := v.step(depth); err != nil {
				return err
			}
			matched = matched || (jsonFingerprint(candidate) == fingerprint && equalJSON(candidate, value))
		}
		if !matched {
			return atPath(path, "value is not one of the permitted enum values")
		}
	}
	if constant, exists := s["const"]; exists && !equalJSON(constant, value) {
		return atPath(path, "value does not match the required constant")
	}
	if v.schema.strict && strictHere {
		if err := v.strictFields(s, value, path, depth+1); err != nil {
			return err
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		children, ok := s[keyword].([]any)
		if !ok {
			continue
		}
		matches := 0
		for _, child := range children {
			err := v.node(child, value, path, depth+1, false)
			if err == nil {
				matches++
			} else if err := v.step(depth); err != nil {
				return err
			}
		}
		if (keyword == "allOf" && matches != len(children)) || (keyword == "anyOf" && matches == 0) || (keyword == "oneOf" && matches != 1) {
			return atPath(path, "value does not satisfy "+keyword)
		}
	}
	if child, exists := s["not"]; exists {
		if v.node(child, value, path, depth+1, false) == nil {
			return atPath(path, "value matches a prohibited schema")
		}
		if err := v.step(depth); err != nil {
			return err
		}
	}
	if condition, exists := s["if"]; exists {
		branch := "else"
		if v.node(condition, value, path, depth+1, false) == nil {
			branch = "then"
		}
		if err := v.step(depth); err != nil {
			return err
		}
		if child, exists := s[branch]; exists {
			if err := v.node(child, value, path, depth+1, false); err != nil {
				return err
			}
		}
	}
	switch value := value.(type) {
	case map[string]any:
		return v.object(s, value, path, depth+1)
	case []any:
		return v.array(s, value, path, depth+1)
	case string:
		if err := checkCount(s, "Length", utf8.RuneCountInString(value), path); err != nil {
			return err
		}
		if pattern, ok := s["pattern"].(string); ok && !v.schema.patterns[pattern].MatchString(value) {
			return atPath(path, "string does not match the required pattern")
		}
	}
	if number, ok := rational(value); ok {
		for _, bound := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"} {
			limit, exists := rational(s[bound])
			if !exists {
				continue
			}
			cmp := number.Cmp(limit)
			if (bound == "minimum" && cmp < 0) || (bound == "maximum" && cmp > 0) || (bound == "exclusiveMinimum" && cmp <= 0) || (bound == "exclusiveMaximum" && cmp >= 0) {
				return atPath(path, "number is outside its permitted range")
			}
		}
		if multiple, exists := rational(s["multipleOf"]); exists && !new(big.Rat).Quo(number, multiple).IsInt() {
			return atPath(path, "number is not a permitted multiple")
		}
	}
	return nil
}

func (v *schemaValidation) object(s, value map[string]any, path string, depth int) error {
	if err := checkCount(s, "Properties", len(value), path); err != nil {
		return err
	}
	if err := checkRequired(s["required"], value, path); err != nil {
		return err
	}
	properties, _ := s["properties"].(map[string]any)
	patterns, _ := s["patternProperties"].(map[string]any)
	for _, key := range sortedKeys(value) {
		if err := v.step(depth); err != nil {
			return err
		}
		childPath := path + "[" + safeSchemaWord(key) + "]"
		if child, exists := s["propertyNames"]; exists {
			if err := v.node(child, key, path, depth+1, false); err != nil {
				return err
			}
		}
		matched := false
		if child, exists := properties[key]; exists {
			matched = true
			if err := v.node(child, value[key], childPath, depth+1, true); err != nil {
				return err
			}
		}
		for _, pattern := range sortedKeys(patterns) {
			if v.schema.patterns[pattern].MatchString(key) {
				matched = true
				if err := v.node(patterns[pattern], value[key], childPath, depth+1, true); err != nil {
					return err
				}
			}
		}
		if !matched {
			if child, exists := s["additionalProperties"]; exists {
				if err := v.node(child, value[key], childPath, depth+1, true); err != nil {
					return err
				}
			}
		}
	}
	for _, keyword := range []string{"dependentRequired", "dependentSchemas", "dependencies"} {
		dependencies, _ := s[keyword].(map[string]any)
		for _, key := range sortedKeys(dependencies) {
			if _, exists := value[key]; !exists {
				continue
			}
			child := dependencies[key]
			if _, required := child.([]any); required {
				if err := checkRequired(child, value, path); err != nil {
					return err
				}
			} else if err := v.node(child, value, path, depth+1, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *schemaValidation) array(s map[string]any, values []any, path string, depth int) error {
	if err := checkCount(s, "Items", len(values), path); err != nil {
		return err
	}
	if unique, _ := s["uniqueItems"].(bool); unique {
		seen := make(map[[32]byte][]any)
		for _, value := range values {
			fingerprint := jsonFingerprint(value)
			for _, earlier := range seen[fingerprint] {
				if err := v.step(depth); err != nil {
					return err
				}
				if equalJSON(value, earlier) {
					return atPath(path, "array items must be unique")
				}
			}
			seen[fingerprint] = append(seen[fingerprint], value)
		}
	}
	prefix, modern := s["prefixItems"].([]any)
	if tuple, ok := s["items"].([]any); ok {
		prefix = tuple
	}
	for i, value := range values {
		var child any
		var exists bool
		if i < len(prefix) {
			child, exists = prefix[i], true
		} else if _, tuple := s["items"].([]any); tuple {
			child, exists = s["additionalItems"]
		} else {
			child, exists = s["items"]
		}
		// prefixItems uses items only for the remaining elements. Homogeneous
		// items without a prefix continues to validate every element.
		if modern && i < len(prefix) {
			child, exists = prefix[i], true
		}
		if exists {
			if err := v.node(child, value, path+"["+strconv.Itoa(i)+"]", depth+1, true); err != nil {
				return err
			}
		}
	}
	if child, exists := s["contains"]; exists {
		matches := 0
		for _, value := range values {
			if v.node(child, value, path, depth+1, true) == nil {
				matches++
			}
			if err := v.step(depth); err != nil {
				return err
			}
		}
		if _, specified := s["minContains"]; !specified && matches == 0 {
			return atPath(path, "array must contain an item matching contains")
		}
		if err := checkCount(s, "Contains", matches, path); err != nil {
			return err
		}
	}
	return nil
}

// strictFields closes built-in objects without rewriting their provider schema.
// Composition/ref declarations are collected together so an allOf does not
// mistakenly reject a field declared by a sibling component.
func (v *schemaValidation) strictFields(schema, value any, path string, depth int) error {
	object, ok := value.(map[string]any)
	if !ok || len(object) == 0 {
		return nil
	}
	names := make(map[string]bool)
	patterns := make(map[string]bool)
	if err := v.fieldDeclarations(schema, names, patterns, make(map[string]bool), depth); err != nil {
		return err
	}
	for key := range object {
		if names[key] {
			continue
		}
		known := false
		for pattern := range patterns {
			known = known || v.schema.patterns[pattern].MatchString(key)
		}
		if !known {
			return atPath(path, "unrecognized object fields are not permitted")
		}
	}
	return nil
}

func (v *schemaValidation) fieldDeclarations(schema any, names, patterns, refs map[string]bool, depth int) error {
	if err := v.step(depth); err != nil {
		return err
	}
	s, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	if ref, ok := s["$ref"].(string); ok {
		if !refs[ref] {
			refs[ref] = true
			target, err := v.schema.resolveRef(ref)
			if err != nil {
				return err
			}
			if err := v.fieldDeclarations(target, names, patterns, refs, depth+1); err != nil {
				return err
			}
		}
		if v.schema.legacyRef {
			return nil
		}
	}
	if properties, ok := s["properties"].(map[string]any); ok {
		for name := range properties {
			names[name] = true
		}
	}
	if properties, ok := s["patternProperties"].(map[string]any); ok {
		for pattern := range properties {
			patterns[pattern] = true
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		if children, ok := s[keyword].([]any); ok {
			for _, child := range children {
				if err := v.fieldDeclarations(child, names, patterns, refs, depth+1); err != nil {
					return err
				}
			}
		}
	}
	for _, keyword := range []string{"then", "else"} {
		if child, exists := s[keyword]; exists {
			if err := v.fieldDeclarations(child, names, patterns, refs, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func matchesType(name string, value any) bool {
	switch name {
	case "null":
		return value == nil
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number", "integer":
		number, ok := rational(value)
		return ok && (name == "number" || number.IsInt())
	}
	return false
}

func checkRequired(required any, object map[string]any, path string) error {
	fields, _ := required.([]any)
	for _, field := range fields {
		name := field.(string)
		if _, exists := object[name]; !exists {
			return atPath(path, "missing required field "+safeSchemaWord(name))
		}
	}
	return nil
}

func checkCount(schema map[string]any, suffix string, count int, path string) error {
	value := new(big.Rat).SetInt64(int64(count))
	if minimum, exists := rational(schema["min"+suffix]); exists && value.Cmp(minimum) < 0 {
		return atPath(path, "value is below min"+suffix)
	}
	if maximum, exists := rational(schema["max"+suffix]); exists && value.Cmp(maximum) > 0 {
		return atPath(path, "value exceeds max"+suffix)
	}
	return nil
}

func atPath(path, message string) error { return fmt.Errorf("%s: %s", path, message) }
