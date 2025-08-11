package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

type Rule struct {
	ASN         string `json:"asn"`
	Country     string `json:"country"`
	TSP         string `json:"tsp"`
	City        string `json:"city"`
	DropPercent int    `json:"drop_percent"`
	TTL         int    `json:"ttl"` // seconds (optional)
	Enabled     bool   `json:"enabled"`
}

var (
	rdb            *redis.Client
	ctx            = context.Background()
	allowedOrigins []string
	allowAny       bool
)

func main() {
	// ---- Redis ----
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "localhost:6379"
	}
	rdb = redis.NewClient(&redis.Options{Addr: redisHost})

	// ---- CORS allow-list from env ----
	// CORS_ORIGINS="https://dash.example.com,http://localhost:3000"
	if v := strings.TrimSpace(os.Getenv("CORS_ORIGINS")); v != "" {
		allowedOrigins = splitAndTrim(v)
	} else {
		// Dev default
		allowedOrigins = []string{"http://localhost:3000"}
	}
	allowAny = len(allowedOrigins) == 1 && allowedOrigins[0] == "*"

	// ---- Routes ----
	http.HandleFunc("/health", corsMiddleware(healthHandler))
	http.HandleFunc("/rules", corsMiddleware(rulesHandler))
	http.HandleFunc("/tsp-list", corsMiddleware(tspListHandler))
	// Back-compat: some clients call /toggle-rule
	http.HandleFunc("/toggle-rule", corsMiddleware(toggleRuleHandler))
	// Safety net: catch stray preflights so they don’t 404 without CORS headers
	http.HandleFunc("/", preflightFallback)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Alak Controller listening on :%s (Redis=%s)", port, redisHost)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

/* ----------------------------- CORS helpers ----------------------------- */

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func writeCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")

	// exact-match allow-list (or reflect any if explicitly configured with "*")
	allowed := ""
	if allowAny && origin != "" {
		allowed = origin
	} else {
		for _, a := range allowedOrigins {
			if a == origin {
				allowed = origin
				break
			}
		}
	}
	if allowed != "" {
		w.Header().Set("Access-Control-Allow-Origin", allowed)
		// Only enable if you truly need credentialed requests:
		// w.Header().Set("Access-Control-Allow-Credentials", "true")
	} else if origin != "" {
		// Not allowed; respond explicitly for clarity
		w.Header().Set("Access-Control-Allow-Origin", "null")
	}

	reqHdrs := r.Header.Get("Access-Control-Request-Headers")
	if reqHdrs == "" {
		reqHdrs = "Content-Type, Authorization, X-Requested-With"
	}
	w.Header().Set("Access-Control-Allow-Headers", reqHdrs)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.Header().Set("Vary", "Origin")
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// Catch-all so stray preflights don’t 404 without CORS headers
func preflightFallback(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		writeCORS(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(w, r)
}

/* ------------------------------- Handlers ------------------------------ */

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func rulesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		keys, err := rdb.Keys(ctx, "rule:*").Result()
		if err != nil {
			http.Error(w, "Redis keys error", http.StatusInternalServerError)
			return
		}
		var rules []Rule
		for _, key := range keys {
			val, err := rdb.Get(ctx, key).Result()
			if err == nil {
				var rule Rule
				if json.Unmarshal([]byte(val), &rule) == nil {
					rules = append(rules, rule)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rules)

	case http.MethodPost:
		var rule Rule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		normalizeRule(&rule)
		key := buildRuleKey(rule)
		data, _ := json.Marshal(rule)
		ttl := time.Duration(rule.TTL) * time.Second
		if err := rdb.Set(ctx, key, data, ttl).Err(); err != nil {
			http.Error(w, "Redis write error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"msg":"Rule stored"}`))

	case http.MethodDelete:
		asn := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("asn")))
		country := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("country")))
		tsp := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tsp")))
		if asn == "" || country == "" || tsp == "" {
			http.Error(w, "asn, country, tsp required", http.StatusBadRequest)
			return
		}
		key := "rule:" + asn + ":" + country + ":" + tsp
		if err := rdb.Del(ctx, key).Err(); err != nil {
			http.Error(w, "Redis delete error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"msg":"Rule deleted"}`))

	case http.MethodPatch, http.MethodPut:
		var rule Rule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		normalizeRule(&rule)
		key := buildRuleKey(rule)

		// Preserve existing TTL on updates/toggles
		expiry := preserveOrNewTTL(key, time.Duration(rule.TTL)*time.Second)

		data, _ := json.Marshal(rule)
		if err := rdb.Set(ctx, key, data, expiry).Err(); err != nil {
			http.Error(w, "Redis write error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"msg":"Rule updated"}`))

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// Accept POST/PATCH/PUT for back-compat; toggles only `enabled`
func toggleRuleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPatch && r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type payload struct {
		ASN     string `json:"asn"`
		Country string `json:"country"`
		TSP     string `json:"tsp"`
		Enabled *bool  `json:"enabled"` // nil => invert
	}
	var p payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Normalize identifiers
	p.ASN = strings.ToUpper(strings.TrimSpace(p.ASN))
	p.Country = strings.ToUpper(strings.TrimSpace(p.Country))
	p.TSP = strings.ToLower(strings.TrimSpace(p.TSP))
	if p.ASN == "" || p.Country == "" || p.TSP == "" {
		http.Error(w, "asn, country, tsp required", http.StatusBadRequest)
		return
	}

	key := "rule:" + p.ASN + ":" + p.Country + ":" + p.TSP

	// Load existing rule
	val, err := rdb.Get(ctx, key).Result()
	if err != nil {
		http.Error(w, "Rule not found", http.StatusNotFound)
		return
	}
	var cur Rule
	if err := json.Unmarshal([]byte(val), &cur); err != nil {
		http.Error(w, "Corrupt rule JSON", http.StatusInternalServerError)
		return
	}

	// Toggle or set explicitly
	if p.Enabled != nil {
		cur.Enabled = *p.Enabled
	} else {
		cur.Enabled = !cur.Enabled
	}

	// Preserve current TTL (or use no-expire if none)
	expiry := preserveOrNewTTL(key, 0)

	data, _ := json.Marshal(cur)
	if err := rdb.Set(ctx, key, data, expiry).Err(); err != nil {
		http.Error(w, "Redis write error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"msg":     "Rule toggled",
		"enabled": cur.Enabled,
	})
}

/* ------------------------------- Helpers ------------------------------- */

func preserveOrNewTTL(key string, ifNew time.Duration) time.Duration {
	ttl, err := rdb.TTL(ctx, key).Result()
	if err != nil {
		return ifNew
	}
	switch {
	case ttl == -2: // key not found
		return ifNew
	case ttl == -1: // no expire set
		return 0
	default:
		return ttl // keep existing
	}
}

func normalizeRule(rule *Rule) {
	rule.Country = strings.ToUpper(strings.TrimSpace(rule.Country))
	rule.City = strings.ToLower(strings.TrimSpace(rule.City))
	rule.TSP = strings.ToLower(strings.TrimSpace(rule.TSP))
	rule.ASN = strings.ToUpper(strings.TrimSpace(rule.ASN))
}

func buildRuleKey(rule Rule) string {
	return "rule:" + rule.ASN + ":" + rule.Country + ":" + rule.TSP
}

func tspListHandler(w http.ResponseWriter, r *http.Request) {
	keys, err := rdb.Keys(ctx, "rule:*").Result()
	if err != nil {
		http.Error(w, "Redis error", http.StatusInternalServerError)
		return
	}
	tspSet := make(map[string]struct{})
	for _, key := range keys {
		parts := strings.Split(key, ":")
		if len(parts) >= 4 {
			tsp := parts[3]
			if tsp != "" {
				tspSet[tsp] = struct{}{}
			}
		}
	}
	var tsps []string
	for tsp := range tspSet {
		tsps = append(tsps, tsp)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tsps)
}
