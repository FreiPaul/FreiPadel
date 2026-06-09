package scraper

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestSmokeFetchAll runs a live fetch against the sources in DefaultConfig.
// Run explicitly with: SMOKE=1 go test ./scraper -run Smoke -v
func TestSmokeFetchAll(t *testing.T) {
	if os.Getenv("SMOKE") != "1" {
		t.Skip("set SMOKE=1 to run the live scraper smoke test")
	}
	cfg := DefaultConfig()
	cfg.DaysAhead = 3

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	scr, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	slots, err := scr.Fetch(ctx)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	t.Logf("got %d slots", len(slots))

	// Write full results to a temporary file for inspection.
	data, err := json.MarshalIndent(slots, "", "  ")
	if err == nil {
		tmp, err := os.CreateTemp("", "scrape-results-*.json")
		if err == nil {
			_ = os.WriteFile(tmp.Name(), data, 0644)
			t.Logf("Full results written to: %s", tmp.Name())
		}
	}

	bySource := map[string]int{}
	for _, s := range slots {
		bySource[s.Source]++
	}
	t.Logf("by source: %v", bySource)
	for i, s := range slots {
		if i >= 5 {
			break
		}
		t.Logf("sample: %+v", s)
	}
}
