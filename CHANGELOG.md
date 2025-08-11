# Changelog

All notable changes to this project will be documented in this file.  
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [v1.0.0] – 2025-08-11
### 🚀 Initial Release – Alak Stack
First stable release of the **Alak Stack**, providing a complete set of services for load-shedding control, rule management, and ISP/Geo-based traffic filtering.

---

### 🛠 Included Services

#### **Alak Controller**
- Central API for ASN/TSP load-shedding rule management.
- Add, delete, enable, disable rules.
- Redis-backed persistence.
- CORS support for Dashboard integration.

#### **Alak Geo**
- GeoIP lookup API (MaxMind GeoLite2 ASN & City).
- Returns ASN, Country and TSP.
- Fuzzy TSP search + multiple match resolution.

#### **Alak Gatekeeper**
- HAProxy sidecar for real-time traffic filtering.
- Rule enforcement by ASN, Country, TSP.
- Prometheus `/metrics` endpoint for analytics.
- Configurable Redis and HAProxy integration.

#### **Alak Dashboard**
- Web UI for rule management and monitoring.
- Real-time rule list (auto-refresh every 5s).
- ASN/TSP search with Geo service autofill.
- Add, delete, enable, disable rules via UI.
- Built-in API proxy to Controller & Geo services.

---

### 📦 Deployment Highlights
- Kubernetes-ready via Helm chart.
- Ingress + TLS support.
- Environment variable configuration:
  - `CONTROLLER_ORIGIN`, `GEO_ORIGIN`
  - `REDIS_HOST`, `CORS_ORIGINS`
  - `HA_PROXY_URL`, `SKIP_TLS_VERIFY`

---

### ⚠ Known Issues
- No authentication yet — secure endpoints via network-level restrictions.
- Manual process required for Geo database updates.

---

[v1.0.0]: https://github.com/mehrdadrfe/alak-stack/releases/tag/v1.0.0
