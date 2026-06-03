package config

import (
	"os"
	"path/filepath"
	"testing"
)

func withTempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	t.Setenv("PINGTRACE_CONFIG", p)
	for _, k := range SortedKeys() {
		envName := "PINGTRACE_" + envify(k)
		t.Setenv(envName, "")
		_ = os.Unsetenv(envName)
	}
	return p
}

func envify(k string) string {
	out := ""
	for _, r := range k {
		if r == '.' {
			out += "_"
		} else if r >= 'a' && r <= 'z' {
			out += string(r - 32)
		} else {
			out += string(r)
		}
	}
	return out
}

func TestSetGetUnset(t *testing.T) {
	withTempConfig(t)

	if err := Set("ipinfo.token", "abc123secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok, err := Get("ipinfo.token")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if v != "abc123secret" {
		t.Fatalf("want abc123secret, got %v", v)
	}

	if err := Unset("ipinfo.token"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	v, _, _ = Get("ipinfo.token")
	if v != "" {
		t.Fatalf("after unset want default empty, got %v", v)
	}
}

func TestNumericCoercion(t *testing.T) {
	withTempConfig(t)
	if err := Set("thresholds.latency.yellow_ms", "150"); err != nil {
		t.Fatalf("Set numeric: %v", err)
	}
	v, _, _ := Get("thresholds.latency.yellow_ms")
	f, ok := v.(float64)
	if !ok || f != 150 {
		t.Fatalf("want float64(150), got %T(%v)", v, v)
	}

	if err := Set("thresholds.latency.yellow_ms", "not-a-number"); err == nil {
		t.Fatal("expected error for non-numeric value")
	}
}

func TestUnknownKey(t *testing.T) {
	withTempConfig(t)
	if err := Set("does.not.exist", "x"); err == nil {
		t.Fatal("expected unknown-key error")
	}
}

func TestEnvOverridesFile(t *testing.T) {
	withTempConfig(t)
	if err := Set("ipinfo.token", "fromfile"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	t.Setenv("PINGTRACE_IPINFO_TOKEN", "fromenv")
	v, _, _ := Get("ipinfo.token")
	if v != "fromenv" {
		t.Fatalf("env should win, got %v", v)
	}
}

func TestRedact(t *testing.T) {
	cases := []struct {
		key  string
		val  any
		want string
	}{
		{"ipinfo.token", "", "(unset)"},
		{"ipinfo.token", "abcd", "****"},
		{"ipinfo.token", "abcdef", "ab**ef"},
		{"thresholds.latency.yellow_ms", float64(100), "100"},
	}
	for _, c := range cases {
		got := Format(Redact(c.key, c.val))
		if got != c.want {
			t.Errorf("Redact(%q,%v) = %q, want %q", c.key, c.val, got, c.want)
		}
	}
}
