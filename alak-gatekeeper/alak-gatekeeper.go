// File: alak-gatekeeper/main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Rule struct {
	ASN         string `json:"asn"`
	Country     string `json:"country"`
	TSP         string `json:"tsp"`
	DropPercent int    `json:"drop_percent"`
	TTL         int    `json:"ttl"`
	Enabled     bool   `json:"enabled"`
}

type Meta struct {
	ASN     string `json:"asn"`
	Country string `json:"country"`
	TSP     string `json:"tsp"`
	City    string `json:"city"`
}

// Normalization: trim, treat "-" as empty, uppercase country.
func cleanField(s string, isCountry bool) string {
	s = strings.TrimSpace(s)
	if s == "-" {
		s = ""
	}
	if isCountry {
		s = strings.ToUpper(s)
	}
	return s
}

var (
	ctx         = context.Background()
	redisClient *redis.Client
	geoURL      string
	haProxyURL  string

	requests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alak_requests_total",
			Help: "Total incoming requests by ASN, country, and TSP",
		},
		[]string{"asn", "country", "tsp"},
	)
	drops = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alak_drops_total",
			Help: "Total dropped requests by ASN, country, and TSP",
		},
		[]string{"asn", "country", "tsp"},
	)
)

func init() {
	prometheus.MustRegister(requests)
	prometheus.MustRegister(drops)
}

func main() {
	// GeoIP service URL
	geoURL = os.Getenv("ALAK_GEO_URL")
	if geoURL == "" {
		geoURL = "http://alak-geo:8081/lookup"
	}
	haProxyURL = os.Getenv("HA_PROXY_URL")
	if haProxyURL == "" {
		haProxyURL = "http://haproxy:80"
	}

	// Redis setup
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "localhost:6379"
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
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	}
	if ip == "" {
		log.Printf("[ERROR] No client IP found in request")
		http.Error(w, "Missing X-Forwarded-For header", http.StatusBadRequest)
		return
	}

	lookupURL := fmt.Sprintf("%s?ip=%s", geoURL, ip)
	resp, err := http.Get(lookupURL)
	if err != nil {
		log.Printf("[FAIL-OPEN] GeoIP lookup error for IP %s: %v; allowing request", ip, err)
		proxyToHAProxy(w, r, ip)
		return
	}
	defer resp.Body.Close()

	// 404 → no data → pass
	if resp.StatusCode == http.StatusNotFound {
		log.Printf("[PASS] No GeoIP data for IP %s", ip)
		proxyToHAProxy(w, r, ip)
		return
	}
	// other non-200 → error, fail open
	if resp.StatusCode != http.StatusOK {
		log.Printf("[FAIL-OPEN] GeoIP lookup failed for IP %s: status %d; allowing request", ip, resp.StatusCode)
		proxyToHAProxy(w, r, ip)
		return
	}

	var meta Meta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		log.Printf("[FAIL-OPEN] Failed to decode GeoIP response for IP %s: %v; allowing request", ip, err)
		proxyToHAProxy(w, r, ip)
		return
	}

	// Normalize fields
	meta.ASN = cleanField(meta.ASN, false)
	meta.Country = cleanField(meta.Country, true)
	meta.TSP = cleanField(meta.TSP, false)

	labels := prometheus.Labels{
		"asn":     meta.ASN,
		"country": meta.Country,
		"tsp":     meta.TSP,
	}
	requests.With(labels).Inc()

	// Build rule keys by your business invariant and with normalization
	ruleKeys := buildRuleKeys(meta)
	log.Printf("[DEBUG] IP=%s ASN=%q Country=%q TSP=%q; Keys checked: %v", ip, meta.ASN, meta.Country, meta.TSP, ruleKeys)

	var (
		found   bool
		rule    Rule
		bestKey string
	)
	for _, key := range ruleKeys {
		val, err := redisClient.Get(ctx, key).Result()
		if err == redis.Nil {
			continue
		} else if err != nil {
			log.Printf("[FAIL-OPEN] Redis get error: %v; allowing request", err)
			proxyToHAProxy(w, r, ip)
			return
		}
		if err := json.Unmarshal([]byte(val), &rule); err != nil {
			log.Printf("[FAIL-OPEN] Failed to unmarshal rule at %s: %v; allowing request", key, err)
			proxyToHAProxy(w, r, ip)
			return
		}
		found = true
		bestKey = key
		break
	}

	if !found {
		log.Printf("[PASS] No matching rule for IP=%s ASN=%q Country=%q TSP=%q", ip, meta.ASN, meta.Country, meta.TSP)
		proxyToHAProxy(w, r, ip)
		return
	}

	log.Printf("[RULE MATCH] key=%s IP=%s ASN=%q Country=%q TSP=%q Drop%%=%d Enabled=%v Hash=%d",
		bestKey, ip, rule.ASN, rule.Country, rule.TSP, rule.DropPercent, rule.Enabled, hashIP(ip))

	if !rule.Enabled {
		log.Printf("[PASS] Rule disabled for ASN=%q Country=%q TSP=%q", rule.ASN, rule.Country, rule.TSP)
		proxyToHAProxy(w, r, ip)
		return
	}

	hash := hashIP(ip)
	if hash < rule.DropPercent {
		drops.With(labels).Inc()
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Request blocked by Alak Gatekeeper\n"))
		return
	}

	log.Printf("[PASS] Request allowed for IP %s", ip)
	proxyToHAProxy(w, r, ip)
}

// Only build keys that can exist given the ASN+TSP always-present invariant, with normalization
func buildRuleKeys(meta Meta) []string {
	var keys []string
	asnSet := meta.ASN != "" && meta.TSP != ""
	countrySet := meta.Country != ""

	// ASN+TSP present
	if asnSet {
		if countrySet {
			keys = append(keys, fmt.Sprintf("rule:%s:%s:%s", meta.ASN, meta.Country, meta.TSP))
			keys = append(keys, fmt.Sprintf("rule:%s:%s:*", meta.ASN, meta.Country))
			keys = append(keys, fmt.Sprintf("rule:%s:*:%s", meta.ASN, meta.TSP))
			keys = append(keys, fmt.Sprintf("rule:%s:*:*", meta.ASN))
		} else {
			keys = append(keys, fmt.Sprintf("rule:%s:*:%s", meta.ASN, meta.TSP))
			keys = append(keys, fmt.Sprintf("rule:%s:*:*", meta.ASN))
		}
	}
	// ASN/TSP missing, country present
	if !asnSet && countrySet {
		keys = append(keys, fmt.Sprintf("rule:*:%s:*", meta.Country))
	}
	// Global catch-all
	keys = append(keys, "rule:*:*:*")
	return keys
}

func proxyToHAProxy(w http.ResponseWriter, r *http.Request, clientIP string) {
	proxyURL := haProxyURL + r.URL.RequestURI()

	req, err := http.NewRequest(r.Method, proxyURL, r.Body)
	if err != nil {
		log.Printf("[PROXY ERROR] newRequest: %v", err)
		http.Error(w, "Proxy error", http.StatusInternalServerError)
		return
	}

	// Copy all headers except Host (handled by Go)
	for h, vals := range r.Header {
		if strings.ToLower(h) == "host" {
			continue
		}
		for _, v := range vals {
			req.Header.Add(h, v)
		}
	}

	// Set or append X-Forwarded-For
	existing := r.Header.Get("X-Forwarded-For")
	if existing != "" && !strings.Contains(existing, clientIP) {
		req.Header.Set("X-Forwarded-For", existing+", "+clientIP)
	} else if existing == "" {
		req.Header.Set("X-Forwarded-For", clientIP)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[PROXY ERROR] client.Do: %v", err)
		http.Error(w, "Upstream HAProxy error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy headers from HAProxy response
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func hashIP(ip string) int {
	h := fnv.New32a()
	h.Write([]byte(ip))
	return int(h.Sum32() % 100)
}
