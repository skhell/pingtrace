// Package enrich performs lightweight per-row enrichment of probe
// results: reverse-DNS lookups today, ASN / org / location later.
//
// The DNS resolver routes lookups through user-configured servers so
// that pingtrace can decode hostnames in private networks (split-
// horizon DNS) as well as on the public internet.
package enrich

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// Resolver performs reverse-DNS lookups against caller-supplied
// public and private servers. It is safe for concurrent use.
type Resolver struct {
	public  []string
	private []string
	timeout time.Duration

	cache sync.Map // ip -> string hostname (negative = "")
}

// New builds a Resolver. publicSrv and privateSrv are lists of
// host[:port] entries; port defaults to 53. timeout is the per-
// lookup wall-clock budget. Either list may be empty; if both are
// empty, Lookup* return "" immediately so callers pay no cost.
func New(publicSrv, privateSrv []string, timeout time.Duration) *Resolver {
	if timeout <= 0 {
		timeout = 250 * time.Millisecond
	}
	return &Resolver{
		public:  normalize(publicSrv),
		private: normalize(privateSrv),
		timeout: timeout,
	}
}

// Enabled reports whether the resolver has at least one server
// configured. When false, all lookups short-circuit to "".
func (r *Resolver) Enabled() bool {
	return len(r.public) > 0 || len(r.private) > 0
}

// PublicEnabled reports whether at least one public DNS server is configured.
func (r *Resolver) PublicEnabled() bool { return len(r.public) > 0 }

// PrivateEnabled reports whether at least one private DNS server is configured.
func (r *Resolver) PrivateEnabled() bool { return len(r.private) > 0 }

// Lookup auto-routes by IP class: private IPs go to the private
// server list, public IPs go to the public server list. Returns ""
// when no applicable servers are configured or the lookup fails.
// This is the convenient entry point for callers that want a single
// "decoded hostname" string per row.
func (r *Resolver) Lookup(ctx context.Context, ip string) string {
	if IsPrivateIP(ip) {
		return r.LookupPrivate(ctx, ip)
	}
	return r.LookupPublic(ctx, ip)
}

// LookupPublic resolves ip via the public server list. Returns ""
// when no public servers are configured, when ip is not a routable
// address, or when the lookup fails / times out.
func (r *Resolver) LookupPublic(ctx context.Context, ip string) string {
	if len(r.public) == 0 || ip == "" {
		return ""
	}
	if IsPrivateIP(ip) {
		return ""
	}
	return r.lookup(ctx, ip, r.public, "pub:")
}

// LookupPrivate resolves ip via the private server list. Returns ""
// when no private servers are configured, when ip is not a private
// address, or when the lookup fails.
func (r *Resolver) LookupPrivate(ctx context.Context, ip string) string {
	if len(r.private) == 0 || ip == "" {
		return ""
	}
	if !IsPrivateIP(ip) {
		return ""
	}
	return r.lookup(ctx, ip, r.private, "prv:")
}

func (r *Resolver) lookup(ctx context.Context, ip string, servers []string, ns string) string {
	key := ns + ip
	if v, ok := r.cache.Load(key); ok {
		return v.(string)
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	var found string
	for _, srv := range servers {
		res := newResolverFor(srv)
		names, err := res.LookupAddr(ctx, ip)
		if err == nil && len(names) > 0 {
			found = strings.TrimSuffix(names[0], ".")
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	r.cache.Store(key, found)
	return found
}

func newResolverFor(server string) *net.Resolver {
	addr := server
	if !strings.Contains(addr, ":") {
		addr += ":53"
	}
	dialer := &net.Dialer{Timeout: 200 * time.Millisecond}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _ string, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "udp", addr)
		},
	}
}

func normalize(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ParseList splits a comma- or whitespace-separated string into a
// trimmed slice. Empty entries are dropped.
func ParseList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

var privateNets []*net.IPNet

func init() {
	for _, cidr := range []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"127.0.0.0/8",
		"100.64.0.0/10", // CGNAT - treat as private for split-horizon needs
		"fc00::/7",
		"fe80::/10",
		"::1/128",
	} {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			privateNets = append(privateNets, n)
		}
	}
}

// IsPrivateIP reports whether ip falls in any of the RFC1918,
// loopback, link-local, ULA, or CGNAT ranges.
func IsPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range privateNets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}
