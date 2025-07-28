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
	TTL         int    `json:"ttl"` // in seconds, optional
}

var (
	rdb *redis.Client
	ctx = context.Background()
)

func main() {
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "localhost:6379"
	}

	rdb = redis.NewClient(&redis.Options{
		Addr: redisHost,
	})

	http.HandleFunc("/rules", corsMiddleware(rulesHandler))
	http.HandleFunc("/tsp-list", corsMiddleware(tspListHandler))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Alak Controller listening on :%s...", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// CORS middleware to allow cross-origin requests
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Allow all origins for dev; restrict in production
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	}
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
		json.NewEncoder(w).Encode(rules)

	case http.MethodPost:
		var rule Rule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Normalize fields
		rule.Country = strings.ToUpper(rule.Country)
		rule.City = strings.ToLower(rule.City)
		rule.TSP = strings.ToLower(rule.TSP)

		key := "rule:" + rule.ASN + ":" + rule.Country + ":" + rule.TSP + ":" + rule.City

		data, _ := json.Marshal(rule)
		ttl := time.Duration(rule.TTL) * time.Second
		if err := rdb.Set(ctx, key, data, ttl).Err(); err != nil {
			http.Error(w, "Redis write error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("Rule stored\n"))

	case http.MethodDelete:
		asn := r.URL.Query().Get("asn")
		country := r.URL.Query().Get("country")
		tsp := r.URL.Query().Get("tsp")
		city := r.URL.Query().Get("city")

		if asn == "" || country == "" {
			http.Error(w, "asn and country required", http.StatusBadRequest)
			return
		}

		country = strings.ToUpper(country)
		tsp = strings.ToLower(tsp)
		city = strings.ToLower(city)

		key := "rule:" + asn + ":" + country + ":" + tsp + ":" + city
		rdb.Del(ctx, key)
		w.Write([]byte("Rule deleted\n"))

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
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
	json.NewEncoder(w).Encode(tsps)
}
