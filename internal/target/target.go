// Package target turns user input (positional args + --file) into a flat,
// de-duplicated, order-preserving list of probe targets.
//
// Accepted forms:
//   - bare host or IP: "1.1.1.1", "example.com", "2606:4700:4700::1111"
//   - comma list: "1.1.1.1,1.1.1.1,example.com"
//   - IPv4 CIDR: "10.0.0.0/30" (expanded to host addresses)
//   - --file <path>: CSV with the target in the first column; lines
//     beginning with "#" and empty lines are ignored. The first column
//     may itself be a comma list or a CIDR.
package target

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
)

// Target is a single resolvable endpoint plus provenance.
type Target struct {
	Value         string
	Source        string // "argument" | "file" | "cidr"
	OriginalInput string
}

// MaxCIDRHosts caps CIDR expansion to keep rendering manageable.
const MaxCIDRHosts = 256

// Parse consolidates positional args and an optional file path into
// a deduplicated slice of Target.
func Parse(args []string, filePath string) ([]Target, error) {
	out := make([]Target, 0, 8)
	seen := make(map[string]struct{})

	add := func(more []Target) {
		for _, t := range more {
			if _, dup := seen[t.Value]; dup {
				continue
			}
			seen[t.Value] = struct{}{}
			out = append(out, t)
		}
	}

	for _, a := range args {
		got, err := expandToken(a, "argument")
		if err != nil {
			return nil, err
		}
		add(got)
	}

	if filePath != "" {
		got, err := readFile(filePath)
		if err != nil {
			return nil, err
		}
		add(got)
	}

	if len(out) == 0 {
		return nil, errors.New("no targets supplied")
	}
	return out, nil
}

func expandToken(tok, source string) ([]Target, error) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return nil, nil
	}
	if strings.Contains(tok, ",") {
		var out []Target
		for _, part := range strings.Split(tok, ",") {
			more, err := expandToken(part, source)
			if err != nil {
				return nil, err
			}
			out = append(out, more...)
		}
		return out, nil
	}
	if strings.Contains(tok, "/") && !strings.Contains(tok, "://") {
		return expandCIDR(tok, source)
	}
	return []Target{{Value: tok, Source: source, OriginalInput: tok}}, nil
}

func expandCIDR(cidr, source string) ([]Target, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	if ipnet.IP.To4() == nil {
		return nil, fmt.Errorf("IPv6 CIDR not supported yet: %s", cidr)
	}
	ones, bits := ipnet.Mask.Size()
	hosts := 1 << (bits - ones)
	if hosts > MaxCIDRHosts {
		return nil, fmt.Errorf("CIDR %s would expand to %d hosts (max %d)", cidr, hosts, MaxCIDRHosts)
	}
	out := make([]Target, 0, hosts)
	ip := ipnet.IP.Mask(ipnet.Mask).To4()
	for i := 0; i < hosts; i++ {
		cur := net.IPv4(ip[0], ip[1], ip[2], ip[3]).To4()
		keep := true
		if hosts >= 4 && (i == 0 || i == hosts-1) {
			keep = false
		}
		if keep {
			out = append(out, Target{Value: cur.String(), Source: "cidr", OriginalInput: cidr})
		}
		for j := 3; j >= 0; j-- {
			ip[j]++
			if ip[j] != 0 {
				break
			}
		}
	}
	return out, nil
}

func readFile(path string) ([]Target, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.Comment = '#'

	var out []Target
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if len(row) == 0 {
			continue
		}
		tok := strings.TrimSpace(row[0])
		if tok == "" {
			continue
		}
		got, err := expandToken(tok, "file")
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}
