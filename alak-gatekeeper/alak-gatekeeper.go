package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Rule struct {
	ASN         string `json:"asn"`
	Country     string `json:"country"`
	TSP         string `json:"tsp"`
	City        string `json:"city"`
	DropPercent int    `json:"drop_percent"`
}

type Meta struct {
	ASN     string `json:"asn"`
	Country string `json:"country"`
	TSP     string `json:"tsp"`
	City    string `json:"city"`
}

var (
	ctx         = context.Background()
	redisClient *redis.Client
	alakURL     string

	requests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alak_requests_total",
			Help: "Total incoming requests by ASN, country, TSP, and city",
		},
		[]string{"asn", "country", "tsp", "city"},
	)

	drops = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alak_drops_total",
			Help: "Total dropped requests by ASN, country, TSP, and city",
		},
		[]string{"asn", "country", "tsp", "city"},
	)
)

func init() {
	prometheus.MustRegister(requests)
	prometheus.MustRegister(drops)
}

func main() {
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "localhost:6379"
	}

	alakURL = os.Getenv("ALAK_URL")
	if alakURL == "" {
		alakURL = "http://alak-geo:8081/lookup"
	}

	redisClient = redis.NewClient(&redis.Options{
		Addr: redisHost,
	})

	http.HandleFunc("/", proxyHandler)
	http.Handle("/metrics", promhttp.Handler())

	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}

	log.Printf("Alak Gatekeeper listening on :%s...", port)
	log.Printf("Using GeoIP API: %s", alakURL)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		http.Error(w, "Missing X-Forwarded-For header", http.StatusBadRequest)
		return
	}

	// Query alak-geo
	lookupURL := fmt.Sprintf("%s?ip=%s", alakURL, ip)
	resp, err := http.Get(lookupURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		log.Printf("[ERROR] GeoIP lookup failed for IP %s: %v", ip, err)
		http.Error(w, "GeoIP lookup failed", http.StatusBadRequest)
		return
	}
	defer resp.Body.Close()

	var meta Meta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		log.Printf("[ERROR] Failed to decode GeoIP response: %v", err)
		http.Error(w, "Invalid GeoIP response", http.StatusInternalServerError)
		return
	}

	if meta.ASN == "" || meta.Country == "" {
		http.Error(w, "GeoIP returned incomplete data", http.StatusBadRequest)
		return
	}

	// 🛡 Override TSP and City if headers are provided
	if tsp := r.Header.Get("X-Alak-TSP"); tsp != "" {
		meta.TSP = strings.ToLower(tsp)
	}
	if city := r.Header.Get("X-Alak-City"); city != "" {
		meta.City = strings.ToLower(city)
	}

	// 📊 Prometheus label set
	labels := prometheus.Labels{
		"asn":     meta.ASN,
		"country": meta.Country,
		"tsp":     meta.TSP,
		"city":    meta.City,
	}
	requests.With(labels).Inc()

	// 🔑 Rule key
	ruleKey := fmt.Sprintf("rule:%s:%s:%s:%s",
		meta.ASN,
		strings.ToUpper(meta.Country),
		meta.TSP,
		meta.City,
	)

	val, err := redisClient.Get(ctx, ruleKey).Result()
	if err == redis.Nil {
		log.Printf("[PASS] No rule for %s", ruleKey)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Request passed\n"))
		return
	} else if err != nil {
		log.Printf("[ERROR] Redis get error: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	var rule Rule
	if err := json.Unmarshal([]byte(val), &rule); err != nil {
		log.Printf("[ERROR] Failed to unmarshal rule: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	hash := hashIP(ip)
	log.Printf("[RULE MATCH] IP: %s ASN: %s Country: %s TSP: %s City: %s Drop%%: %d Hash: %d",
		ip, rule.ASN, rule.Country, rule.TSP, rule.City, rule.DropPercent, hash)

	if hash < rule.DropPercent {
		drops.With(labels).Inc()
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Request blocked by Alak Gatekeeper\n"))
		return
	}

	log.Printf("→ [PASS] Request allowed")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Request passed\n"))
}

// hashIP creates a stable 0–99 hash bucket from IP
func hashIP(ip string) int {
	h := fnv.New32a()
	h.Write([]byte(ip))
	return int(h.Sum32() % 100)
}
