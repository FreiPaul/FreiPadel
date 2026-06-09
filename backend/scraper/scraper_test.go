package scraper

import (
	"encoding/json"
	"testing"
)

// TestNewFailsFast verifies that New rejects misconfigured sources at build
// time rather than silently skipping them at fetch time.
func TestNewFailsFast(t *testing.T) {
	tests := []struct {
		name    string
		sources []SourceConfig
	}{
		{
			name:    "unknown type",
			sources: []SourceConfig{{Type: "matchi", Options: json.RawMessage(`{}`)}},
		},
		{
			name:    "malformed options json",
			sources: []SourceConfig{{Type: "mock", Options: json.RawMessage(`not json`)}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(Config{Sources: tt.sources}); err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

// TestNewDefaultConfig verifies the shipped default config builds cleanly.
func TestNewDefaultConfig(t *testing.T) {
	s, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("New(DefaultConfig()): %v", err)
	}
	if len(s.sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(s.sources))
	}
}
