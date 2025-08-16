// File: alak-gatekeeper/main.go
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
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

	geoURL     string
	haProxyURL string

	// parsed upstream and global TLS flags for transport
	hapURL           *url.URL
	skipVerifyGlobal bool
	reverseProxy     *httputil.ReverseProxy
	sniOverride      = getenv("ALAK_SNI_OVERRIDE", "")

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

// ctx key to pass SNI (servername) into DialTLSContext
type sniCtxKey struct{}

func init() {
	prometheus.MustRegister(requests)
	prometheus.MustRegister(drops)
}

func main() {
	geoURL = getenv("ALAK_GEO_URL", "http://alak-geo:8081/lookup")
	haProxyURL = getenv("HA_PROXY_URL", "http://haproxy:80")

	var err error
	hapURL, err = url.Parse(haProxyURL)
	if err != nil {
		log.Fatalf("invalid HA_PROXY_URL %q: %v", haProxyURL, err)
	}

	redisHost := getenv("REDIS_HOST", "localhost:6379")
	redisClient = redis.NewClient(&redis.Options{Addr: redisHost})

	skipTLSVerify := strings.EqualFold(getenv("SKIP_TLS_VERIFY", "true"), "true")
	skipVerifyGlobal = skipTLSVerify
	if skipTLSVerify {
		log.Printf("⚠️  SKIP_TLS_VERIFY=true — backend TLS certificate verification is disabled.")
	}

	transport := newUpstreamTransport(skipTLSVerify)
	reverseProxy = newReverseProxy(transport)

	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/", proxyHandler)

	port := getenv("PORT", "8090")
	log.Printf("Alak Gatekeeper listening on :%s (upstream=%s, geo=%s, skip_verify=%v, sni_override=%q)",
		port, haProxyURL, geoURL, skipTLSVerify, sniOverride)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	// --- Client IP extraction (prefer XFF set by edge HAProxy) ---
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	}
	if ip == "" {
		log.Printf("[ERROR] No client IP found in request")
		http.Error(w, "Missing X-Forwarded-For header", http.StatusBadRequest)
		return
	}

	// --- Geo lookup (fail-open) ---
	lookupURL := fmt.Sprintf("%s?ip=%s", geoURL, ip)
	resp, err := http.Get(lookupURL)
	if err != nil {
		log.Printf("[FAIL-OPEN] GeoIP lookup error for IP %s: %v; allowing request", ip, err)
		reverseProxy.ServeHTTP(w, r.WithContext(withSNI(r.Context(), desiredSNI(r))))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		log.Printf("[PASS] No GeoIP data for IP %s", ip)
		reverseProxy.ServeHTTP(w, r.WithContext(withSNI(r.Context(), desiredSNI(r))))
		return
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("[FAIL-OPEN] GeoIP lookup failed for IP %s: status %d; allowing request", ip, resp.StatusCode)
		reverseProxy.ServeHTTP(w, r.WithContext(withSNI(r.Context(), desiredSNI(r))))
		return
	}

	var meta Meta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		log.Printf("[FAIL-OPEN] Failed to decode GeoIP response for IP %s: %v; allowing request", ip, err)
		reverseProxy.ServeHTTP(w, r.WithContext(withSNI(r.Context(), desiredSNI(r))))
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
			reverseProxy.ServeHTTP(w, r.WithContext(withSNI(r.Context(), desiredSNI(r))))
			return
		}
		if err := json.Unmarshal([]byte(val), &rule); err != nil {
			log.Printf("[FAIL-OPEN] Failed to unmarshal rule at %s: %v; allowing request", key, err)
			reverseProxy.ServeHTTP(w, r.WithContext(withSNI(r.Context(), desiredSNI(r))))
			return
		}
		found = true
		bestKey = key
		break
	}

	if !found {
		log.Printf("[PASS] No matching rule for IP=%s ASN=%q Country=%q TSP=%q", ip, meta.ASN, meta.Country, meta.TSP)
		reverseProxy.ServeHTTP(w, r.WithContext(withSNI(r.Context(), desiredSNI(r))))
		return
	}

	log.Printf("[RULE MATCH] key=%s IP=%s ASN=%q Country=%q TSP=%q Drop%%=%d Enabled=%v Hash=%d",
		bestKey, ip, rule.ASN, rule.Country, rule.TSP, rule.DropPercent, rule.Enabled, hashIP(ip))

	if !rule.Enabled {
		log.Printf("[PASS] Rule disabled for ASN=%q Country=%q TSP=%q", rule.ASN, rule.Country, rule.TSP)
		reverseProxy.ServeHTTP(w, r.WithContext(withSNI(r.Context(), desiredSNI(r))))
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
	reverseProxy.ServeHTTP(w, r.WithContext(withSNI(r.Context(), desiredSNI(r))))
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

// ---- Reverse proxy (long-term solution) ----

func newReverseProxy(tr *http.Transport) *httputil.ReverseProxy {
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// Upstream target: edge HAProxy (scheme+host from HA_PROXY_URL)
			req.URL.Scheme = hapURL.Scheme
			req.URL.Host = hapURL.Host
			// Keep origin-form path/query as sent by the client
			// (ReverseProxy will clear RequestURI for us)

			// Preserve Host for Ingress host-based routing (and for SNI via context)
			cleanHost := desiredSNI(req)
			req.Host = cleanHost
			req.Header.Set("Host", cleanHost)

			// Forward proto/host hints
			if req.TLS != nil {
				req.Header.Set("X-Forwarded-Proto", "https")
			} else {
				req.Header.Set("X-Forwarded-Proto", "http")
			}
			req.Header.Set("X-Forwarded-Host", cleanHost)

			// Let ReverseProxy append X-Forwarded-For; ensure existing chain remains
			// (no change needed; it preserves existing header and appends RemoteAddr)

			// Inject per-request SNI for upstream TLS handshakes
			ctx := withSNI(req.Context(), cleanHost)
			*req = *req.WithContext(ctx)
		},
		Transport: tr,
		ErrorLog:  log.New(os.Stdout, "[reverse-proxy] ", log.LstdFlags),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("[PROXY ERROR] %s %s: %v", r.Method, r.URL.String(), err)
			http.Error(w, "Upstream error", http.StatusBadGateway)
		},
	}
	return rp
}

// Build an upstream transport that:
// - disables HTTP/2 (WebSocket Upgrade stays on HTTP/1.1)
// - injects SNI per request via context
// - has no overall request timeout (long-lived WS)
// - sets conservative, sane dial/idle timeouts
func newUpstreamTransport(skipVerify bool) *http.Transport {
	baseTLS := &tls.Config{
		InsecureSkipVerify: skipVerify,           // set false when proper CA is mounted
		NextProtos:         []string{"http/1.1"}, // advertise h1 only
		MinVersion:         tls.VersionTLS12,
	}

	dialer := &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 60 * time.Second,
	}

	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         dialer.DialContext,
		ForceAttemptHTTP2:   false,                                                  // disable h2
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{}, // no h2
		MaxIdleConns:        512,
		IdleConnTimeout:     120 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
		// ResponseHeaderTimeout: applies only to headers. Keep modest to not hang handshakes:
		ResponseHeaderTimeout: 15 * time.Second,
		// DisableCompression: false (fine; WS frames are not affected)
	}

	// Per-request SNI injection for TLS
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
		if err := tlsConn.Handshake(); err != nil {
			_ = raw.Close()
			return nil, fmt.Errorf("tls handshake to %s with SNI=%q failed: %w", addr, serverName, err)
		}
		return tlsConn, nil
	}

	return tr
}

func withSNI(ctx context.Context, sni string) context.Context {
	return context.WithValue(ctx, sniCtxKey{}, sni)
}

func desiredSNI(r *http.Request) string {
	cleanHost := hostNoPort(r.Host)
	if sniOverride != "" {
		cleanHost = sniOverride
	}
	return cleanHost
}

// ---- utils ----

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
