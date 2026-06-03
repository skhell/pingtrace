package probe

import (
	"testing"
)

// Real-world output samples captured from each platform's native
// tools. These tests run on every CI OS (linux/macos/windows)
// because parseTraceLine and parsePingLine are now GOOS-agnostic;
// a Windows-format regression will fail the Linux job too.

func TestParsePingLine_Linux(t *testing.T) {
	cases := []struct {
		line     string
		wantOK   bool
		wantIP   string
		wantBy   int
		wantTTL  int
		wantTime float64
	}{
		{
			line:   "64 bytes from 1.1.1.1: icmp_seq=1 ttl=56 time=14.2 ms",
			wantOK: true,
			wantIP: "1.1.1.1", wantBy: 64, wantTTL: 56, wantTime: 14.2,
		},
		{
			line:   "64 bytes from one.one.one.one (1.1.1.1): icmp_seq=2 ttl=56 time=0.412 ms",
			wantOK: true,
			wantIP: "1.1.1.1", wantBy: 64, wantTTL: 56, wantTime: 0.412,
		},
		{
			line:   "64 bytes from 2606:4700:4700::1111: icmp_seq=1 ttl=56 time=14.2 ms",
			wantOK: true,
			wantIP: "2606:4700:4700::1111", wantBy: 64, wantTTL: 56, wantTime: 14.2,
		},
		{line: "PING 1.1.1.1 (1.1.1.1) 56(84) bytes of data."},
		{line: "--- 1.1.1.1 ping statistics ---"},
		{line: ""},
	}
	for _, c := range cases {
		got, ok := parsePingLine(c.line, 0)
		if ok != c.wantOK {
			t.Errorf("ok=%v want %v for %q", ok, c.wantOK, c.line)
			continue
		}
		if !ok {
			continue
		}
		if got.IP != c.wantIP || got.Bytes != c.wantBy || got.TTL != c.wantTTL || got.TimeMs != c.wantTime {
			t.Errorf("parsed %+v want IP=%s bytes=%d ttl=%d time=%v from %q",
				got, c.wantIP, c.wantBy, c.wantTTL, c.wantTime, c.line)
		}
	}
}

func TestParsePingLine_Windows(t *testing.T) {
	cases := []struct {
		line     string
		wantIP   string
		wantBy   int
		wantTTL  int
		wantTime float64
	}{
		{"Reply from 1.1.1.1: bytes=32 time=14ms TTL=56", "1.1.1.1", 32, 56, 14},
		{"Reply from 1.1.1.1: bytes=32 time<1ms TTL=56", "1.1.1.1", 32, 56, 1},
		{"Reply from 8.8.8.8: bytes=32 time=23ms TTL=117", "8.8.8.8", 32, 117, 23},
	}
	for _, c := range cases {
		got, ok := parsePingLine(c.line, 0)
		if !ok {
			t.Errorf("did not parse %q", c.line)
			continue
		}
		if got.IP != c.wantIP || got.Bytes != c.wantBy || got.TTL != c.wantTTL || got.TimeMs != c.wantTime {
			t.Errorf("parsed %+v want IP=%s bytes=%d ttl=%d time=%v from %q",
				got, c.wantIP, c.wantBy, c.wantTTL, c.wantTime, c.line)
		}
	}

	for _, skip := range []string{
		"Pinging 1.1.1.1 with 32 bytes of data:",
		"Request timed out.",
		"Reply from 192.168.1.1: Destination host unreachable.",
		"Ping statistics for 1.1.1.1:",
		"    Packets: Sent = 4, Received = 4, Lost = 0 (0% loss),",
		"Approximate round trip times in milli-seconds:",
		"    Minimum = 12ms, Maximum = 23ms, Average = 17ms",
	} {
		if _, ok := parsePingLine(skip, 0); ok {
			t.Errorf("unexpected match on non-reply line: %q", skip)
		}
	}
}

func TestParseTraceLine_LinuxBSD(t *testing.T) {
	cases := []struct {
		line     string
		wantHop  int
		wantIP   string
		wantHost string
		wantStat string
		want1    float64
	}{
		{"  1  router.lan (192.168.1.1)  0.512 ms  0.421 ms  0.398 ms", 1, "192.168.1.1", "router.lan", "ok", 0.512},
		{" 2  10.0.0.1  1.234 ms  1.345 ms  1.123 ms", 2, "10.0.0.1", "", "ok", 1.234},
		{" 3  * * *", 3, "", "", "timeout", 0},
		{" 4  one.one.one.one (1.1.1.1)  14.2 ms  14.1 ms  13.9 ms", 4, "1.1.1.1", "one.one.one.one", "ok", 14.2},
		{"traceroute to 1.1.1.1 (1.1.1.1), 30 hops max, 60 byte packets", 0, "", "", "", 0},
	}
	for _, c := range cases {
		got, ok := parseTraceLine(c.line)
		if c.wantHop == 0 {
			if ok {
				t.Errorf("header line parsed as hop: %q", c.line)
			}
			continue
		}
		if !ok {
			t.Errorf("did not parse %q", c.line)
			continue
		}
		if got.Hop != c.wantHop || got.IP != c.wantIP || got.Host != c.wantHost ||
			got.Status != c.wantStat || got.Probe1Ms != c.want1 {
			t.Errorf("parsed %+v want hop=%d ip=%s host=%s status=%s p1=%v from %q",
				got, c.wantHop, c.wantIP, c.wantHost, c.wantStat, c.want1, c.line)
		}
	}
}

func TestParseTraceLine_Windows(t *testing.T) {
	cases := []struct {
		line     string
		wantHop  int
		wantIP   string
		wantHost string
		wantStat string
		want1    float64
	}{
		{"  1     1 ms     1 ms     1 ms  192.168.1.1", 1, "192.168.1.1", "", "ok", 1},
		{"  2     *        *        *     Request timed out.", 2, "", "", "timeout", 0},
		{"  3    14 ms    13 ms    14 ms  one.one.one.one [1.1.1.1]", 3, "1.1.1.1", "one.one.one.one", "ok", 14},
		{"  4    <1 ms    <1 ms    <1 ms  router-1.example.com [10.0.0.1]", 4, "10.0.0.1", "router-1.example.com", "ok", 1},
		{"Tracing route to one.one.one.one [1.1.1.1]", 0, "", "", "", 0},
		{"over a maximum of 30 hops:", 0, "", "", "", 0},
		{"Trace complete.", 0, "", "", "", 0},
	}
	for _, c := range cases {
		got, ok := parseTraceLine(c.line)
		if c.wantHop == 0 {
			if ok {
				t.Errorf("non-hop line parsed as hop: %q -> %+v", c.line, got)
			}
			continue
		}
		if !ok {
			t.Errorf("did not parse %q", c.line)
			continue
		}
		if got.Hop != c.wantHop || got.IP != c.wantIP || got.Host != c.wantHost ||
			got.Status != c.wantStat || got.Probe1Ms != c.want1 {
			t.Errorf("parsed %+v want hop=%d ip=%s host=%s status=%s p1=%v from %q",
				got, c.wantHop, c.wantIP, c.wantHost, c.wantStat, c.want1, c.line)
		}
	}
}

func TestParseTraceLine_WindowsAllTimeouts(t *testing.T) {
	// All-asterisk Windows hop: probe2 and probe3 should also be
	// populated (as 0) so the CSV row keeps 3 probe columns.
	got, ok := parseTraceLine("  2     *        *        *     Request timed out.")
	if !ok {
		t.Fatal("did not parse all-asterisk hop")
	}
	if got.Probe1Ms != 0 || got.Probe2Ms != 0 || got.Probe3Ms != 0 {
		t.Errorf("expected zero probes, got %+v", got)
	}
	if got.Status != "timeout" {
		t.Errorf("expected status=timeout, got %s", got.Status)
	}
}
