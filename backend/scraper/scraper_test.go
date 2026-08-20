package scraper

import (
	"context"
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

// TestNormalizeLocationName pins the canonical form of a venue name. The saved
// location filter is compared to stored slot locations by exact string equality,
// so any change here silently invalidates users' existing filters.
func TestNormalizeLocationName(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"already clean is untouched", "Padel Club Freiburg", "Padel Club Freiburg"},
		{"surrounding whitespace trimmed", "  Padel Club Freiburg\t", "Padel Club Freiburg"},
		{"internal run collapsed", "Padel  Club   Freiburg", "Padel Club Freiburg"},
		{"newlines and tabs are whitespace", "Padel\nClub\tFreiburg", "Padel Club Freiburg"},
		{"case is preserved", "PADEL club FreiBurg", "PADEL club FreiBurg"},
		{"punctuation is preserved", "Padel-Club Freiburg e.V.", "Padel-Club Freiburg e.V."},
		{"empty stays empty", "", ""},
		{"whitespace only becomes empty", "   \t\n ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeLocationName(tt.in)
			if got != tt.want {
				t.Fatalf("NormalizeLocationName(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if again := NormalizeLocationName(got); again != got {
				t.Fatalf("not idempotent: %q -> %q", got, again)
			}
		})
	}
}

// TestFetchNormalizesLocations verifies Fetch is the choke point, so a source
// that returns an untidy venue name cannot desync it from the location filter.
func TestFetchNormalizesLocations(t *testing.T) {
	Register("untidy", func(json.RawMessage) (Source, error) {
		return sourceFunc(func(context.Context, Window) ([]Slot, error) {
			return []Slot{{Location: "  Padel  Club\tFreiburg ", Court: "Court 1", Time: "19:00"}}, nil
		}), nil
	})
	s, err := New(Config{Sources: []SourceConfig{{Type: "untidy", Options: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	slots, err := s.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(slots) != 1 {
		t.Fatalf("expected 1 slot, got %d", len(slots))
	}
	if got, want := slots[0].Location, "Padel Club Freiburg"; got != want {
		t.Fatalf("Fetch left location %q, want %q", got, want)
	}
}

// sourceFunc adapts a plain function to the Source interface.
type sourceFunc func(ctx context.Context, w Window) ([]Slot, error)

func (f sourceFunc) Fetch(ctx context.Context, w Window) ([]Slot, error) { return f(ctx, w) }
