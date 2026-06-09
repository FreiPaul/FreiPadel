// Package scraper fetches free padel court slots from one or more pluggable
// sources and merges them into a single list. Each source registers itself
// (see README.md); the active sources are chosen at runtime from config.json.
package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Slot is one bookable court slot, with date/time in local time (Europe/Berlin).
type Slot struct {
	Source          string  `json:"source"`
	Location        string  `json:"location"`
	Court           string  `json:"court"`
	Date            string  `json:"date"` // YYYY-MM-DD
	Time            string  `json:"time"` // HH:MM
	DurationMinutes int     `json:"duration_minutes"`
	Price           float64 `json:"price"`
	Currency        string  `json:"currency"`
}

// SourceConfig configures one scrape source. The vendor-specific settings live
// in Options as raw JSON, decoded by the vendor's Factory — so adding a vendor
// never touches this struct.
type SourceConfig struct {
	Type    string          `json:"type"` // registered vendor type, e.g. "mock"
	Options json.RawMessage `json:"options"`
}

// Source fetches free slots for one configured vendor account. Each vendor owns
// its own setup and day-looping, since vendor APIs differ.
type Source interface {
	Fetch(ctx context.Context, w Window) ([]Slot, error)
}

// Factory builds a Source from its vendor-specific JSON options.
type Factory func(opts json.RawMessage) (Source, error)

var registry = map[string]Factory{}

// Register wires a vendor type to its Factory. Vendors call this from init(),
// so a new vendor is a single new file with no edits to shared code.
func Register(typ string, f Factory) { registry[typ] = f }

// Window is the resolved, vendor-agnostic scrape window passed to every Source.
type Window struct {
	Loc       *time.Location
	Today     time.Time // time.Now().In(Loc)
	DaysAhead int
	TimeStart string // "HH:MM", inclusive lower bound on slot start
	TimeEnd   string // "HH:MM", inclusive upper bound on slot start
}

// Dates yields the YYYY-MM-DD strings to scrape, in chronological order.
func (w Window) Dates() []string {
	dates := make([]string, 0, w.DaysAhead)
	for i := 0; i < w.DaysAhead; i++ {
		dates = append(dates, w.Today.AddDate(0, 0, i).Format("2006-01-02"))
	}
	return dates
}

// Keep reports whether a slot falls inside the start-time band and is not a
// "single" court. Sources call this so filtering stays consistent across vendors.
func (w Window) Keep(s Slot) bool {
	if strings.Contains(strings.ToLower(s.Court), "single") {
		return false
	}
	return s.Time >= w.TimeStart && s.Time <= w.TimeEnd
}

type TelegramConfig struct {
	BotToken    string `json:"botToken,omitempty"`
	AdminChatID uint32 `json:"adminChatID,omitempty"`
}

// Config is the scrape-wide configuration. The scrape window is intentionally
// broad — per-user filtering (weekdays, time window, min duration) happens at
// query time in the API.
type Config struct {
	Sources   []SourceConfig `json:"sources"`
	DaysAhead int            `json:"days_ahead"`
	TimeStart string         `json:"time_start"` // earliest slot start, "HH:MM"
	TimeEnd   string         `json:"time_end"`   // latest slot start, "HH:MM"
	Timezone  string         `json:"timezone"`
	Telegram  TelegramConfig `json:"telegram"`
}

// DefaultConfig is written to config.json on first run. It enables only the
// built-in mock source, so the app runs end-to-end out of the box; swap in real
// sources by editing config.json (see README.md). The window is intentionally
// broad — per-user filtering happens at query time in the API.
func DefaultConfig() Config {
	return Config{
		Sources: []SourceConfig{
			{
				Type: "mock",
				Options: mustOpts(mockOptions{
					Location: "Mock Padel Club",
					Courts:   []string{"Court 1", "Court 2"},
					Times:    []string{"18:00", "19:30"},
					Duration: 90,
					Price:    24,
				}),
			},
		},
		DaysAhead: 21,
		TimeStart: "07:00",
		TimeEnd:   "22:00",
		Timezone:  "Europe/Berlin",
		Telegram:  TelegramConfig{},
	}
}

// LoadConfig reads the config from path, writing the default there first if
// the file does not exist yet (so it can be edited on the homelab).
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := DefaultConfig()
		out, _ := json.MarshalIndent(cfg, "", "  ")
		if werr := os.WriteFile(path, out, 0o644); werr != nil {
			return cfg, fmt.Errorf("write default config: %w", werr)
		}
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Timezone == "" {
		cfg.Timezone = "Europe/Berlin"
	}
	if cfg.DaysAhead <= 0 {
		cfg.DaysAhead = 21
	}
	return cfg, nil
}

func newClient() *http.Client {
	return &http.Client{Timeout: 25 * time.Second}
}

func setCommonHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")
}

// mustOpts marshals a vendor's typed options into raw JSON for a SourceConfig.
// Used only with literal default options, so a marshal error is a programmer bug.
func mustOpts(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal default source options: %v", err))
	}
	return b
}

// Scraper holds the sources and the operator-set collection window built once
// from a Config. The window bounds (timezone, day count, time band) come from
// config.json and only change on restart — which rebuilds the Scraper — so they
// belong here rather than being threaded through every fetch. Only "today" is
// time-dependent, so Fetch recomputes it on each call.
type Scraper struct {
	sources   []scrapeSource
	loc       *time.Location
	daysAhead int
	timeStart string
	timeEnd   string
}

type scrapeSource struct {
	typ string
	src Source
}

// New builds and validates every source up front, failing fast on an unloadable
// timezone, an unknown source type, or malformed options.
func New(cfg Config) (*Scraper, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone %q: %w", cfg.Timezone, err)
	}
	s := &Scraper{
		loc:       loc,
		daysAhead: cfg.DaysAhead,
		timeStart: cfg.TimeStart,
		timeEnd:   cfg.TimeEnd,
	}
	for _, sc := range cfg.Sources {
		factory, ok := registry[sc.Type]
		if !ok {
			return nil, fmt.Errorf("unknown source type %q", sc.Type)
		}
		src, err := factory(sc.Options)
		if err != nil {
			return nil, fmt.Errorf("source %s: %w", sc.Type, err)
		}
		s.sources = append(s.sources, scrapeSource{typ: sc.Type, src: src})
	}
	return s, nil
}

// Fetch scrapes every source and returns the combined slots. A source whose
// fetch fails is logged and skipped; an error is only returned when every
// source failed. The window's "today" is recomputed each call.
func (s *Scraper) Fetch(ctx context.Context) ([]Slot, error) {
	w := Window{
		Loc:       s.loc,
		Today:     time.Now().In(s.loc),
		DaysAhead: s.daysAhead,
		TimeStart: s.timeStart,
		TimeEnd:   s.timeEnd,
	}

	var all []Slot
	var lastErr error
	failures := 0

	for _, e := range s.sources {
		slots, err := e.src.Fetch(ctx, w)
		if err != nil {
			log.Printf("scraper: source %s failed: %v", e.typ, err)
			lastErr = err
			failures++
			continue
		}
		all = append(all, slots...)
	}

	if failures > 0 && failures == len(s.sources) {
		return nil, fmt.Errorf("all sources failed, last error: %w", lastErr)
	}
	return all, nil
}
