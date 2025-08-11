// File: alak-gatekeeper/main.go
package main

import (
	"context"
	"crypto/tls"
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
	proxyClient *http.Client

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

	sniOverride = getenv("ALAK_SNI_OVERRIDE", "")
)

// ctx key to pass SNI (servername) into DialTLSContext
type sniCtxKey struct{}

func init() {
	prometheus.MustRegister(requests)
	prometheus.MustRegister(drops)
}

func main() {
	geoURL = getenv("ALAK_GEO_URL", "http://alak-geo:8081/lookup")
	haProxyURL = getenv("HA_PROXY_URL", "http://haproxy:80")

	redisHost := getenv("REDIS_HOST", "localhost:6379")
	redisClient = redis.NewClient(&redis.Options{Addr: redisHost})

	skipTLSVerify := strings.EqualFold(getenv("SKIP_TLS_VERIFY", "true"), "true")
	if skipTLSVerify {
		log.Printf("⚠️  SKIP_TLS_VERIFY=true — backend TLS certificate verification is disabled.")
	}

	proxyClient = newProxyClient(skipTLSVerify)

	http.HandleFunc("/", proxyHandler)
	http.Handle("/metrics", promhttp.Handler())

	port := getenv("PORT", "8090")
	log.Printf("Alak Gatekeeper listening on :%s (upstream=%s, geo=%s, skip_verify=%v, sni_override=%q)",
		port, haProxyURL, geoURL, skipTLSVerify, sniOverride)
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

	meta.ASN = cleanField(meta.ASN, false)
	meta.Country = cleanField(meta.Country, true)
	meta.TSP = cleanField(meta.TSP, false)

	labels := prometheus.Labels{
		"asn":     meta.ASN,
		"country": meta.Country,
		"tsp":     meta.TSP,
	}
	requests.With(labels).Inc()

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
		_, _ = w.Write([]byte("Request blocked by Alak Gatekeeper\n"))
		return
	}

	log.Printf("[PASS] Request allowed for IP %s", ip)
	proxyToHAProxy(w, r, ip)
}

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
	if !asnSet && countrySet {
		keys = append(keys, fmt.Sprintf("rule:*:%s:*", meta.Country))
	}
	keys = append(keys, "rule:*:*:*")
	return keys
}

func proxyToHAProxy(w http.ResponseWriter, r *http.Request, clientIP string) {
	// Build upstream URL by concatenating base (scheme+host[:port]) with the original path + query
	upURL := haProxyURL + r.URL.RequestURI()

	req, err := http.NewRequest(r.Method, upURL, r.Body)
	if err != nil {
		log.Printf("[PROXY ERROR] newRequest(%s): %v", upURL, err)
		http.Error(w, "Proxy error", http.StatusInternalServerError)
		return
	}

	// Copy all headers as-is
	for h, vals := range r.Header {
		for _, v := range vals {
			req.Header.Add(h, v)
		}
	}

	// Determine clean host for Ingress routing & SNI (no :port)
	cleanHost := hostNoPort(r.Host)
	if sniOverride != "" {
		cleanHost = sniOverride
	}

	// Preserve Host for Ingress host-based routing
	req.Host = cleanHost
	req.Header.Set("Host", cleanHost)

	// Forward proto/host hints
	if r.TLS != nil {
		req.Header.Set("X-Forwarded-Proto", "https")
	} else {
		req.Header.Set("X-Forwarded-Proto", "http")
	}
	req.Header.Set("X-Forwarded-Host", cleanHost)

	// XFF chain
	existing := r.Header.Get("X-Forwarded-For")
	if existing != "" && !strings.Contains(existing, clientIP) {
		req.Header.Set("X-Forwarded-For", existing+", "+clientIP)
	} else if existing == "" {
		req.Header.Set("X-Forwarded-For", clientIP)
	}

	// Use a fresh, bounded context so upstream isn’t killed by client jitter
	ctxUp, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctxUp = context.WithValue(ctxUp, sniCtxKey{}, cleanHost)
	req = req.WithContext(ctxUp)

	log.Printf("[PROXY → INGRESS] url=%s host=%q sni=%q method=%s", upURL, cleanHost, cleanHost, r.Method)

	resp, err := proxyClient.Do(req)
	if err != nil {
		log.Printf("[PROXY ERROR] client.Do url=%s host=%q sni=%q: %v", upURL, cleanHost, cleanHost, err)
		http.Error(w, "Upstream HAProxy error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func hashIP(ip string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(ip))
	return int(h.Sum32() % 100)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func hostNoPort(h string) string {
	if h == "" {
		return h
	}
	if nh, _, err := net.SplitHostPort(h); err == nil && nh != "" {
		return nh
	}
	return h
}

// --- HTTP client with per-request SNI injection, h2 disabled, and no redirect follow ---

func newProxyClient(skipVerify bool) *http.Client {
	baseTLS := &tls.Config{
		InsecureSkipVerify: skipVerify,           // set to false once CA is mounted
		NextProtos:         []string{"http/1.1"}, // advertise h1 only
		MinVersion:         tls.VersionTLS12,
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 60 * time.Second}

	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     false,                                                  // disable h2 to avoid cancel-on-upgrade quirks
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{}, // no h2
		MaxIdleConns:          512,
		IdleConnTimeout:       120 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
	}

	// Inject SNI (servername) from request context, then do TLS handshake
	tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		raw, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		serverName := ""
		if v := ctx.Value(sniCtxKey{}); v != nil {
			if s, ok := v.(string); ok {
				serverName = s
			}
		}
		if serverName == "" {
			if host, _, _ := net.SplitHostPort(addr); host != "" {
				serverName = host
			}
		}

		cfg := baseTLS.Clone()
		cfg.ServerName = serverName

		tlsConn := tls.Client(raw, cfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, fmt.Errorf("tls handshake to %s with SNI=%q failed: %w", addr, serverName, err)
		}
		return tlsConn, nil
	}

	return &http.Client{
		Transport: tr,
		Timeout:   25 * time.Second,
		// Critical for browser-driven auth flows: do NOT follow upstream redirects.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
