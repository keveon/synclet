package dbutil

import (
	"fmt"
	"regexp"
	"strings"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// QuoteIdentifier validates and quotes a single identifier with backticks
// (MySQL style). Arbitrary SQL inside identifiers is refused.
func QuoteIdentifier(identifier string) (string, error) {
	identifier = strings.TrimSpace(identifier)
	if !identifierPattern.MatchString(identifier) {
		return "", fmt.Errorf("invalid identifier %q", identifier)
	}
	return "`" + identifier + "`", nil
}

// QuoteMySQLPath quotes a dotted identifier path with backticks.
func QuoteMySQLPath(path string) (string, error) {
	return quotePath(path, "`")
}

// QuotePostgresPath quotes a dotted identifier path with double quotes.
func QuotePostgresPath(path string) (string, error) {
	return quotePath(path, `"`)
}

func quotePath(path string, quote string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("identifier path is empty")
	}
	parts := strings.Split(path, ".")
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !identifierPattern.MatchString(part) {
			return "", fmt.Errorf("invalid identifier %q", part)
		}
		quoted = append(quoted, quote+part+quote)
	}
	return strings.Join(quoted, "."), nil
}
