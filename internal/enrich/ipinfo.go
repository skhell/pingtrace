package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IPInfo is the subset of ipinfo.io fields pingtrace surfaces.
type IPInfo struct {
	IP       string
	Hostname string
	City     string
	Region   string
	Country  string
	Org      string // human-readable org name (AS prefix stripped)
	ASN      string // e.g. "AS13335"
	Loc      string // "lat,lon"
}

// IPInfoClient looks up IP metadata via ipinfo.io and caches the
// result per IP. Safe for concurrent use.
type IPInfoClient struct {
	token   string
	timeout time.Duration
	http    *http.Client
	cache   sync.Map // ip -> IPInfo
}

// NewIPInfo builds a client. token may be empty (Enabled() will then
// return false and Lookup() short-circuits to an empty result).
func NewIPInfo(token string, timeout time.Duration) *IPInfoClient {
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}
	return &IPInfoClient{
		token:   strings.TrimSpace(token),
		timeout: timeout,
		http:    &http.Client{Timeout: timeout},
	}
}

// Enabled reports whether a token is configured.
func (c *IPInfoClient) Enabled() bool {
	return c != nil && c.token != ""
}

// Lookup fetches IPInfo for ip. Returns a zero IPInfo (no error
// signal) when disabled, on private IPs, on HTTP errors, or on
// timeout - pingtrace prefers showing blank cells to surfacing
// transient enrichment failures.
func (c *IPInfoClient) Lookup(ctx context.Context, ip string) IPInfo {
	if !c.Enabled() || ip == "" || IsPrivateIP(ip) {
		return IPInfo{}
	}
	if v, ok := c.cache.Load(ip); ok {
		return v.(IPInfo)
	}
	info := c.fetch(ctx, ip)
	c.cache.Store(ip, info)
	return info
}

func (c *IPInfoClient) fetch(ctx context.Context, ip string) IPInfo {
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	url := fmt.Sprintf("https://ipinfo.io/%s?token=%s", ip, c.token)
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		return IPInfo{}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "pingtrace")

	resp, err := c.http.Do(req)
	if err != nil {
		return IPInfo{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return IPInfo{}
	}

	var raw struct {
		IP       string `json:"ip"`
		Hostname string `json:"hostname"`
		City     string `json:"city"`
		Region   string `json:"region"`
		Country  string `json:"country"`
		Loc      string `json:"loc"`
		Org      string `json:"org"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return IPInfo{}
	}

	asn, org := splitASNOrg(raw.Org)
	return IPInfo{
		IP:       raw.IP,
		Hostname: raw.Hostname,
		City:     raw.City,
		Region:   raw.Region,
		Country:  raw.Country,
		Org:      org,
		ASN:      asn,
		Loc:      raw.Loc,
	}
}

// splitASNOrg parses ipinfo's combined "AS13335 Cloudflare, Inc."
// org field into its ASN and human-readable parts. Returns ("", s)
// if no AS prefix is present.
func splitASNOrg(s string) (asn, org string) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "AS") {
		return "", s
	}
	sp := strings.IndexByte(s, ' ')
	if sp < 0 {
		return s, ""
	}
	return s[:sp], strings.TrimSpace(s[sp+1:])
}

// FormatLocation builds a compact "City, CC" string for the
// location column. Falls back to lat,lon when city is unset.
func (i IPInfo) FormatLocation() string {
	switch {
	case i.City != "" && i.Country != "":
		return i.City + ", " + i.Country
	case i.City != "":
		return i.City
	case i.Country != "":
		return i.Country
	default:
		return i.Loc
	}
}
