package jsonreport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skhell/pingtrace/internal/probe"
)

func TestFromPingResultPopulatesEnrichmentAndDuration(t *testing.T) {
	pr := probe.PingResult{
		Command: "ping -c 1 1.1.1.1",
		Sent:    1, Received: 1, LossPct: 0, AvgMs: 4.2,
		Packets: []probe.PingReply{{
			Seq: 0, Bytes: 64, IP: "1.1.1.1", TTL: 56, TimeMs: 4.2, Status: "ok",
			Org: "Cloudflare", ASN: "AS13335", Location: "San Francisco, CA",
			PublicDNS: "one.one.one.one", Policy: "Open", NetType: "Content",
		}},
	}
	r := FromPingResult("1.1.1.1", "argument", 123, pr)
	if r.Source != "argument" || r.Operation != "ping" || r.DurationMs != 123 {
		t.Fatalf("unexpected result: %+v", r)
	}
	if len(r.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(r.Rows))
	}
	row := r.Rows[0]
	if row.Org != "Cloudflare" || row.ASN != "AS13335" || row.Policy != "Open" || row.NetType != "Content" {
		t.Fatalf("enrichment not propagated: %+v", row)
	}
	if row.TimeMs == nil || *row.TimeMs != 4.2 {
		t.Fatalf("timeMs missing or wrong: %+v", row)
	}
	if row.TTL == nil || *row.TTL != 56 {
		t.Fatalf("ttl missing: %+v", row)
	}
}

func TestNormalizeSourceMapsFileToCsv(t *testing.T) {
	if NormalizeSource("file") != "csv" {
		t.Fatal("file should map to csv")
	}
	if NormalizeSource("argument") != "argument" {
		t.Fatal("argument should pass through")
	}
}

func TestWriterEmitsProbeReport(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir, "1.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	w.AddTarget(Target{Value: "1.1.1.1", Source: "argument"})
	w.AppendPing("1.1.1.1", "argument", 10, probe.PingResult{
		Command: "ping", Packets: []probe.PingReply{{Seq: 0, IP: "1.1.1.1", Bytes: 64, TTL: 60, TimeMs: 5, Status: "ok"}},
	})
	exported := []ExportedFile{{Operation: "ping", Path: "ping.csv", RowCount: 1}}
	paths, err := w.Close(exported)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("want 1 path, got %v", paths)
	}
	if !strings.HasPrefix(filepath.Base(paths[0]), "probe_UTC") {
		t.Fatalf("unexpected filename: %s", paths[0])
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["mode"] != "probe" {
		t.Fatalf("expected mode=probe, got %v", doc["mode"])
	}
	if _, ok := doc["$schema"].(string); !ok {
		t.Fatal("missing $schema")
	}
	if ef, ok := doc["exportedFiles"].([]any); !ok || len(ef) != 1 {
		t.Fatalf("exportedFiles missing or wrong: %v", doc["exportedFiles"])
	}
}

func TestWriterEmitsMtrReportPerTarget(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir, "1.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	cycles := 2
	w.AppendMTR("1.1.1.1", &cycles, 1000, "traceroute", probe.MTRResult{
		Target: "1.1.1.1", Cycles: 2,
		Hops: []probe.MTRStat{{Hop: 1, IP: "10.0.0.1", Snt: 2, LossPc: 0, Last: 1.1, Avg: 1.0, Best: 0.9, Wrst: 1.1, StDev: 0.1}},
	})
	w.AppendMTR("1.1.1.1", &cycles, 1000, "traceroute", probe.MTRResult{Target: "1.1.1.1", Cycles: 2})
	paths, err := w.Close(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("want 2 mtr files, got %v", paths)
	}
	for _, p := range paths {
		base := filepath.Base(p)
		if !strings.HasPrefix(base, "mtr_") {
			t.Fatalf("not an mtr file: %s", base)
		}
		var doc map[string]any
		data, _ := os.ReadFile(p)
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if doc["mode"] != "mtr" {
			t.Fatalf("expected mode=mtr, got %v", doc["mode"])
		}
	}
}
