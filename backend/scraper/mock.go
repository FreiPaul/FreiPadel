package scraper

import (
	"context"
	"encoding/json"
)

// The mock source is a self-contained reference connector: it generates a few
// synthetic slots without touching the network. It exists so the app can run
// end-to-end out of the box and as a worked example of the Source contract —
// see README.md. Like every vendor it self-registers here, but it only runs if
// config.json explicitly lists a source of {"type": "mock"}.
func init() { Register("mock", newMockSource) }

// mockOptions are the (all optional) settings for a mock source. Sensible
// defaults are filled in by newMockSource, so `{"type": "mock"}` alone works.
type mockOptions struct {
	Location string   `json:"location"`         // club name shown on slots
	Courts   []string `json:"courts"`           // court names to emit per time
	Times    []string `json:"times"`            // slot start times, "HH:MM"
	Duration int      `json:"duration_minutes"` // slot length
	Price    float64  `json:"price"`            // price per slot
}

type mockSource struct {
	opts mockOptions
}

func newMockSource(raw json.RawMessage) (Source, error) {
	var o mockOptions
	// Empty/absent options are fine for the mock — unlike real vendors it has
	// no required fields. Only malformed JSON is an error.
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, err
		}
	}
	if o.Location == "" {
		o.Location = "Mock Padel Club"
	}
	if len(o.Courts) == 0 {
		o.Courts = []string{"Court 1", "Court 2"}
	}
	if len(o.Times) == 0 {
		o.Times = []string{"18:00", "19:30"}
	}
	if o.Duration == 0 {
		o.Duration = 90
	}
	if o.Price == 0 {
		o.Price = 24
	}
	return &mockSource{opts: o}, nil
}

// Fetch emits one slot per (date, time, court) across the window. It loops the
// same window helpers (w.Dates) and reuses w.Keep for filtering, exactly like a
// real source — only the data is invented instead of fetched.
func (m *mockSource) Fetch(_ context.Context, w Window) ([]Slot, error) {
	var slots []Slot
	for _, date := range w.Dates() {
		for _, t := range m.opts.Times {
			for _, court := range m.opts.Courts {
				slot := Slot{
					Source:          "mock",
					Location:        m.opts.Location,
					Court:           court,
					Date:            date,
					Time:            t,
					DurationMinutes: m.opts.Duration,
					Price:           m.opts.Price,
					Currency:        "EUR",
				}
				if w.Keep(slot) {
					slots = append(slots, slot)
				}
			}
		}
	}
	return slots, nil
}
