package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PeeringDBInfo is the subset of PeeringDB net record fields
// surfaced by pingtrace.
type PeeringDBInfo struct {
	ASN       int
	Name      string
	NetType   string // Content / NSP / Cable/DSL/ISP / Enterprise / Educational
	Policy    string // Open / Selective / Restrictive / No
	Traffic   string // e.g. "100+ Gbps"
	Prefixes4 int
	Prefixes6 int
	IXPCount  int
}

// PeeringDBClient fetches /api/net records keyed by ASN.
type PeeringDBClient struct {
	token   string
	timeout time.Duration
	http    *http.Client
	cache   sync.Map // ASN (int) -> PeeringDBInfo
}

// NewPeeringDB builds a client. token may be empty - PeeringDB
// allows anonymous reads with a lower rate limit. Enabled() is
// always true so the client is used whenever an ASN is known.
func NewPeeringDB(token string, timeout time.Duration) *PeeringDBClient {
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	return &PeeringDBClient{
		token:   strings.TrimSpace(token),
		timeout: timeout,
		http:    &http.Client{Timeout: timeout},
	}
}

// Enabled reports whether this client should be used. Returns true
// even without a token (anonymous reads work, just rate-limited).
// Callers should still guard on whether they have an ASN to query.
func (c *PeeringDBClient) Enabled() bool { return c != nil }

// LookupASN parses "AS13335" or "13335" and fetches the matching
// net record. Returns a zero PeeringDBInfo on any failure (no
// network, bad asn, http error, empty result).
func (c *PeeringDBClient) LookupASN(ctx context.Context, asn string) PeeringDBInfo {
	if !c.Enabled() {
		return PeeringDBInfo{}
	}
	num := parseASN(asn)
	if num <= 0 {
		return PeeringDBInfo{}
	}
	if v, ok := c.cache.Load(num); ok {
		return v.(PeeringDBInfo)
	}
	info := c.fetch(ctx, num)
	c.cache.Store(num, info)
	return info
}

func parseASN(s string) int {
	s = strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(s), "AS"))
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func (c *PeeringDBClient) fetch(ctx context.Context, asn int) PeeringDBInfo {
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	url := fmt.Sprintf("https://www.peeringdb.com/api/net?asn=%d", asn)
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		return PeeringDBInfo{}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "pingtrace")
	if c.token != "" {
		req.Header.Set("Authorization", "Api-Key "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return PeeringDBInfo{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return PeeringDBInfo{}
	}

	var wrap struct {
		Data []struct {
			ASN         int    `json:"asn"`
			Name        string `json:"name"`
			InfoType    string `json:"info_type"`
			PolicyGen   string `json:"policy_general"`
			InfoTraffic string `json:"info_traffic"`
			InfoPrefix4 int    `json:"info_prefixes4"`
			InfoPrefix6 int    `json:"info_prefixes6"`
			NetixlanSet []any  `json:"netixlan_set"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrap); err != nil || len(wrap.Data) == 0 {
		return PeeringDBInfo{}
	}
	d := wrap.Data[0]
	return PeeringDBInfo{
		ASN:       d.ASN,
		Name:      d.Name,
		NetType:   d.InfoType,
		Policy:    d.PolicyGen,
		Traffic:   d.InfoTraffic,
		Prefixes4: d.InfoPrefix4,
		Prefixes6: d.InfoPrefix6,
		IXPCount:  len(d.NetixlanSet),
	}
}
