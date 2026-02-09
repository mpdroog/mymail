package config

import (
	"testing"
)

func TestParseSize(t *testing.T) {
	patterns := map[string]bool{
		"":     false,
		"10MB": true,
		"1GB":  true,
		"1KB":  true,
		"1B":   true,
	}
	for pattern, valid := range patterns {
		_, err := parseSize(pattern)
		if valid && err != nil {
			t.Errorf("Valid but pattern invalid: " + pattern)
		}
		if !valid && err == nil {
			t.Errorf("Invalid but pattern accepted: " + pattern)
		}
	}
}
