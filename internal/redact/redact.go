package redact

import "regexp"

var (
	keyValuePattern  = regexp.MustCompile(`(?i)\b(password|passwd|pwd|token|secret)\s*[:=]\s*([^;&\s]+)`)
	urlAuthPattern   = regexp.MustCompile(`([a-z][a-z0-9+.-]*://[^:/@\s]+):([^/@\s]+)@`)
	mysqlAuthPattern = regexp.MustCompile(`(^|\s)([^:\s/@]+):([^@\s]+)@`)
)

// String masks password/token/secret key-value pairs and URL userinfo
// credentials in both URL and MySQL DSN forms.
func String(input string) string {
	output := keyValuePattern.ReplaceAllString(input, `$1=***`)
	output = urlAuthPattern.ReplaceAllString(output, `$1:***@`)
	output = mysqlAuthPattern.ReplaceAllString(output, `$1$2:***@`)
	return output
}

// Error masks sensitive information in an error message.
func Error(err error) string {
	if err == nil {
		return ""
	}
	return String(err.Error())
}
