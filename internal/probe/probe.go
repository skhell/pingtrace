// Package probe implements OS-native ping / trace / mtr engines rather than open raw ICMP sockets.
// Raw sockets require root or CAP_NET_RAW on Linux, are restricted on macOS user sessions, and need elevated
// privileges on Windows. Shelling out works everywhere with no setup; this is a deliberate non-feature.
package probe

// PingResult is the parsed outcome of a ping run.
type PingResult struct {
	Command  string      `json:"command"`
	Sent     int         `json:"sent"`
	Received int         `json:"received"`
	LossPct  float64     `json:"lossPct"`
	AvgMs    float64     `json:"avgMs"`
	MinMs    float64     `json:"minMs"`
	MaxMs    float64     `json:"maxMs"`
	Packets  []PingReply `json:"packets"`
}

// PingReply describes one echo reply (or one timeout).
type PingReply struct {
	Seq    int     `json:"seq"`
	Bytes  int     `json:"bytes"`
	IP     string  `json:"ip"`
	TTL    int     `json:"ttl"`
	TimeMs float64 `json:"timeMs"`
	Status string  `json:"status"` // "ok" | "timeout"

	// Enrichment (DNS populated when dns.public/dns.private are set;
	// org/asn/location/city/region/country/loc from ipinfo;
	// policy/net_type/pdb_* from PeeringDB).
	PublicDNS  string `json:"publicDns,omitempty"`
	PrivateDNS string `json:"privateDns,omitempty"`
	Hostname   string `json:"hostname,omitempty"` // ipinfo PTR
	Org        string `json:"org,omitempty"`
	ASN        string `json:"asn,omitempty"`
	Location   string `json:"location,omitempty"` // "City, CC"
	City       string `json:"city,omitempty"`
	Region     string `json:"region,omitempty"`
	Country    string `json:"country,omitempty"`
	Loc        string `json:"loc,omitempty"` // "lat,lon"
	Policy     string `json:"policy,omitempty"`
	NetType    string `json:"netType,omitempty"`
	PdbName    string `json:"pdbName,omitempty"`
	Traffic    string `json:"traffic,omitempty"`
	Prefixes4  int    `json:"prefixes4,omitempty"`
	Prefixes6  int    `json:"prefixes6,omitempty"`
	IXPCount   int    `json:"ixpCount,omitempty"`
}

// TraceResult is the parsed outcome of a traceroute run.
type TraceResult struct {
	Command string     `json:"command"`
	Hops    []TraceHop `json:"hops"`
}

// TraceHop is one row of traceroute output (one TTL).
type TraceHop struct {
	Hop      int     `json:"hop"`
	IP       string  `json:"ip"`
	Host     string  `json:"host,omitempty"`
	Probe1Ms float64 `json:"probe1Ms"`
	Probe2Ms float64 `json:"probe2Ms"`
	Probe3Ms float64 `json:"probe3Ms"`
	TimeMs   float64 `json:"timeMs"` // = Probe1Ms, kept for back-compat
	Status   string  `json:"status"` // "ok" | "timeout"

	// Enrichment (see PingReply for field semantics).
	PublicDNS  string `json:"publicDns,omitempty"`
	PrivateDNS string `json:"privateDns,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	Org        string `json:"org,omitempty"`
	ASN        string `json:"asn,omitempty"`
	Location   string `json:"location,omitempty"`
	City       string `json:"city,omitempty"`
	Region     string `json:"region,omitempty"`
	Country    string `json:"country,omitempty"`
	Loc        string `json:"loc,omitempty"`
	Policy     string `json:"policy,omitempty"`
	NetType    string `json:"netType,omitempty"`
	PdbName    string `json:"pdbName,omitempty"`
	Traffic    string `json:"traffic,omitempty"`
	Prefixes4  int    `json:"prefixes4,omitempty"`
	Prefixes6  int    `json:"prefixes6,omitempty"`
	IXPCount   int    `json:"ixpCount,omitempty"`
}
