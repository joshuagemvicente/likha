package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	jsonDepthLimit      = 128
	schemaNodeLimit     = 4096
	validationWorkLimit = 100000
)

// compiledSchema implements a deliberately explicit JSON Schema subset. A
// keyword with unsupported validation semantics is a registration error, not
// an ignored constraint. External/dynamic references and format vocabularies
// therefore cannot accidentally create an unvalidated MCP dispatch path.
type compiledSchema struct {
	root        any
	strict      bool
	legacyRef   bool
	modernItems bool
	patterns    map[string]*regexp.Regexp
}

func compileSchema(raw json.RawMessage, strict bool) (*compiledSchema, error) {
	root, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	schema := &compiledSchema{root: root, strict: strict, patterns: make(map[string]*regexp.Regexp)}
	if object, ok := root.(map[string]any); ok {
		if dialect, exists := object["$schema"]; exists {
			text, ok := dialect.(string)
			if !ok {
				return nil, errors.New("$schema must be a string")
			}
			text = strings.TrimSuffix(strings.Replace(text, "https://", "http://", 1), "#")
			switch text {
			case "http://json-schema.org/draft-04/schema", "http://json-schema.org/draft-06/schema", "http://json-schema.org/draft-07/schema":
				schema.legacyRef = true
			case "http://json-schema.org/draft/2019-09/schema":
			case "http://json-schema.org/draft/2020-12/schema":
				schema.modernItems = true
			default:
				return nil, errors.New("unsupported JSON Schema dialect")
			}
		}
	}
	checker := schemaChecker{schema: schema, refs: map[string]bool{"#": true}}
	if err := checker.check(root, 0); err != nil {
		return nil, err
	}
	work := 0
	for _, node := range checker.checked {
		if err := schema.checkReferenceProgress(node, make(map[string]bool), 0, &work); err != nil {
			return nil, err
		}
	}
	return schema, nil
}

type schemaChecker struct {
	schema  *compiledSchema
	refs    map[string]bool
	nodes   int
	checked []any
}

func (c *schemaChecker) check(node any, depth int) error {
	c.nodes++
	if depth > jsonDepthLimit || c.nodes > schemaNodeLimit {
		return errors.New("schema is too complex to validate safely")
	}
	if _, ok := node.(bool); ok {
		return nil
	}
	object, ok := node.(map[string]any)
	if !ok {
		return errors.New("schema nodes must be objects or booleans")
	}
	c.checked = append(c.checked, node)
	for _, key := range sortedKeys(object) {
		value := object[key]
		var err error
		switch key {
		case "$schema":
			if _, ok := value.(string); !ok {
				err = errors.New("must be a string")
			}
		case "title", "description", "$comment", "contentEncoding", "contentMediaType":
			if _, ok := value.(string); !ok {
				err = errors.New("must be a string")
			}
		case "default": // Annotations do not authorize or change arguments.
		case "examples":
			if _, ok := value.([]any); !ok {
				err = errors.New("must be an array")
			}
		case "deprecated", "readOnly", "writeOnly":
			if _, ok := value.(bool); !ok {
				err = errors.New("must be a boolean")
			}
		case "$ref":
			ref, ok := value.(string)
			if !ok {
				err = errors.New("must be a local JSON pointer string")
				break
			}
			var target any
			target, err = c.schema.resolveRef(ref)
			if err == nil && !c.refs[ref] {
				c.refs[ref] = true
				err = c.check(target, depth+1)
			}
		case "type":
			types, ok := schemaTypes(value)
			if !ok || len(types) == 0 {
				err = errors.New("must name supported, distinct JSON types")
			}
		case "enum":
			values, ok := value.([]any)
			if !ok || len(values) == 0 || len(values) > schemaNodeLimit {
				err = errors.New("must be a nonempty bounded array")
				break
			}
			seen := make(map[[32]byte][]any)
			for _, candidate := range values {
				fingerprint := jsonFingerprint(candidate)
				for _, earlier := range seen[fingerprint] {
					if equalJSON(candidate, earlier) {
						err = errors.New("must contain distinct values")
						break
					}
				}
				if err != nil {
					break
				}
				seen[fingerprint] = append(seen[fingerprint], candidate)
			}
		case "const":
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
			children, ok := value.(map[string]any)
			if !ok {
				err = errors.New("must be an object of schemas")
				break
			}
			for _, name := range sortedKeys(children) {
				if key == "patternProperties" {
					if err = c.pattern(name); err != nil {
						break
					}
				}
				if err = c.check(children[name], depth+1); err != nil {
					break
				}
			}
		case "required":
			err = checkStringSet(value)
		case "dependentRequired":
			children, ok := value.(map[string]any)
			if !ok {
				err = errors.New("must be an object of required-field arrays")
				break
			}
			for _, name := range sortedKeys(children) {
				if err = checkStringSet(children[name]); err != nil {
					break
				}
			}
		case "dependencies":
			children, ok := value.(map[string]any)
			if !ok {
				err = errors.New("must be an object of schemas or required-field arrays")
				break
			}
			for _, name := range sortedKeys(children) {
				if _, ok := children[name].([]any); ok {
					err = checkStringSet(children[name])
				} else {
					err = c.check(children[name], depth+1)
				}
				if err != nil {
					break
				}
			}
		case "additionalProperties", "propertyNames", "additionalItems", "contains", "not", "if", "then", "else":
			err = c.check(value, depth+1)
		case "items":
			if children, ok := value.([]any); ok {
				if c.schema.modernItems {
					err = errors.New("tuple items require prefixItems in the 2020-12 dialect")
					break
				}
				err = c.checkList(children, depth, false)
			} else {
				err = c.check(value, depth+1)
			}
		case "prefixItems":
			children, ok := value.([]any)
			if !ok {
				err = errors.New("must be an array of schemas")
			} else {
				err = c.checkList(children, depth, false)
			}
		case "allOf", "anyOf", "oneOf":
			children, ok := value.([]any)
			if !ok {
				err = errors.New("must be a nonempty array of schemas")
			} else {
				err = c.checkList(children, depth, true)
			}
		case "pattern":
			pattern, ok := value.(string)
			if !ok {
				err = errors.New("must be a string")
			} else {
				err = c.pattern(pattern)
			}
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
			number, ok := rational(value)
			if !ok {
				err = errors.New("must be a number (boolean exclusive bounds are unsupported)")
			} else if key == "multipleOf" && number.Sign() <= 0 {
				err = errors.New("must be positive")
			}
		case "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties", "minContains", "maxContains":
			number, ok := rational(value)
			if !ok || !number.IsInt() || number.Sign() < 0 {
				err = errors.New("must be a nonnegative integer")
			}
		case "uniqueItems":
			if _, ok := value.(bool); !ok {
				err = errors.New("must be a boolean")
			}
		default:
			err = errors.New("unsupported schema keyword")
		}
		if err != nil {
			return fmt.Errorf("%s: %w", safeSchemaWord(key), err)
		}
	}
	if _, exists := object["prefixItems"]; exists {
		if _, tuple := object["items"].([]any); tuple {
			return errors.New("prefixItems and tuple items cannot be combined safely")
		}
		if c.schema.legacyRef {
			return errors.New("prefixItems requires a newer JSON Schema dialect")
		}
	}
	return nil
}

func (c *schemaChecker) checkList(children []any, depth int, nonempty bool) error {
	if nonempty && len(children) == 0 {
		return errors.New("must contain at least one schema")
	}
	for _, child := range children {
		if err := c.check(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (c *schemaChecker) pattern(pattern string) error {
	if _, exists := c.schema.patterns[pattern]; exists {
		return nil
	}
	// JSON Schema uses ECMA-262 patterns. Accept the shared ASCII subset,
	// not Go-specific classes/flags, lookaround, or backreferences. Wildcards,
	// complement classes and Unicode literals have UTF-16/rune differences;
	// refuse them rather than silently changing their validation semantics.
	if strings.Contains(pattern, "(?") || strings.Contains(pattern, "[[:") || strings.Contains(pattern, "[^") {
		return errors.New("unsupported nonportable regular expression")
	}
	inClass := false
	for i := 0; i < len(pattern); i++ {
		if pattern[i] >= utf8.RuneSelf || (pattern[i] == '.' && !inClass) {
			return errors.New("unsupported nonportable regular expression")
		}
		if pattern[i] == '[' {
			inClass = true
		} else if pattern[i] == ']' {
			inClass = false
		}
		if pattern[i] != '\\' {
			continue
		}
		i++
		if i == len(pattern) {
			return errors.New("invalid regular expression")
		}
		if strings.ContainsRune("aAbBGQEzZpPkK0123456789uUxXcCsSDW", rune(pattern[i])) {
			return errors.New("unsupported nonportable regular expression escape")
		}
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return errors.New("invalid or unsupported regular expression")
	}
	c.schema.patterns[pattern] = compiled
	return nil
}

func (s *compiledSchema) resolveRef(ref string) (any, error) {
	if ref == "#" {
		return s.root, nil
	}
	if !strings.HasPrefix(ref, "#/") || strings.Contains(ref, "%") {
		return nil, errors.New("only local JSON pointer references are supported")
	}
	current := s.root
	for _, token := range strings.Split(ref[2:], "/") {
		for i := 0; i < len(token); i++ {
			if token[i] == '~' {
				if i+1 >= len(token) || (token[i+1] != '0' && token[i+1] != '1') {
					return nil, errors.New("invalid JSON pointer escape")
				}
				i++
			}
		}
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch node := current.(type) {
		case map[string]any:
			var exists bool
			current, exists = node[token]
			if !exists {
				return nil, errors.New("local reference does not resolve")
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(node) || strconv.Itoa(index) != token {
				return nil, errors.New("local reference has an invalid array index")
			}
			current = node[index]
		default:
			return nil, errors.New("local reference does not resolve")
		}
	}
	return current, nil
}

// Recursive data structures are supported; recursion over the same instance
// without descending to a property/item is not. Hide such schemas up front,
// including loops concealed in composition or conditional branches.
func (s *compiledSchema) checkReferenceProgress(node any, active map[string]bool, depth int, work *int) error {
	*work++
	if depth > jsonDepthLimit || *work > validationWorkLimit {
		return errors.New("schema reference graph is too complex to validate safely")
	}
	object, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	if ref, ok := object["$ref"].(string); ok {
		if active[ref] {
			return errors.New("local references recurse without descending into an argument value")
		}
		target, err := s.resolveRef(ref)
		if err != nil {
			return err
		}
		active[ref] = true
		err = s.checkReferenceProgress(target, active, depth+1, work)
		delete(active, ref)
		if err != nil {
			return err
		}
		if s.legacyRef {
			return nil
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		if children, ok := object[keyword].([]any); ok {
			for _, child := range children {
				if err := s.checkReferenceProgress(child, active, depth+1, work); err != nil {
					return err
				}
			}
		}
	}
	for _, keyword := range []string{"not", "if", "then", "else"} {
		if child, exists := object[keyword]; exists {
			if err := s.checkReferenceProgress(child, active, depth+1, work); err != nil {
				return err
			}
		}
	}
	for _, keyword := range []string{"dependentSchemas", "dependencies"} {
		if children, ok := object[keyword].(map[string]any); ok {
			for _, key := range sortedKeys(children) {
				if _, required := children[key].([]any); required {
					continue
				}
				if err := s.checkReferenceProgress(children[key], active, depth+1, work); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func checkStringSet(value any) error {
	values, ok := value.([]any)
	if !ok {
		return errors.New("must be an array of distinct strings")
	}
	seen := make(map[string]bool)
	for _, candidate := range values {
		text, ok := candidate.(string)
		if !ok || seen[text] {
			return errors.New("must be an array of distinct strings")
		}
		seen[text] = true
	}
	return nil
}

func schemaTypes(value any) ([]string, bool) {
	values, ok := value.([]any)
	if text, isString := value.(string); isString {
		values, ok = []any{text}, true
	}
	if !ok {
		return nil, false
	}
	types := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, candidate := range values {
		text, ok := candidate.(string)
		if !ok || seen[text] {
			return nil, false
		}
		switch text {
		case "object", "array", "string", "number", "integer", "boolean", "null":
		default:
			return nil, false
		}
		seen[text] = true
		types = append(types, text)
	}
	return types, true
}

func rational(value any) (*big.Rat, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return nil, false
	}
	// Avoid allocating an enormous big.Int from an adversarial exponent. This
	// is a validation safety limit, never a conversion to lossy float64.
	text := string(number)
	if len(text) > 4096 {
		return nil, false
	}
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(text[i+1:])
		if err != nil || exponent < -4096 || exponent > 4096 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(text)
}

func equalJSON(a, b any) bool {
	if left, ok := a.(json.Number); ok {
		right, ok := b.(json.Number)
		if !ok {
			return false
		}
		x, xOK := rational(left)
		y, yOK := rational(right)
		return xOK && yOK && x.Cmp(y) == 0
	}
	switch left := a.(type) {
	case nil:
		return b == nil
	case bool:
		right, ok := b.(bool)
		return ok && left == right
	case string:
		right, ok := b.(string)
		return ok && left == right
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i := range left {
			if !equalJSON(left[i], right[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, exists := right[key]
			if !exists || !equalJSON(value, other) {
				return false
			}
		}
		return true
	}
	return false
}

// Fingerprints make enum/uniqueItems checking linear in payload size instead
// of quadratic in the number of large, almost-identical entries. Hash matches
// are still compared structurally, including exact rational number equality.
func jsonFingerprint(value any) [32]byte {
	hash := sha256.New()
	var write func(any)
	write = func(value any) {
		switch value := value.(type) {
		case nil:
			io.WriteString(hash, "null;")
		case bool:
			io.WriteString(hash, strconv.FormatBool(value)+";")
		case string:
			io.WriteString(hash, "s"+strconv.Quote(value)+";")
		case json.Number:
			number, _ := rational(value)
			io.WriteString(hash, "n"+number.RatString()+";")
		case []any:
			io.WriteString(hash, "[")
			for _, child := range value {
				write(child)
			}
			io.WriteString(hash, "]")
		case map[string]any:
			io.WriteString(hash, "{")
			for _, key := range sortedKeys(value) {
				write(key)
				write(value[key])
			}
			io.WriteString(hash, "}")
		}
	}
	write(value)
	var fingerprint [32]byte
	copy(fingerprint[:], hash.Sum(nil))
	return fingerprint
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func safeSchemaWord(word string) string {
	word = strings.ToValidUTF8(word, "\uFFFD")
	if len(word) > 128 {
		word = utf8Prefix(word, 128) + "…"
	}
	return strconv.Quote(word)
}

// decodeJSON rejects duplicate keys, trailing values, non-UTF-8 input, and
// excessive nesting instead of letting encoding/json replace or overwrite them.
func decodeJSON(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("JSON must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	work := 0
	value, err := decodeValue(decoder, 0, &work)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("JSON must contain exactly one value")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder, depth int, work *int) (any, error) {
	*work++
	if depth > jsonDepthLimit || *work > validationWorkLimit {
		return nil, errors.New("JSON is too complex to validate safely")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, errors.New("malformed JSON")
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok {
					return nil, errors.New("malformed JSON object")
				}
				if _, exists := object[name]; exists {
					return nil, errors.New("JSON contains duplicate object fields")
				}
				value, err := decodeValue(decoder, depth+1, work)
				if err != nil {
					return nil, err
				}
				object[name] = value
			}
			if close, err := decoder.Token(); err != nil || close != json.Delim('}') {
				return nil, errors.New("malformed JSON object")
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				value, err := decodeValue(decoder, depth+1, work)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			if close, err := decoder.Token(); err != nil || close != json.Delim(']') {
				return nil, errors.New("malformed JSON array")
			}
			return array, nil
		default:
			return nil, errors.New("malformed JSON")
		}
	}
	if number, ok := token.(json.Number); ok {
		if _, ok := rational(number); !ok {
			return nil, errors.New("JSON number is too large to validate safely")
		}
	}
	return token, nil
}
