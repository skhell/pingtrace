// Package config manages persistent user settings for pingtrace.
//
// The on-disk format is a single JSON document stored at
// <UserConfigDir>/pingtrace/config.json. Values are addressed by
// dotted keys (for example, "ipinfo.token" or
// "thresholds.latency.yellow_ms"). Environment variables of the
// form PINGTRACE_<UPPER_DOTTED> override the file on read.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Defaults returns the baseline configuration. Any key not present
// in the on-disk file falls back to these values.
func Defaults() map[string]any {
	return map[string]any{
		"ipinfo.token":         "",
		"ipinfo.timeout_ms":    float64(800),
		"peeringdb.token":      "",
		"peeringdb.timeout_ms": float64(1500),

		// Ping engine defaults.
		"ping.count":       float64(4),
		"ping.packet_size": float64(56),   // payload bytes (Unix default)
		"ping.timeout_ms":  float64(1000), // per-packet receive timeout
		"ping.interval_ms": float64(1000), // gap between sends

		// Traceroute engine defaults.
		"trace.max_hops":    float64(30),
		"trace.queries":     float64(3),    // probes per hop
		"trace.wait_ms":     float64(5000), // per-probe timeout
		"trace.packet_size": float64(60),

		// MTR engine defaults (interval/cycles can also be flag-driven).
		"mtr.interval_ms": float64(1000),
		"mtr.cycles":      float64(0), // 0 = continuous

		// DNS resolution for the host / public_dns / private_dns
		// columns. Servers are comma-separated host[:port] entries;
		// port defaults to 53 when omitted. Public servers are used
		// for routable IPs; private servers for RFC1918, link-local,
		// loopback, and ULA addresses (useful in corporate networks
		// with split-horizon DNS).
		"dns.public":     "",
		"dns.private":    "",
		"dns.timeout_ms": float64(250),

		// Renderer color thresholds.
		"thresholds.latency.green_ms":  float64(50),
		"thresholds.latency.yellow_ms": float64(100),
		"thresholds.latency.orange_ms": float64(200),
		"thresholds.latency.red_ms":    float64(400),
		"thresholds.loss.yellow_pct":   float64(1),
		"thresholds.loss.orange_pct":   float64(5),
		"thresholds.loss.red_pct":      float64(20),
	}
}

// SecretKeys lists keys whose values must be redacted in human
// output unless the caller opts in.
var SecretKeys = map[string]bool{
	"ipinfo.token":    true,
	"peeringdb.token": true,
}

// KeyMeta describes a single config key: which category it belongs
// to (used for grouping in the UI), a one-line description shown
// inline, and an optional hint URL or follow-up sentence (used for
// account-signup nudges on ipinfo / peeringdb).
type KeyMeta struct {
	Category    string
	Description string
	Hint        string
}

// CategoryOrder is the display order used by `config list` and the
// interactive TUI. Keys not associated with any category fall under
// "Other".
var CategoryOrder = []string{
	"Tokens & accounts",
	"DNS resolution",
	"Ping engine",
	"Traceroute engine",
	"MTR engine",
	"Color thresholds",
	"Other",
}

// Meta returns the description table for every known key. Keep
// descriptions short (fits in a list view) and use Hint for the
// "go sign up" nudge or unit explanations.
func Meta() map[string]KeyMeta {
	return map[string]KeyMeta{
		"ipinfo.token": {
			Category:    "Tokens & accounts",
			Description: "API token for ipinfo.io enrichment (ASN, org, geo).",
			Hint:        "Free tier: 50k req/mo. Sign up at https://ipinfo.io/signup",
		},
		"ipinfo.timeout_ms": {
			Category:    "Tokens & accounts",
			Description: "Per-IP HTTP timeout for ipinfo.io lookups (ms).",
		},
		"peeringdb.token": {
			Category:    "Tokens & accounts",
			Description: "PeeringDB API token (raises rate limit; reads work anonymously).",
			Hint:        "Free account: https://www.peeringdb.com/register",
		},
		"peeringdb.timeout_ms": {
			Category:    "Tokens & accounts",
			Description: "Per-ASN HTTP timeout for PeeringDB lookups (ms).",
		},

		"dns.public": {
			Category:    "DNS resolution",
			Description: "Comma-separated DNS servers for routable IPs.",
			Hint:        "Example: 1.1.1.1,8.8.8.8 (port 53 assumed if omitted).",
		},
		"dns.private": {
			Category:    "DNS resolution",
			Description: "DNS servers for RFC1918 / link-local / ULA / CGNAT.",
			Hint:        "Use your internal resolver for split-horizon DNS.",
		},
		"dns.timeout_ms": {
			Category:    "DNS resolution",
			Description: "Per-lookup timeout in milliseconds (default 250).",
		},

		"ping.count": {
			Category:    "Ping engine",
			Description: "Number of echo requests sent per target.",
		},
		"ping.packet_size": {
			Category:    "Ping engine",
			Description: "ICMP payload bytes (Unix default 56 = 64-byte packet).",
		},
		"ping.timeout_ms": {
			Category:    "Ping engine",
			Description: "Per-packet receive timeout in milliseconds.",
		},
		"ping.interval_ms": {
			Category:    "Ping engine",
			Description: "Delay between sends in milliseconds.",
			Hint:        "Sub-second intervals usually require root/elevated rights.",
		},

		"trace.max_hops": {
			Category:    "Traceroute engine",
			Description: "Maximum TTL to probe (default 30).",
		},
		"trace.queries": {
			Category:    "Traceroute engine",
			Description: "Number of probes per hop (default 3).",
		},
		"trace.wait_ms": {
			Category:    "Traceroute engine",
			Description: "Per-probe response timeout in milliseconds.",
		},
		"trace.packet_size": {
			Category:    "Traceroute engine",
			Description: "Probe packet size in bytes.",
		},

		"mtr.interval_ms": {
			Category:    "MTR engine",
			Description: "Delay between MTR cycles in milliseconds.",
		},
		"mtr.cycles": {
			Category:    "MTR engine",
			Description: "Number of cycles to run; 0 means continuous.",
		},

		"thresholds.latency.green_ms": {
			Category:    "Color thresholds",
			Description: "RTT <= this is colored green (excellent).",
		},
		"thresholds.latency.yellow_ms": {
			Category:    "Color thresholds",
			Description: "RTT <= this is colored yellow (acceptable).",
		},
		"thresholds.latency.orange_ms": {
			Category:    "Color thresholds",
			Description: "RTT <= this is colored orange (degraded).",
		},
		"thresholds.latency.red_ms": {
			Category:    "Color thresholds",
			Description: "RTT above this is colored red (critical).",
		},
		"thresholds.loss.yellow_pct": {
			Category:    "Color thresholds",
			Description: "Packet loss % at which to start coloring yellow.",
		},
		"thresholds.loss.orange_pct": {
			Category:    "Color thresholds",
			Description: "Packet loss % at which to start coloring orange.",
		},
		"thresholds.loss.red_pct": {
			Category:    "Color thresholds",
			Description: "Packet loss % at which to start coloring red (critical).",
		},
	}
}

// KeysByCategory returns the known keys grouped by category, with
// each group sorted alphabetically. Categories follow CategoryOrder
// and any unknown ones are appended at the end.
func KeysByCategory() [][2]any {
	meta := Meta()
	groups := map[string][]string{}
	for _, k := range SortedKeys() {
		cat := "Other"
		if m, ok := meta[k]; ok && m.Category != "" {
			cat = m.Category
		}
		groups[cat] = append(groups[cat], k)
	}
	seen := map[string]bool{}
	out := make([][2]any, 0, len(groups))
	for _, cat := range CategoryOrder {
		if ks, ok := groups[cat]; ok {
			out = append(out, [2]any{cat, ks})
			seen[cat] = true
		}
	}
	for cat, ks := range groups {
		if !seen[cat] {
			out = append(out, [2]any{cat, ks})
		}
	}
	return out
}

// Path returns the absolute path to the pingtrace config file.
// Honors PINGTRACE_CONFIG if set.
func Path() (string, error) {
	if override := os.Getenv("PINGTRACE_CONFIG"); override != "" {
		return override, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "pingtrace", "config.json"), nil
}

// Load reads the config file. A missing file is not an error;
// callers receive an empty map and can layer Defaults on top.
func Load() (map[string]any, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	nested := map[string]any{}
	if err := json.Unmarshal(b, &nested); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return flatten("", nested), nil
}

// Save writes the supplied flat key/value map to the config file
// atomically (write to .tmp + rename). Parent directories are
// created with 0o700.
func Save(flat map[string]any) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	nested := unflatten(flat)
	b, err := json.MarshalIndent(nested, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	b = append(b, '\n')

	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, p, err)
	}
	return nil
}

// Effective merges defaults, file, and env overrides. Env wins,
// file second, defaults last. Returned values are typed per the
// defaults map (numbers stay float64, strings stay string).
func Effective() (map[string]any, error) {
	out := Defaults()
	file, err := Load()
	if err != nil {
		return nil, err
	}
	for k, v := range file {
		if def, ok := out[k]; ok {
			out[k] = coerce(def, v)
		} else {
			out[k] = v
		}
	}
	for k := range out {
		if ev, ok := envLookup(k); ok {
			out[k] = coerce(out[k], ev)
		}
	}
	return out, nil
}

// Get returns the effective value for a single key.
func Get(key string) (any, bool, error) {
	eff, err := Effective()
	if err != nil {
		return nil, false, err
	}
	v, ok := eff[key]
	return v, ok, nil
}

// Set persists key=value to the file. The value is coerced to the
// type implied by Defaults (numeric keys parse as float64).
func Set(key, value string) error {
	if key == "" {
		return errors.New("config key is required")
	}
	if !KnownKey(key) {
		return fmt.Errorf("unknown config key %q (run `pingtrace config list` for valid keys)", key)
	}
	file, err := Load()
	if err != nil {
		return err
	}
	def := Defaults()[key]
	typed := coerce(def, value)
	if _, isFloat := def.(float64); isFloat {
		if _, ok := typed.(float64); !ok {
			return fmt.Errorf("value for %q must be numeric, got %q", key, value)
		}
	}
	file[key] = typed
	return Save(file)
}

// Unset removes a key from the file (defaults / env still apply).
func Unset(key string) error {
	file, err := Load()
	if err != nil {
		return err
	}
	if _, ok := file[key]; !ok {
		return nil
	}
	delete(file, key)
	return Save(file)
}

// KnownKey reports whether the key is part of the defaults set.
func KnownKey(key string) bool {
	_, ok := Defaults()[key]
	return ok
}

// SortedKeys returns the defaults' keys in stable order for
// listing and tab-completion.
func SortedKeys() []string {
	def := Defaults()
	keys := make([]string, 0, len(def))
	for k := range def {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Redact returns "(unset)" for empty secrets and a masked form
// otherwise. Non-secret values pass through unchanged.
func Redact(key string, value any) any {
	if !SecretKeys[key] {
		return value
	}
	s, ok := value.(string)
	if !ok || s == "" {
		return "(unset)"
	}
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + strings.Repeat("*", len(s)-4) + s[len(s)-2:]
}

// Format renders a value for human output (numbers without
// trailing zeros, strings as-is).
func Format(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// --- helpers ---------------------------------------------------

func envLookup(key string) (string, bool) {
	name := "PINGTRACE_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
	return os.LookupEnv(name)
}

func coerce(def any, raw any) any {
	switch def.(type) {
	case float64:
		switch v := raw.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case string:
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return raw
			}
			return f
		}
	case string:
		switch v := raw.(type) {
		case string:
			return v
		default:
			return fmt.Sprintf("%v", v)
		}
	case bool:
		switch v := raw.(type) {
		case bool:
			return v
		case string:
			b, err := strconv.ParseBool(v)
			if err != nil {
				return raw
			}
			return b
		}
	}
	return raw
}

func flatten(prefix string, in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if child, ok := v.(map[string]any); ok {
			for ck, cv := range flatten(key, child) {
				out[ck] = cv
			}
			continue
		}
		out[key] = v
	}
	return out
}

func unflatten(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		parts := strings.Split(k, ".")
		cur := out
		for i, p := range parts {
			if i == len(parts)-1 {
				cur[p] = v
				break
			}
			next, ok := cur[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[p] = next
			}
			cur = next
		}
	}
	return out
}
