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
	geoURL = os.Getenv("ALAK_GEO_URL")
	if geoURL == "" {
		geoURL = "http://alak-geo:8081/lookup"
	}
	haProxyURL = os.Getenv("HA_PROXY_URL")
	if haProxyURL == "" {
		haProxyURL = "http://haproxy:80"
	}

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

	if resp.StatusCode == http.StatusNotFound {
		log.Printf("[PASS] No GeoIP data for IP %s", ip)
		proxyToHAProxy(w, r, ip)
		return
	}
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

	if meta.ASN == "" || meta.Country == "" {
		log.Printf("[PASS] Incomplete GeoIP data for IP %s", ip)
		proxyToHAProxy(w, r, ip)
		return
	}

	labels := prometheus.Labels{
		"asn":     meta.ASN,
		"country": meta.Country,
		"tsp":     meta.TSP,
	}
	requests.With(labels).Inc()

	keys := buildRuleKeys(meta)

	var rule Rule
	found := false
	for _, key := range keys {
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
		break
	}

	if !found {
		log.Printf("[PASS] No matching rule for %s", meta.ASN)
		proxyToHAProxy(w, r, ip)
		return
	}

	if !rule.Enabled {
		log.Printf("[PASS] Rule disabled for %s", meta.ASN)
		proxyToHAProxy(w, r, ip)
		return
	}

	hash := hashIP(ip)
	log.Printf("[RULE MATCH] IP=%s ASN=%s Country=%s TSP=%s Drop%%=%d Enabled=%v Hash=%d",
		ip, rule.ASN, rule.Country, rule.TSP, rule.DropPercent, rule.Enabled, hash)

	if hash < rule.DropPercent {
		drops.With(labels).Inc()
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Request blocked by Alak Gatekeeper\n"))
		return
	}

	log.Printf("[PASS] Request allowed for IP %s", ip)
	proxyToHAProxy(w, r, ip)
}

// Helper to build rule keys (business logic unchanged)
func buildRuleKeys(meta Meta) []string {
	var keys []string
	asnSet := meta.ASN != "" && meta.TSP != ""
	countrySet := meta.Country != ""

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
	if countrySet {
		keys = append(keys, fmt.Sprintf("rule:*:%s:*", meta.Country))
	}
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

	for h, vals := range r.Header {
		if strings.ToLower(h) == "host" {
			continue
		}
		for _, v := range vals {
			req.Header.Add(h, v)
		}
	}
	existing := r.Header.Get("X-Forwarded-For")
	if existing != "" && !strings.Contains(existing, clientIP) {
		req.Header.Set("X-Forwarded-For", existing+", "+clientIP)
	} else if existing == "" {
		req.Header.Set("X-Forwarded-For", clientIP)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[PROXY ERROR] client.Do: %v", err)
		http.Error(w, "Upstream HAProxy error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = http.MaxBytesReader(w, resp.Body, 10<<20).WriteTo(w)
}

func hashIP(ip string) int {
	h := fnv.New32a()
	h.Write([]byte(ip))
	return int(h.Sum32() % 100)
}
