package filter

import (
	"sort"
	"strings"
)

// Config is the scope section of the synclet configuration.
type Config struct {
	AllowAll     bool     `yaml:"allow_all"`
	AllowedCodes []string `yaml:"allowed_codes"`
}

// Filter is the resolved scope: either allow-everything or an allowlist of
// codes. The zero value allows nothing.
type Filter struct {
	allowAll bool
	allowed  map[string]struct{}
}

// New resolves a scope config into a Filter.
func New(cfg Config) Filter {
	allowed := make(map[string]struct{}, len(cfg.AllowedCodes))
	for _, code := range cfg.AllowedCodes {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		allowed[code] = struct{}{}
	}
	return Filter{allowAll: cfg.AllowAll, allowed: allowed}
}

// Allows reports whether the code passes the scope.
func (f Filter) Allows(code string) bool {
	if f.allowAll {
		return true
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	_, ok := f.allowed[code]
	return ok
}

// AllowAll reports whether the scope explicitly allows everything.
func (f Filter) AllowAll() bool {
	return f.allowAll
}

// AllowedCodes returns the sorted allowlist.
func (f Filter) AllowedCodes() []string {
	codes := make([]string, 0, len(f.allowed))
	for code := range f.allowed {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// EffectiveAllowedCodes resolves the config's allowlist.
func (cfg Config) EffectiveAllowedCodes() []string {
	return New(cfg).AllowedCodes()
}
