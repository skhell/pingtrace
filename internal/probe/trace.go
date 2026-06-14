package probe

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// TraceOptions controls the Trace engine.
type TraceOptions struct {
	MaxHops    int
	Queries    int
	WaitMs     int
	PacketSize int
}

func (o TraceOptions) withDefaults() TraceOptions {
	if o.MaxHops <= 0 {
		o.MaxHops = 30
	}
	if o.Queries <= 0 {
		o.Queries = 3
	}
	if o.WaitMs <= 0 {
		o.WaitMs = 5000
	}
	if o.PacketSize <= 0 {
		o.PacketSize = 60
	}
	return o
}

// TraceEvent is emitted for each parsed hop as traceroute prints it.
type TraceEvent struct {
	Hop TraceHop
}

// TraceStream runs traceroute and streams parsed hops on `events`
// as they appear. The final aggregated TraceResult is sent on
// `done` (single value, then closed).
func TraceStream(ctx context.Context, target string, opts TraceOptions) (<-chan TraceEvent, <-chan TraceResult, error) {
	opts = opts.withDefaults()
	bin, args := traceCommand(target, opts)
	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("%s %s: %w", bin, strings.Join(args, " "), err)
	}
	events := make(chan TraceEvent, 4)
	done := make(chan TraceResult, 1)
	full := append([]string{bin}, args...)
	srcIP := OutboundIP(target)

	go func() {
		defer close(events)
		defer close(done)
		res := TraceResult{Command: strings.Join(full, " ")}
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			hop, ok := parseTraceLine(scanner.Text())
			if !ok {
				continue
			}
			hop.Source = srcIP
			hop.Target = target
			res.Hops = append(res.Hops, hop)
			select {
			case events <- TraceEvent{Hop: hop}:
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				return
			}
		}
		_ = cmd.Wait()
		done <- res
	}()

	return events, done, nil
}

// Trace is the buffered convenience wrapper.
func Trace(ctx context.Context, target string, opts TraceOptions) (TraceResult, error) {
	events, done, err := TraceStream(ctx, target, opts)
	if err != nil {
		return TraceResult{}, err
	}
	for range events {
	}
	return <-done, nil
}

var (
	// Hop line starts with TTL number, then everything after. Both
	// `traceroute` and `tracert` print this shape, so one regex
	// covers Unix and Windows.
	traceHopRe = regexp.MustCompile(`^\s*(\d+)\s+(.+)$`)
	// IPv4 or IPv6.
	hopIPRe = regexp.MustCompile(`([0-9]{1,3}(?:\.[0-9]{1,3}){3}|[0-9a-fA-F:]+:[0-9a-fA-F:]+)`)
	// Per-probe time or asterisk. Matches "1.234 ms" / "<1 ms" / "*".
	probeRe = regexp.MustCompile(`(?:\*|(?:<\s*)?([\d.]+)\s*ms)`)
	// Hostname in iputils/BSD form: "host.example.com (1.2.3.4)".
	unixHopHostRe = regexp.MustCompile(`([A-Za-z0-9][A-Za-z0-9._-]*[A-Za-z0-9])\s+\([0-9a-fA-F:.]+\)`)
	// Windows tracert form: "host.example.com [1.2.3.4]". Bracket
	// notation does not appear in Unix traceroute output, so this
	// regex is safe to run on every line regardless of GOOS.
	winHopHostRe = regexp.MustCompile(`([A-Za-z0-9][A-Za-z0-9._-]*[A-Za-z0-9])\s+\[[0-9a-fA-F:.]+\]`)
)

func parseTraceLine(line string) (TraceHop, bool) {
	if strings.TrimSpace(line) == "" {
		return TraceHop{}, false
	}
	m := traceHopRe.FindStringSubmatch(line)
	if m == nil {
		return TraceHop{}, false
	}
	hopNum, err := strconv.Atoi(m[1])
	if err != nil {
		return TraceHop{}, false
	}
	rest := m[2]
	hop := TraceHop{Hop: hopNum, Status: "timeout"}

	if ip := hopIPRe.FindString(rest); ip != "" {
		hop.IP = ip
		hop.Status = "ok"
	}
	if mh := unixHopHostRe.FindStringSubmatch(rest); mh != nil {
		hop.Host = mh[1]
	} else if mh := winHopHostRe.FindStringSubmatch(rest); mh != nil {
		hop.Host = mh[1]
	}

	// Collect up to 3 probe times.
	probes := probeRe.FindAllStringSubmatch(rest, -1)
	times := make([]float64, 0, 3)
	for _, p := range probes {
		if p[1] == "" {
			times = append(times, 0)
			continue
		}
		if v, perr := strconv.ParseFloat(p[1], 64); perr == nil {
			times = append(times, v)
		}
	}
	if len(times) >= 1 {
		hop.Probe1Ms = times[0]
		hop.TimeMs = times[0]
	}
	if len(times) >= 2 {
		hop.Probe2Ms = times[1]
	}
	if len(times) >= 3 {
		hop.Probe3Ms = times[2]
	}
	return hop, true
}

func traceCommand(target string, o TraceOptions) (string, []string) {
	if runtime.GOOS == "windows" {
		// Windows tracert: -d disables DNS; we want hostnames, so omit it.
		return "tracert", []string{
			"-h", strconv.Itoa(o.MaxHops),
			"-w", strconv.Itoa(o.WaitMs),
			target,
		}
	}
	waitSec := o.WaitMs / 1000
	if waitSec < 1 {
		waitSec = 1
	}
	// We deliberately omit -n so reverse DNS hostnames appear
	// alongside the IP. Linux iputils prints "host (ip)"; BSD
	// (macOS) prints the same when DNS resolves.
	args := []string{
		"-m", strconv.Itoa(o.MaxHops),
		"-q", strconv.Itoa(o.Queries),
		"-w", strconv.Itoa(waitSec),
		target,
		strconv.Itoa(o.PacketSize),
	}
	return "traceroute", args
}

func parseTraceOutput(out string) TraceResult {
	res := TraceResult{}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		if hop, ok := parseTraceLine(scanner.Text()); ok {
			res.Hops = append(res.Hops, hop)
		}
	}
	return res
}
