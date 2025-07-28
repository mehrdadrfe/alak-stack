package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/oschwald/geoip2-golang"
)

type Response struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
	ASN     string `json:"asn"`
	Org     string `json:"org"` // ISP / TSP
	TSP     string `json:"tsp"` // alias to Org
	City    string `json:"city"`
}

var (
	cityDB *geoip2.Reader
	asnDB  *geoip2.Reader
)

func main() {
	var err error

	// Load City DB
	cityDB, err = geoip2.Open("/data/GeoLite2-City.mmdb")
	if err != nil {
		log.Fatalf("failed to open city DB: %v", err)
	}
	defer cityDB.Close()

	// Load ASN DB
	asnDB, err = geoip2.Open("/data/GeoLite2-ASN.mmdb")
	if err != nil {
		log.Fatalf("failed to open ASN DB: %v", err)
	}
	defer asnDB.Close()

	port := getenv("PORT", "8081")
	http.HandleFunc("/lookup", lookupHandler)

	log.Printf("Alak Geo listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// getenv returns environment variable value or fallback default
func getenv(k, d string) string {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	return v
}

// lookupHandler handles IP geo-enrichment requests
func lookupHandler(w http.ResponseWriter, r *http.Request) {
	ipStr := r.URL.Query().Get("ip")
	if ipStr == "" {
		http.Error(w, "missing ip", http.StatusBadRequest)
		return
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		http.Error(w, "invalid ip", http.StatusBadRequest)
		return
	}

	// Look up city and ASN info
	cityRecord, cityErr := cityDB.City(ip)
	asnRecord, asnErr := asnDB.ASN(ip)

	if cityErr != nil || asnErr != nil {
		log.Printf("Geo lookup failed: cityErr=%v, asnErr=%v", cityErr, asnErr)
		http.Error(w, "Geo lookup failed", http.StatusInternalServerError)
		return
	}

	resp := Response{
		IP:      ipStr,
		Country: cityRecord.Country.IsoCode,
		ASN:     fmt.Sprintf("AS%d", asnRecord.AutonomousSystemNumber),
		Org:     asnRecord.AutonomousSystemOrganization,
		TSP:     asnRecord.AutonomousSystemOrganization,
		City:    cityRecord.City.Names["en"],
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
