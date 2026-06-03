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

// PingOptions controls the Ping engine. Zero values are filled in
// from the package defaults (count=4, packet 56B, 1s timeout, 1s
// interval).
type PingOptions struct {
	Count      int
	PacketSize int
	TimeoutMs  int
	IntervalMs int
}

func (o PingOptions) withDefaults() PingOptions {
	if o.Count <= 0 {
		o.Count = 4
	}
	if o.PacketSize <= 0 {
		o.PacketSize = 56
	}
	if o.TimeoutMs <= 0 {
		o.TimeoutMs = 1000
	}
	if o.IntervalMs <= 0 {
		o.IntervalMs = 1000
	}
	return o
}

// Ping is a convenience wrapper that drains PingStream and returns
// the final aggregated PingResult. Most callers should prefer
// PingStream for live output.
func Ping(ctx context.Context, target string, opts PingOptions) (PingResult, error) {
	events, done, err := PingStream(ctx, target, opts)
	if err != nil {
		return PingResult{}, err
	}
	for range events {
	}
	return <-done, nil
}

func pingCommand(target string, o PingOptions) (string, []string) {
	if runtime.GOOS == "windows" {
		return "ping", []string{
			"-n", strconv.Itoa(o.Count),
			"-l", strconv.Itoa(o.PacketSize),
			"-w", strconv.Itoa(o.TimeoutMs),
			target,
		}
	}
	args := []string{
		"-c", strconv.Itoa(o.Count),
		"-s", strconv.Itoa(o.PacketSize),
	}
	intervalSec := float64(o.IntervalMs) / 1000.0
	args = append(args, "-i", strconv.FormatFloat(intervalSec, 'f', 2, 64))
	if runtime.GOOS == "darwin" {
		// BSD ping: -W in milliseconds.
		args = append(args, "-W", strconv.Itoa(o.TimeoutMs))
	} else {
		// iputils ping: -W in whole seconds.
		w := o.TimeoutMs / 1000
		if w < 1 {
			w = 1
		}
		args = append(args, "-W", strconv.Itoa(w))
	}
	args = append(args, target)
	return "ping", args
}

var (
	// Unix ping reply. Handles both "from IP:" (IP target or BSD)
	// and "from HOST (IP):" (iputils when pinging a hostname).
	unixReplyRe = regexp.MustCompile(`(?i)(?P<bytes>\d+)\s+bytes\s+from\s+(?:[^()\s]+\s+\()?(?P<ip>[\da-fA-F.:]+?)\)?:\s+.*icmp_seq=(?P<seq>\d+).*ttl=(?P<ttl>\d+).*time=(?P<time>[\d.]+)\s*ms`)
	winReplyRe  = regexp.MustCompile(`(?i)Reply from\s+(?P<ip>[\da-fA-F:.]+):\s*bytes=(?P<bytes>\d+)\s+time[<=](?P<time>[\d.]+)ms\s+TTL=(?P<ttl>\d+)`)
)

// PingEvent is delivered for every echo reply parsed from the live
// ping output. emitted as the kernel hands them to us so the
// renderer can show rows in real time.
type PingEvent struct {
	Reply PingReply
}

// PingStream runs ping and streams parsed replies on the returned
// channel. The channel is closed when ping exits. The final aggregated PingResult is sent on `done` (single value, then closed).
func PingStream(ctx context.Context, target string, opts PingOptions) (<-chan PingEvent, <-chan PingResult, error) {
	opts = opts.withDefaults()
	bin, args := pingCommand(target, opts)
	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("%s %s: %w", bin, strings.Join(args, " "), err)
	}

	events := make(chan PingEvent, 4)
	done := make(chan PingResult, 1)
	full := append([]string{bin}, args...)

	go func() {
		defer close(events)
		defer close(done)

		res := PingResult{Command: strings.Join(full, " "), Sent: opts.Count, MinMs: -1}
		var sumMs float64
		scanner := bufio.NewScanner(stdout)
		seq := 0
		for scanner.Scan() {
			line := scanner.Text()
			reply, ok := parsePingLine(line, seq)
			if !ok {
				continue
			}
			seq++
			res.Packets = append(res.Packets, reply)
			res.Received++
			sumMs += reply.TimeMs
			if res.MinMs < 0 || reply.TimeMs < res.MinMs {
				res.MinMs = reply.TimeMs
			}
			if reply.TimeMs > res.MaxMs {
				res.MaxMs = reply.TimeMs
			}
			select {
			case events <- PingEvent{Reply: reply}:
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				return
			}
		}
		_ = cmd.Wait()

		if res.MinMs < 0 {
			res.MinMs = 0
		}
		if res.Received > 0 {
			res.AvgMs = sumMs / float64(res.Received)
		}
		if res.Sent > 0 {
			res.LossPct = float64(res.Sent-res.Received) / float64(res.Sent) * 100
		}
		done <- res
	}()

	return events, done, nil
}

// parsePingLine extracts a single PingReply from one line of ping
// output (any OS). Returns ok=false if the line is not a reply.
func parsePingLine(line string, seq int) (PingReply, bool) {
	if m := unixReplyRe.FindStringSubmatch(line); m != nil {
		bytes, _ := strconv.Atoi(m[1])
		ttl, _ := strconv.Atoi(m[4])
		t, _ := strconv.ParseFloat(m[5], 64)
		return PingReply{Seq: seq, Bytes: bytes, IP: m[2], TTL: ttl, TimeMs: t, Status: "ok"}, true
	}
	if m := winReplyRe.FindStringSubmatch(line); m != nil {
		bytes, _ := strconv.Atoi(m[2])
		t, _ := strconv.ParseFloat(m[3], 64)
		ttl, _ := strconv.Atoi(m[4])
		return PingReply{Seq: seq, Bytes: bytes, IP: m[1], TTL: ttl, TimeMs: t, Status: "ok"}, true
	}
	return PingReply{}, false
}
