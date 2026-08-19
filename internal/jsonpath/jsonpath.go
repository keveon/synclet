package jsonpath

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse parses a rooted path such as `$` or `$.a.b.c`. Supported syntax is
// restricted: dotted map keys and numeric array indices only — no wildcards,
// filters or bracket syntax.
func Parse(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("json path is empty")
	}
	if path == "$" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "$.") {
		return nil, fmt.Errorf("json path %q must start with $.", path)
	}
	return parseRelative(path[2:])
}

// ParseRelative parses a path that may omit the `$` root, e.g. `a.b` or
// `$.a.b`.
func ParseRelative(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	if path == "$" || strings.HasPrefix(path, "$.") {
		return Parse(path)
	}
	return parseRelative(path)
}

// Get evaluates a rooted path against a decoded JSON value.
func Get(root any, path string) (any, bool, error) {
	tokens, err := Parse(path)
	if err != nil {
		return nil, false, err
	}
	return GetTokens(root, tokens)
}

// GetRelative evaluates a relative path against a decoded JSON value.
func GetRelative(root any, path string) (any, bool, error) {
	tokens, err := ParseRelative(path)
	if err != nil {
		return nil, false, err
	}
	return GetTokens(root, tokens)
}

// GetTokens evaluates pre-parsed tokens. Missing keys or out-of-range
// indices return (nil, false, nil); they are not errors.
func GetTokens(root any, tokens []string) (any, bool, error) {
	current := root
	for _, token := range tokens {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[token]
			if !ok {
				return nil, false, nil
			}
			current = next
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil {
				return nil, false, nil
			}
			if index < 0 || index >= len(typed) {
				return nil, false, nil
			}
			current = typed[index]
		default:
			return nil, false, nil
		}
	}
	return current, true, nil
}

func parseRelative(path string) ([]string, error) {
	parts := strings.Split(path, ".")
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("json path %q contains an empty segment", path)
		}
		if strings.ContainsAny(part, "[]*?") {
			return nil, fmt.Errorf("json path segment %q uses unsupported syntax", part)
		}
		tokens = append(tokens, part)
	}
	return tokens, nil
}
