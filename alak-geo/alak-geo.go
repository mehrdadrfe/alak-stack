package main

import (
	"encoding/csv"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/oschwald/geoip2-golang"
)

type LookupResponse struct {
	ASN     string `json:"asn"`
	Country string `json:"country"`
	TSP     string `json:"tsp"`
	City    string `json:"city"`
}

var (
	cityDB        *geoip2.Reader
	asnDB         *geoip2.Reader
	tspMap        map[string]string
	asnMap        map[string]LookupResponse
	asnCountryMap map[string]string
)

func main() {
	var err error
	cityDB, err = geoip2.Open("/data/GeoLite2-City.mmdb")
	if err != nil {
		log.Fatalf("failed to open City DB: %v", err)
	}
	defer cityDB.Close()

	asnDB, err = geoip2.Open("/data/GeoLite2-ASN.mmdb")
	if err != nil {
		log.Fatalf("failed to open ASN DB: %v", err)
	}
	defer asnDB.Close()

	// Step 1: Build ASN->Country map using only IPv4
	asnCountryMap = buildASNtoCountry("/data/GeoLite2-ASN-Blocks-IPv4.csv", "/data/GeoLite2-City-Blocks-IPv4.csv")

	// Step 2: Build ASN <-> TSP map
	loadASNFromCSV("/data/GeoLite2-ASN-Blocks-IPv4.csv")

	http.HandleFunc("/lookup", cors(lookupHandler))
	http.HandleFunc("/tsp-list", cors(tspListHandler))

	port := getenv("PORT", "8081")
	log.Printf("Alak Geo listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// Build ASN→Country using only IPv4 CSVs
func buildASNtoCountry(asnFile, cityFile string) map[string]string {
	// 1. Load City Blocks: network (CIDR) → country code
	cityBlockToCountry := map[string]string{}
	f, _ := os.Open(cityFile)
	r := csv.NewReader(f)
	header, _ := r.Read()
	idxNetwork, idxCountry := -1, -1
	for i, col := range header {
		if col == "network" {
			idxNetwork = i
		}
		if col == "country_iso_code" {
			idxCountry = i
		}
	}
	if idxNetwork == -1 || idxCountry == -1 {
		log.Fatal("GeoLite2-City-Blocks-IPv4.csv missing required columns")
	}
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		network := rec[idxNetwork]
		country := strings.ToUpper(rec[idxCountry])
		if network != "" && country != "" {
			cityBlockToCountry[network] = country
		}
	}
	f.Close()

	// 2. Load ASN Blocks and map ASN → countries
	asnToCountry := map[string]map[string]int{}
	f, _ = os.Open(asnFile)
	r = csv.NewReader(f)
	r.Read() // skip header
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		network := rec[0]
		asn := "AS" + rec[1]
		country := cityBlockToCountry[network]
		if country != "" {
			if asnToCountry[asn] == nil {
				asnToCountry[asn] = map[string]int{}
			}
			asnToCountry[asn][country]++
		}
	}
	f.Close()

	// 3. Most frequent country per ASN
	out := map[string]string{}
	for asn, countries := range asnToCountry {
		maxC, maxN := "", 0
		for c, n := range countries {
			if n > maxN {
				maxC, maxN = c, n
			}
		}
		if maxC != "" {
			out[asn] = maxC
		}
	}
	log.Printf("Generated ASN→Country map for %d ASNs", len(out))
	return out
}

func cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

func loadASNFromCSV(file string) {
	tspMap = make(map[string]string)
	asnMap = make(map[string]LookupResponse)
	f, err := os.Open(file)
	if err != nil {
		log.Fatalf("warn: cannot open %s: %v", file, err)
	}
	reader := csv.NewReader(f)
	reader.Read() // skip header
	for {
		rec, err := reader.Read()
		if err != nil {
			break
		}
		asn := "AS" + rec[1]
		tsp := strings.ToLower(rec[2])
		if asn == "AS" || tsp == "" {
			continue
		}
		country := asnCountryMap[asn]
		asnMap[asn] = LookupResponse{ASN: asn, TSP: tsp, Country: country, City: ""}
		tspMap[tsp] = asn
	}
	f.Close()
	log.Printf("Loaded %d TSP records", len(tspMap))
}

func lookupHandler(w http.ResponseWriter, r *http.Request) {
	// 1) IP-based lookup
	if ipStr := r.URL.Query().Get("ip"); ipStr != "" {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			http.Error(w, "invalid ip", http.StatusBadRequest)
			return
		}
		cityRec, cErr := cityDB.City(ip)
		asnRec, aErr := asnDB.ASN(ip)
		if cErr != nil || aErr != nil {
			http.Error(w, "GeoIP lookup failed", http.StatusInternalServerError)
			return
		}
		asn := "AS" + strconv.Itoa(int(asnRec.AutonomousSystemNumber))
		country := cityRec.Country.IsoCode
		if country == "" {
			country = asnCountryMap[asn]
		}
		resp := LookupResponse{
			ASN:     asn,
			Country: country,
			TSP:     strings.ToLower(asnRec.AutonomousSystemOrganization),
			City:    cityRec.City.Names["en"],
		}
		json.NewEncoder(w).Encode(resp)
		return
	}

	// 2) ASN exact lookup
	if asnQ := strings.ToUpper(r.URL.Query().Get("asn")); asnQ != "" {
		if val, ok := asnMap[asnQ]; ok {
			val.Country = asnCountryMap[asnQ]
			json.NewEncoder(w).Encode(val)
			return
		}
	}

	// 3) TSP partial lookup
	if tspQ := strings.ToLower(r.URL.Query().Get("tsp")); tspQ != "" {
		var matches []LookupResponse
		for tsp, asn := range tspMap {
			if strings.Contains(tsp, tspQ) {
				val := asnMap[asn]
				val.Country = asnCountryMap[asn]
				matches = append(matches, val)
			}
		}
		switch len(matches) {
		case 0:
			http.Error(w, "Not found", http.StatusNotFound)
		case 1:
			json.NewEncoder(w).Encode(matches[0])
		default:
			w.WriteHeader(http.StatusMultipleChoices)
			json.NewEncoder(w).Encode(matches)
		}
		return
	}

	http.Error(w, "Invalid query", http.StatusBadRequest)
}

func tspListHandler(w http.ResponseWriter, r *http.Request) {
	var list []string
	for tsp := range tspMap {
		list = append(list, tsp)
	}
	json.NewEncoder(w).Encode(list)
}
