package dbutil

import (
	"fmt"
	"net/url"
	"strings"

	mysql "github.com/go-sql-driver/mysql"
)

// ParseMySQLDSN accepts either a native go-sql-driver DSN or a mysql:// URL
// and returns the driver config with parseTime enabled, so DATETIME cursor
// columns always decode as time.Time.
func ParseMySQLDSN(raw string) (*mysql.Config, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("DSN is empty")
	}

	var cfg *mysql.Config
	if strings.HasPrefix(strings.ToLower(raw), "mysql://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("mysql URL is invalid")
		}
		if !strings.EqualFold(parsed.Scheme, "mysql") {
			return nil, fmt.Errorf("URL scheme must be mysql")
		}
		if parsed.Host == "" {
			return nil, fmt.Errorf("mysql URL host is required")
		}

		user := ""
		password := ""
		if parsed.User != nil {
			user = parsed.User.Username()
			password, _ = parsed.User.Password()
		}

		driverDSN := "tcp(" + parsed.Host + ")/" + strings.TrimPrefix(parsed.EscapedPath(), "/")
		if parsed.RawQuery != "" {
			driverDSN += "?" + parsed.RawQuery
		}
		cfg, err = mysql.ParseDSN(driverDSN)
		if err != nil {
			return nil, fmt.Errorf("parse mysql URL options: %w", err)
		}
		cfg.User = user
		cfg.Passwd = password
	} else {
		parsed, err := mysql.ParseDSN(raw)
		if err != nil {
			return nil, err
		}
		cfg = parsed
	}

	cfg.ParseTime = true
	return cfg, nil
}
