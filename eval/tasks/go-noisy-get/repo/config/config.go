// Package config holds string settings.
package config

// Values is a set of settings.
type Values map[string]string

// Get returns a setting, or "" if it is not set.
func (v Values) Get(key string) string { return v[key] }

// Default returns the default settings.
func Default() Values {
	return Values{"currency": "USD", "low_stock": "2", "region": "us"}
}
