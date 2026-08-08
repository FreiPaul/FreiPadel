package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // so Europe/Berlin works in scratch/alpine containers

	"freipadel/emailer"
	"freipadel/internal/store"
	"freipadel/scraper"
	"freipadel/telegram"
	"gorm.io/gorm"
)

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type App struct {
	store         *store.Store
	orm           *gorm.DB
	scrapeCfg     scraper.Config
	scraper       *scraper.Scraper
	tz            *time.Location
	secureCookies bool

	mu         sync.Mutex
	scraping   bool
	lastScrape time.Time

	hub *syncHub

	telegramSender telegram.TelegramSender
	emailer        *emailer.Emailer
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	// Admin CLI mode: `freipadel <command> ...` (e.g. via docker exec).
	if len(os.Args) > 1 {
		runCLI(os.Args[1:])
		return
	}

	dataDir := envOr("DATA_DIR", "./data")
	staticDir := envOr("STATIC_DIR", "./static")
	port := envOr("PORT", "8080")

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	storage, err := store.Open(filepath.Join(dataDir, "freipadel.db"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	scrapeCfg, err := scraper.LoadConfig(filepath.Join(dataDir, "config.json"))
	if err != nil {
		log.Fatalf("load scraper config: %v", err)
	}
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		scrapeCfg.Telegram.BotToken = token
	}
	if chatID := os.Getenv("TELEGRAM_ADMIN_CHAT_ID"); chatID != "" {
		parsed, err := strconv.ParseUint(chatID, 10, 32)
		if err != nil {
			log.Fatalf("parse TELEGRAM_ADMIN_CHAT_ID: %v", err)
		}
		scrapeCfg.Telegram.AdminChatID = uint32(parsed)
	}
	tz, err := time.LoadLocation(scrapeCfg.Timezone)
	if err != nil {
		log.Fatalf("load timezone: %v", err)
	}
	scr, err := scraper.New(scrapeCfg)
	if err != nil {
		log.Fatalf("init scraper: %v", err)
	}

	app := &App{
		store:          storage,
		orm:            storage.ORM,
		scrapeCfg:      scrapeCfg,
		scraper:        scr,
		tz:             tz,
		secureCookies:  os.Getenv("COOKIE_SECURE") == "1",
		telegramSender: *telegram.NewSender(scrapeCfg.Telegram.BotToken),
		emailer:        emailer.FromEnv(),
	}
	if app.emailer.Configured() {
		log.Printf("SMTP emailer configured")
	} else {
		log.Printf("SMTP emailer not configured (set SMTP_HOST/SMTP_USER/SMTP_PASS and EMAILER_ENABLED=\"1\")")
	}
	app.hub = newSyncHub(storage.ORM)

	// Background scrape loop.
	intervalMin, _ := strconv.Atoi(envOr("SCRAPE_INTERVAL_MINUTES", "30"))
	if intervalMin < 5 {
		intervalMin = 5
	}
	go app.scrapeLoop(time.Duration(intervalMin) * time.Minute)

	// Hourly session cleanup + sync log compaction.
	go func() {
		for {
			_ = store.DeleteExpiredSessions(storage.ORM)
			app.compactSyncLog()
			time.Sleep(time.Hour)
		}
	}()

	mux := http.NewServeMux()

	// Auth
	mux.HandleFunc("GET /api/auth/setup", app.handleSetup)
	mux.HandleFunc("POST /api/auth/register", app.handleRegister)
	mux.HandleFunc("POST /api/auth/login", app.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", app.handleLogout)
	mux.HandleFunc("GET /api/auth/me", app.requireAuth(app.handleMe))

	// Settings
	mux.HandleFunc("GET /api/settings", app.requireAuth(app.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", app.requireAuth(app.handlePutSettings))

	// Slots
	mux.HandleFunc("GET /api/slots", app.requireAuth(app.handleGetSlots))
	mux.HandleFunc("POST /api/slots/refresh", app.requireAuth(app.handleRefreshSlots))
	mux.HandleFunc("GET /api/locations", app.requireAuth(app.handleListLocations))

	// Polls
	mux.HandleFunc("GET /api/polls", app.requireAuth(app.handleListPolls))
	mux.HandleFunc("POST /api/polls", app.requireAuth(app.handleCreatePoll))
	mux.HandleFunc("POST /api/polls/{id}/vote", app.requireAuth(app.handleVote))
	mux.HandleFunc("POST /api/polls/{id}/close", app.requireAuth(app.handleClosePoll))
	mux.HandleFunc("DELETE /api/polls/{id}", app.requireAuth(app.handleDeletePoll))

	// Members
	mux.HandleFunc("GET /api/users", app.requireAuth(app.handleListUsers))

	// Sync engine (bootstrap snapshot + SSE delta stream)
	mux.HandleFunc("GET /api/sync/bootstrap", app.requireAuth(app.handleSyncBootstrap))
	mux.HandleFunc("GET /api/sync/events", app.requireAuth(app.handleSyncEvents))

	// Invites
	mux.HandleFunc("GET /api/invites/{token}/check", app.handleCheckInvite)
	mux.HandleFunc("GET /api/invites", app.requireAdmin(app.handleListInvites))
	mux.HandleFunc("POST /api/invites", app.requireAdmin(app.handleCreateInvite))
	mux.HandleFunc("POST /api/invites/{token}/disable", app.requireAdmin(app.handleDisableInvite))
	mux.HandleFunc("DELETE /api/invites/{token}", app.requireAdmin(app.handleDeleteInvite))

	// Unknown API routes must not fall through to the SPA.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpError(w, http.StatusNotFound, "not found")
	})

	// Frontend (static SPA with index.html fallback for client-side routes).
	mux.Handle("/", spaHandler(staticDir))

	log.Printf("FreiPadel listening on :%s (data: %s, static: %s)", port, dataDir, staticDir)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

// spaHandler serves files from dir, falling back to index.html for
// client-side routes like /polls or /register.
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

// --- Scraping ---

func (a *App) isScraping() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.scraping
}

// triggerScrape starts a scrape in the background unless one is already
// running or the last one finished less than a minute ago.
func (a *App) triggerScrape() bool {
	a.mu.Lock()
	if a.scraping || time.Since(a.lastScrape) < time.Minute {
		a.mu.Unlock()
		return false
	}
	a.scraping = true
	a.mu.Unlock()
	a.hub.broadcastEphemeral("scrape", "status", map[string]bool{"scraping": true})

	go func() {
		defer func() {
			a.mu.Lock()
			a.scraping = false
			a.lastScrape = time.Now()
			a.mu.Unlock()
			a.hub.broadcastEphemeral("scrape", "status", map[string]bool{"scraping": false})
		}()
		a.runScrape()
	}()
	return true
}

func (a *App) scrapeLoop(interval time.Duration) {
	// on startup: respect last_fetched_at from database
	lastScraped := a.store.GetMeta("last_fetched_at")
	lastScrapedTime, err := time.Parse(time.RFC3339, lastScraped)
	if err == nil {
		a.mu.Lock()
		a.lastScrape = lastScrapedTime
		a.mu.Unlock()
		sinceLastScrape := time.Since(lastScrapedTime)
		if sinceLastScrape > 0 && sinceLastScrape < interval {
			log.Printf("waiting %s until next scrape", (interval - sinceLastScrape).Round(time.Second))
			time.Sleep(interval - sinceLastScrape)
		}
	}

	for {
		a.triggerScrape()
		time.Sleep(interval)
	}
}

func (a *App) runScrape() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	start := time.Now()
	slots, err := a.scraper.Fetch(ctx)
	if err != nil {
		log.Printf("scrape failed: %v", err)
		return
	}

	keySet := map[string]bool{}
	keys := []string{}
	records := make([]store.SlotRecord, 0, len(slots))
	for _, s := range slots {
		if strings.Contains(strings.ToLower(s.Court), "single") {
			continue
		}
		records = append(records, store.SlotRecord{
			Source: s.Source, Location: s.Location, Court: s.Court, Date: s.Date, Time: s.Time,
			DurationMinutes: s.DurationMinutes, Price: s.Price, Currency: s.Currency,
		})
		if k := slotKey(s.Date, s.Time, s.DurationMinutes, s.Location); !keySet[k] {
			keySet[k] = true
			keys = append(keys, k)
		}
	}
	fetchedAt := time.Now().In(a.tz).Format(time.RFC3339)
	err = a.orm.Transaction(func(tx *gorm.DB) error {
		if err := store.ReplaceSlots(tx, records); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"keys": keys, "last_fetched_at": fetchedAt})
		return store.AppendSync(tx, "slots", "snapshot", "upsert", payload, 0)
	})
	if err != nil {
		log.Printf("scrape store: %v", err)
		return
	}
	_ = a.store.SetMeta("last_fetched_at", fetchedAt)
	a.hub.notify()
	log.Printf("scrape done: %d slots in %s", len(slots), time.Since(start).Round(time.Millisecond))
}
