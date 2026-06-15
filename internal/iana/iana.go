// Package iana provides a lookup table of IANA service name assignments
// built from the official service-names-port-numbers CSV registry.
//
// No data is bundled in the binary. On first use LoadEffective auto-downloads
// the CSV from the configured URL and caches it in the user config directory.
// Subsequent calls load from the cache. Manual refresh is available via Sync
// or the config TUI sync button.
package iana

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Entry is a single IANA port assignment with all registry columns preserved.
// Field names match the official CSV header so callers can write them verbatim.
type Entry struct {
	ServiceName             string
	PortNumber              string // stored as string to preserve original value
	TransportProtocol       string
	Description             string
	Assignee                string
	Contact                 string
	RegistrationDate        string
	ModificationDate        string
	Reference               string
	ServiceCode             string
	UnauthorizedUseReported string
	AssignmentNotes         string
}

// DB is a loaded IANA registry keyed by (port, protocol).
type DB map[portKey]Entry

type portKey struct {
	port  int
	proto string // lowercase: "tcp", "udp", "sctp", "dccp", or ""
}

// Lookup returns the IANA entry for port and proto ("tcp"/"udp").
// Falls back to a protocol-agnostic entry when no exact match exists.
func (db DB) Lookup(port int, proto string) (Entry, bool) {
	if e, ok := db[portKey{port, strings.ToLower(proto)}]; ok {
		return e, true
	}
	e, ok := db[portKey{port, ""}]
	return e, ok
}

// LoadEffective loads the IANA registry from the user cache when it exists.
// If the cache is absent or corrupt it auto-downloads a fresh copy from url
// and caches it before returning. On download failure it returns an empty DB
// and a non-nil error so callers can degrade gracefully (ports show as N/tcp).
func LoadEffective(url string) (DB, error) {
	p, err := CachePath()
	if err == nil {
		if f, err2 := os.Open(p); err2 == nil {
			db, err3 := parseCSV(f)
			f.Close()
			if err3 == nil {
				return db, nil
			}
		}
	}
	return downloadAndLoad(url)
}

func downloadAndLoad(url string) (DB, error) {
	if url == "" {
		url = "https://www.iana.org/assignments/service-names-port-numbers/service-names-port-numbers.csv"
	}
	if err := Sync(url); err != nil {
		return make(DB), fmt.Errorf("IANA download failed: %w", err)
	}
	p, err := CachePath()
	if err != nil {
		return make(DB), err
	}
	f, err := os.Open(p)
	if err != nil {
		return make(DB), err
	}
	defer f.Close()
	return parseCSV(f)
}

// CachePath returns the path for the user-downloaded CSV.
func CachePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "pingtrace", "iana", "service-names-port-numbers.csv"), nil
}

// LastSyncTime returns the modification time of the user cache file
// and whether the cache exists at all.
func LastSyncTime() (time.Time, bool) {
	p, err := CachePath()
	if err != nil {
		return time.Time{}, false
	}
	info, err := os.Stat(p)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime().UTC(), true
}

// Sync downloads the IANA CSV from url and writes it to the user cache
// directory atomically. The cache is used by LoadEffective on the next call.
func Sync(url string) error {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return fmt.Errorf("download IANA CSV: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("IANA server returned %s", resp.Status)
	}

	p, err := CachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create iana cache dir: %w", err)
	}

	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write IANA CSV: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("install IANA CSV: %w", err)
	}
	return nil
}

func parseCSV(r io.Reader) (DB, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	// skip header row
	if _, err := cr.Read(); err != nil {
		return nil, fmt.Errorf("read IANA CSV header: %w", err)
	}

	db := make(DB, 4096)
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if len(rec) < 4 {
			continue
		}
		field := func(i int) string {
			if i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		portStr := field(1)
		proto := strings.ToLower(field(2))

		// skip empty ports and ranges (e.g. "1024-65535")
		if portStr == "" || strings.ContainsRune(portStr, '-') {
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}

		e := Entry{
			ServiceName:             field(0),
			PortNumber:              portStr,
			TransportProtocol:       proto,
			Description:             field(3),
			Assignee:                field(4),
			Contact:                 field(5),
			RegistrationDate:        field(6),
			ModificationDate:        field(7),
			Reference:               field(8),
			ServiceCode:             field(9),
			UnauthorizedUseReported: field(10),
			AssignmentNotes:         field(11),
		}

		key := portKey{port, proto}
		// don't clobber a named entry with an unnamed one
		if existing, ok := db[key]; ok && existing.ServiceName != "" && e.ServiceName == "" {
			continue
		}
		db[key] = e
	}
	return db, nil
}
