// Package cli wires Cobra commands for the pingtrace CLI.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/skhell/pingtrace/internal/config"
	"github.com/skhell/pingtrace/internal/csvexport"
	"github.com/skhell/pingtrace/internal/enrich"
	"github.com/skhell/pingtrace/internal/iana"
	"github.com/skhell/pingtrace/internal/jsonreport"
	"github.com/skhell/pingtrace/internal/portscan"
	"github.com/skhell/pingtrace/internal/probe"
	"github.com/skhell/pingtrace/internal/render"
	"github.com/skhell/pingtrace/internal/target"
	mtrtui "github.com/skhell/pingtrace/internal/tui/mtr"
)

// Version is overridden at build time via -ldflags.
var Version = "1.2.0"

type rootFlags struct {
	noPing   bool
	noTrace  bool
	mtr      bool
	cycles   int
	interval float64

	count          int
	packetSize     int
	timeoutMs      int
	pingIntervalMs int

	maxHops         int
	traceQueries    int
	traceWaitMs     int
	tracePacketSize int

	columns []string
	summary bool
	wide    bool
	noColor bool

	exportDir     string
	exportSet     bool
	jsonOut       bool
	compactExport bool

	ports           string // "" = no scan, "default" -> scan.ports config, else port spec
	portTimeout     int    // ms per TCP connect attempt (0 = use scan.timeout_ms)
	portConcurrency int    // concurrent TCP dials (0 = use scan.concurrency)

	file string

	tables      bool // force per-target tables even for CIDR / large lists
	noBulk      bool // skip bulk mode (synonym for --tables, kept for clarity)
	concurrency int  // workers in bulk mode (default 8)
}

func newRootCmd() *cobra.Command {
	f := &rootFlags{}

	root := &cobra.Command{
		Use:   "pingtrace [target...]",
		Short: "Cross-platform ping + traceroute + port scan + MTR in one command.",
		Long: "pingtrace runs ping, traceroute, and TCP port scan from one command.\n" +
			"Pass one or more targets (comma-separated, IPv4 CIDR, or via --file).\n" +
			"Use --mtr / -m for live MTR (Ctrl+C to stop).\n" +
			"Use --ports to TCP-scan open ports alongside ping + trace.\n\n" +
			"Engine defaults (count, packet size, timeouts, port list, etc.) can be\n" +
			"set persistently with `pingtrace config set`. CLI flags override the\n" +
			"config file for a single invocation.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && f.file == "" {
				return cmd.Help()
			}
			return runRoot(cmd, args, f)
		},
	}

	root.Flags().BoolVar(&f.noPing, "no-ping", false, "Skip the ping operation.")
	root.Flags().BoolVar(&f.noTrace, "no-trace", false, "Skip the traceroute operation.")
	root.Flags().BoolVarP(&f.mtr, "mtr", "m", false, "Run live MTR (continuous traceroute with running stats).")
	root.Flags().IntVar(&f.cycles, "cycles", 0, "Stop MTR after N cycles (0 = continuous). Overrides mtr.cycles.")
	root.Flags().Float64Var(&f.interval, "interval", 0, "Seconds between MTR cycles. Overrides mtr.interval_ms.")

	// Ping engine knobs (override config `ping.*`).
	root.Flags().IntVarP(&f.count, "count", "c", 0, "Ping echo requests per target. Overrides ping.count (default 4).")
	root.Flags().IntVar(&f.packetSize, "packet-size", 0, "Ping payload bytes. Overrides ping.packet_size (default 56).")
	root.Flags().IntVar(&f.timeoutMs, "timeout", 0, "Per-packet receive timeout in ms. Overrides ping.timeout_ms (default 1000).")
	root.Flags().IntVar(&f.pingIntervalMs, "ping-interval", 0, "Gap between ping sends in ms. Overrides ping.interval_ms (default 1000).")

	// Traceroute engine knobs (override config `trace.*`).
	root.Flags().IntVar(&f.maxHops, "max-hops", 0, "TTL ceiling for traceroute / MTR. Overrides trace.max_hops (default 30).")
	root.Flags().IntVar(&f.traceQueries, "queries", 0, "Probes per hop in traceroute. Overrides trace.queries (default 3).")
	root.Flags().IntVar(&f.traceWaitMs, "wait", 0, "Per-probe timeout for traceroute in ms. Overrides trace.wait_ms (default 5000).")
	root.Flags().IntVar(&f.tracePacketSize, "trace-packet-size", 0, "Traceroute probe size in bytes. Overrides trace.packet_size (default 60).")

	root.Flags().StringSliceVar(&f.columns, "columns", nil, "Render only these columns (snake_case or camelCase).")
	root.Flags().BoolVar(&f.summary, "summary", false, "One line per target instead of per-packet / per-hop.")
	root.Flags().BoolVar(&f.wide, "wide", false, "Disable terminal auto-fit; render at full natural width.")
	root.Flags().BoolVar(&f.noColor, "no-color", false, "Disable ANSI colors.")

	root.Flags().StringVar(&f.exportDir, "export", "", "Write CSV files to DIR. Use --export alone for current dir, --export=DIR or --export DIR for a path.")
	root.Flags().Lookup("export").NoOptDefVal = "."
	root.Flags().BoolVar(&f.jsonOut, "json", false, "Also write a JSON report (validates against schema/pingtrace.schema.json). Honors --export DIR; defaults to current dir.")
	root.Flags().BoolVar(&f.compactExport, "compact-export", false, "Omit columns that are entirely empty from CSV export (e.g. PeeringDB columns when PeeringDB is not configured).")

	root.Flags().StringVar(&f.file, "file", "", "Read targets from a CSV file (first column = target).")

	root.Flags().StringVar(&f.ports, "ports", "", "TCP ports to scan: comma list (22,80,443), range (1-1024), or omit value to use scan.ports config.")
	root.Flags().Lookup("ports").NoOptDefVal = "default"
	root.Flags().IntVar(&f.portTimeout, "port-timeout", 0, "Per-port TCP connect timeout in ms. Overrides scan.timeout_ms (default 1500).")
	root.Flags().IntVar(&f.portConcurrency, "scan-concurrency", 0, "Concurrent TCP dials per port scan. Overrides scan.concurrency (default 50).")

	root.Flags().BoolVar(&f.tables, "tables", false, "Force per-target tables (default for ≤4 hosts; CIDR / large lists use bulk mode otherwise).")
	root.Flags().IntVar(&f.concurrency, "concurrency", 8, "Parallel probes in bulk mode.")

	root.AddCommand(newConfigCmd())
	root.AddCommand(newCompletionCmd())
	root.CompletionOptions.DisableDefaultCmd = true

	root.SetHelpFunc(styledHelp)
	return root
}

// Execute is the CLI entry point.
func Execute() error {
	cmd := newRootCmd()
	cmd.SetArgs(normalizeOptionalFlags(os.Args[1:]))
	return cmd.Execute()
}

// normalizeOptionalFlags rewrites `--flag value` to `--flag=value` for
// flags that use NoOptDefVal (optional-value flags). Without this rewrite
// pflag treats the space-separated value as a positional argument.
func normalizeOptionalFlags(in []string) []string {
	optionalFlags := map[string]bool{
		"--export": true,
		"--ports":  true,
	}
	out := make([]string, 0, len(in))
	for i := 0; i < len(in); i++ {
		a := in[i]
		if optionalFlags[a] && i+1 < len(in) {
			next := in[i+1]
			if !strings.HasPrefix(next, "-") {
				out = append(out, a+"="+next)
				i++
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// --- run -------------------------------------------------------

// resolveEngineOpts merges defaults < config < CLI flags into the
// probe option structs used by runPingTrace / runMTR.
func resolveEngineOpts(cmd *cobra.Command, f *rootFlags) (probe.PingOptions, probe.TraceOptions, time.Duration, int, error) {
	eff, err := config.Effective()
	if err != nil {
		return probe.PingOptions{}, probe.TraceOptions{}, 0, 0, err
	}
	asInt := func(key string) int {
		if v, ok := eff[key].(float64); ok {
			return int(v)
		}
		return 0
	}
	pickInt := func(flagName string, override int, cfgKey string) int {
		if cmd.Flags().Changed(flagName) {
			return override
		}
		return asInt(cfgKey)
	}

	pingOpts := probe.PingOptions{
		Count:      pickInt("count", f.count, "ping.count"),
		PacketSize: pickInt("packet-size", f.packetSize, "ping.packet_size"),
		TimeoutMs:  pickInt("timeout", f.timeoutMs, "ping.timeout_ms"),
		IntervalMs: pickInt("ping-interval", f.pingIntervalMs, "ping.interval_ms"),
	}
	traceOpts := probe.TraceOptions{
		MaxHops:    pickInt("max-hops", f.maxHops, "trace.max_hops"),
		Queries:    pickInt("queries", f.traceQueries, "trace.queries"),
		WaitMs:     pickInt("wait", f.traceWaitMs, "trace.wait_ms"),
		PacketSize: pickInt("trace-packet-size", f.tracePacketSize, "trace.packet_size"),
	}

	var mtrInterval time.Duration
	if cmd.Flags().Changed("interval") {
		mtrInterval = time.Duration(f.interval * float64(time.Second))
	} else {
		mtrInterval = time.Duration(asInt("mtr.interval_ms")) * time.Millisecond
	}
	if mtrInterval <= 0 {
		mtrInterval = time.Second
	}

	mtrCycles := f.cycles
	if !cmd.Flags().Changed("cycles") {
		mtrCycles = asInt("mtr.cycles")
	}

	return pingOpts, traceOpts, mtrInterval, mtrCycles, nil
}

func runRoot(cmd *cobra.Command, args []string, f *rootFlags) error {
	if f.noPing && f.noTrace && !f.mtr {
		return errors.New("--no-ping and --no-trace together leave nothing to run")
	}

	render.ClearScreen()
	defer render.ShowCursor()

	pingOpts, traceOpts, mtrInterval, mtrCycles, err := resolveEngineOpts(cmd, f)
	if err != nil {
		return err
	}

	targets, err := target.Parse(args, f.file)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	useBulk := !f.mtr && shouldUseBulk(targets, f.tables, false)

	var csvW *csvexport.Writer
	var jsonW *jsonreport.Writer
	jsonDir := f.exportDir
	if jsonDir == "" {
		jsonDir = "."
	}
	srcIP := probe.OutboundIP(targets[0].Value)
	toLabel := filenameTarget(targets)
	if cmd.Flags().Changed("export") {
		f.exportSet = true
		csvW, err = csvexport.New(f.exportDir)
		if err != nil {
			return err
		}
		csvW.SetFromTo(srcIP, toLabel)
		csvW.SetCompact(f.compactExport)
	} else if useBulk {
		// Bulk mode auto-exports CSV to CWD even without --export, so the
		// user always walks away with a record of large fan-outs.
		csvW, err = csvexport.New(".")
		if err != nil {
			return err
		}
		csvW.SetFromTo(srcIP, toLabel)
		csvW.SetCompact(f.compactExport)
	}
	if f.jsonOut {
		jsonW, err = jsonreport.New(jsonDir, Version)
		if err != nil {
			return err
		}
		jsonW.SetFromTo(srcIP, toLabel)
		jsonW.SetTag(cidrFilenameTag(targets))
		for _, t := range targets {
			jsonW.AddTarget(jsonreport.Target{
				Value:         t.Value,
				Source:        t.Source,
				OriginalInput: t.OriginalInput,
			})
		}
	}
	if csvW != nil || jsonW != nil {
		defer finalizeWriters(cmd, csvW, jsonW)
	}

	if f.mtr {
		opts := render.Options{
			Columns: f.columns,
			Summary: f.summary,
			Wide:    f.wide,
			NoColor: f.noColor || !render.IsTTY(),
			Out:     cmd.OutOrStdout(),
		}
		return runMTR(ctx, targets, opts, csvW, jsonW, traceOpts, mtrInterval, mtrCycles)
	}

	// Parse --ports and load IANA once for the whole run.
	// Resolve scan engine opts: config defaults < CLI flag overrides.
	var scanPorts []int
	var ianaDB iana.DB
	if f.ports != "" {
		scanEff, _ := config.Effective()
		asInt := func(key string) int {
			if v, ok := scanEff[key].(float64); ok {
				return int(v)
			}
			return 0
		}
		// "default" is the NoOptDefVal sentinel: substitute with scan.ports config.
		portSpec := f.ports
		if portSpec == "default" {
			if v, _ := scanEff["scan.ports"].(string); v != "" {
				portSpec = v
			}
		}
		if !cmd.Flags().Changed("port-timeout") {
			f.portTimeout = asInt("scan.timeout_ms")
		}
		if f.portTimeout <= 0 {
			f.portTimeout = 1500
		}
		if !cmd.Flags().Changed("scan-concurrency") {
			f.portConcurrency = asInt("scan.concurrency")
		}
		if f.portConcurrency <= 0 {
			f.portConcurrency = 50
		}
		scanPorts, err = portscan.ParsePorts(portSpec)
		if err != nil {
			return fmt.Errorf("--ports: %w", err)
		}
		ianaURL, _ := scanEff["iana.url"].(string)
		if _, cached := iana.LastSyncTime(); !cached {
			fmt.Fprintln(cmd.OutOrStdout(), "IANA service database not cached; downloading (first run only)...")
		}
		var ianaErr error
		ianaDB, ianaErr = iana.LoadEffective(ianaURL)
		if ianaErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v; ports will show without service names\n", ianaErr)
		}
	}

	enr := buildEnrichers()
	hasPublicDNS := enr.dns.PublicEnabled() || enr.ipinfo.Enabled()
	hasPrivateDNS := enr.dns.PrivateEnabled()
	pingRenderOpts := render.Options{
		Columns:        f.columns,
		DefaultColumns: filterDNSColumns(render.PingAllColumns, hasPublicDNS, hasPrivateDNS),
		Summary:        f.summary,
		Wide:           f.wide,
		NoColor:        f.noColor || !render.IsTTY(),
		Out:            cmd.OutOrStdout(),
	}
	traceRenderOpts := pingRenderOpts
	traceRenderOpts.DefaultColumns = filterDNSColumns(render.TraceAllColumns, hasPublicDNS, hasPrivateDNS)

	if useBulk {
		return runBulk(ctx, targets, f, pingRenderOpts, traceRenderOpts, csvW, jsonW, pingOpts, traceOpts, enr, scanPorts, ianaDB)
	}
	return runPingTrace(ctx, targets, f, pingRenderOpts, traceRenderOpts, csvW, jsonW, pingOpts, traceOpts, enr, scanPorts, ianaDB)
}

// finalizeWriters closes csvW (so file paths/row counts are stable),
// then closes jsonW (feeding it CSV metadata for `exportedFiles`).
// Both calls are safe with nil inputs; whatever paths are produced
// get printed in a single "wrote:" block on stderr.
func finalizeWriters(cmd *cobra.Command, csvW *csvexport.Writer, jsonW *jsonreport.Writer) {
	var paths []string
	var exported []jsonreport.ExportedFile
	if csvW != nil {
		// Snapshot metadata before Close so RowCount is accurate.
		for _, fi := range csvW.Files() {
			exported = append(exported, jsonreport.ExportedFile{
				Operation: fi.Operation,
				Path:      fi.Path,
				RowCount:  fi.RowCount,
			})
		}
		csvPaths, _ := csvW.Close()
		paths = append(paths, csvPaths...)
	}
	if jsonW != nil {
		jsonPaths, _ := jsonW.Close(exported)
		paths = append(paths, jsonPaths...)
	}
	if len(paths) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "wrote:")
		for _, p := range paths {
			fmt.Fprintln(cmd.ErrOrStderr(), "  "+p)
		}
	}
}

// enrichers bundles the per-row enrichment clients used by the
// streaming pipeline. All clients short-circuit cheaply when their
// config is empty so callers can call them unconditionally.
type enrichers struct {
	dns    *enrich.Resolver
	ipinfo *enrich.IPInfoClient
	pdb    *enrich.PeeringDBClient
}

// buildEnrichers constructs DNS + ipinfo + peeringdb clients from
// persisted config. Empty/missing values produce disabled clients.
func buildEnrichers() enrichers {
	eff, err := config.Effective()
	if err != nil {
		return enrichers{
			dns:    enrich.New(nil, nil, 0),
			ipinfo: enrich.NewIPInfo("", 0),
			pdb:    enrich.NewPeeringDB("", 0),
		}
	}
	pubStr, _ := eff["dns.public"].(string)
	prvStr, _ := eff["dns.private"].(string)
	dnsTimeout := 250 * time.Millisecond
	if v, ok := eff["dns.timeout_ms"].(float64); ok && v > 0 {
		dnsTimeout = time.Duration(v) * time.Millisecond
	}
	tok, _ := eff["ipinfo.token"].(string)
	ipiTimeout := 800 * time.Millisecond
	if v, ok := eff["ipinfo.timeout_ms"].(float64); ok && v > 0 {
		ipiTimeout = time.Duration(v) * time.Millisecond
	}
	pdbTok, _ := eff["peeringdb.token"].(string)
	pdbTimeout := 1500 * time.Millisecond
	if v, ok := eff["peeringdb.timeout_ms"].(float64); ok && v > 0 {
		pdbTimeout = time.Duration(v) * time.Millisecond
	}
	return enrichers{
		dns:    enrich.New(enrich.ParseList(pubStr), enrich.ParseList(prvStr), dnsTimeout),
		ipinfo: enrich.NewIPInfo(tok, ipiTimeout),
		pdb:    enrich.NewPeeringDB(pdbTok, pdbTimeout),
	}
}

// filterDNSColumns removes public_dns / private_dns from a column list
// based on what is actually configured, so empty columns are never shown.
func filterDNSColumns(cols []string, hasPublicDNS, hasPrivateDNS bool) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		if c == "public_dns" && !hasPublicDNS {
			continue
		}
		if c == "private_dns" && !hasPrivateDNS {
			continue
		}
		out = append(out, c)
	}
	return out
}

// --- ping + trace ---------------------------------------------

// enrichReply fills DNS + ipinfo fields on a ping reply. Each
// lookup is gated by Enabled() so disabled clients incur no cost.
func enrichReply(ctx context.Context, p *probe.PingReply, enr enrichers) {
	if p.IP == "" {
		return
	}
	if enr.dns.Enabled() {
		if enrich.IsPrivateIP(p.IP) {
			if p.PrivateDNS == "" {
				p.PrivateDNS = enr.dns.LookupPrivate(ctx, p.IP)
			}
		} else {
			if p.PublicDNS == "" {
				p.PublicDNS = enr.dns.LookupPublic(ctx, p.IP)
			}
		}
	}
	if enr.ipinfo.Enabled() {
		info := enr.ipinfo.Lookup(ctx, p.IP)
		if p.PublicDNS == "" && info.Hostname != "" {
			p.PublicDNS = info.Hostname
		}
		if p.Hostname == "" {
			p.Hostname = info.Hostname
		}
		if p.ASN == "" {
			p.ASN = info.ASN
		}
		if p.Org == "" {
			p.Org = info.Org
		}
		if p.Location == "" {
			p.Location = info.FormatLocation()
		}
		if p.City == "" {
			p.City = info.City
		}
		if p.Region == "" {
			p.Region = info.Region
		}
		if p.Country == "" {
			p.Country = info.Country
		}
		if p.Loc == "" {
			p.Loc = info.Loc
		}
	}
	if enr.pdb.Enabled() && p.ASN != "" && (p.Policy == "" || p.NetType == "") {
		pi := enr.pdb.LookupASN(ctx, p.ASN)
		if p.Policy == "" {
			p.Policy = pi.Policy
		}
		if p.NetType == "" {
			p.NetType = pi.NetType
		}
		if p.PdbName == "" {
			p.PdbName = pi.Name
		}
		if p.Traffic == "" {
			p.Traffic = pi.Traffic
		}
		if p.Prefixes4 == 0 {
			p.Prefixes4 = pi.Prefixes4
		}
		if p.Prefixes6 == 0 {
			p.Prefixes6 = pi.Prefixes6
		}
		if p.IXPCount == 0 {
			p.IXPCount = pi.IXPCount
		}
	}
}

// enrichHop fills DNS + ipinfo fields on a trace hop and, when
// possible, also fills the human-readable Host column.
func enrichHop(ctx context.Context, h *probe.TraceHop, enr enrichers) {
	if h.IP == "" {
		return
	}
	if enr.dns.Enabled() {
		if enrich.IsPrivateIP(h.IP) {
			if h.PrivateDNS == "" {
				h.PrivateDNS = enr.dns.LookupPrivate(ctx, h.IP)
				if (h.Host == "" || h.Host == h.IP) && h.PrivateDNS != "" {
					h.Host = h.PrivateDNS
				}
			}
		} else {
			if h.PublicDNS == "" {
				h.PublicDNS = enr.dns.LookupPublic(ctx, h.IP)
				if (h.Host == "" || h.Host == h.IP) && h.PublicDNS != "" {
					h.Host = h.PublicDNS
				}
			}
		}
	}
	if enr.ipinfo.Enabled() {
		info := enr.ipinfo.Lookup(ctx, h.IP)
		if h.PublicDNS == "" && info.Hostname != "" {
			h.PublicDNS = info.Hostname
		}
		if (h.Host == "" || h.Host == h.IP) && info.Hostname != "" {
			h.Host = info.Hostname
		}
		if h.Hostname == "" {
			h.Hostname = info.Hostname
		}
		if h.ASN == "" {
			h.ASN = info.ASN
		}
		if h.Org == "" {
			h.Org = info.Org
		}
		if h.Location == "" {
			h.Location = info.FormatLocation()
		}
		if h.City == "" {
			h.City = info.City
		}
		if h.Region == "" {
			h.Region = info.Region
		}
		if h.Country == "" {
			h.Country = info.Country
		}
		if h.Loc == "" {
			h.Loc = info.Loc
		}
	}
	if enr.pdb.Enabled() && h.ASN != "" && (h.Policy == "" || h.NetType == "") {
		pi := enr.pdb.LookupASN(ctx, h.ASN)
		if h.Policy == "" {
			h.Policy = pi.Policy
		}
		if h.NetType == "" {
			h.NetType = pi.NetType
		}
		if h.PdbName == "" {
			h.PdbName = pi.Name
		}
		if h.Traffic == "" {
			h.Traffic = pi.Traffic
		}
		if h.Prefixes4 == 0 {
			h.Prefixes4 = pi.Prefixes4
		}
		if h.Prefixes6 == 0 {
			h.Prefixes6 = pi.Prefixes6
		}
		if h.IXPCount == 0 {
			h.IXPCount = pi.IXPCount
		}
	}
}

func runPingTrace(ctx context.Context, targets []target.Target, f *rootFlags, pingRenderOpts, traceRenderOpts render.Options, csvW *csvexport.Writer, jsonW *jsonreport.Writer, pingOpts probe.PingOptions, traceOpts probe.TraceOptions, enr enrichers, scanPorts []int, ianaDB iana.DB) error {
	for i, t := range targets {
		if i > 0 {
			fmt.Fprintln(pingRenderOpts.Out)
			fmt.Fprintln(pingRenderOpts.Out, strings.Repeat("-", 60))
		}
		if err := pingTraceOne(ctx, t, f, pingRenderOpts, traceRenderOpts, csvW, jsonW, pingOpts, traceOpts, enr, scanPorts, ianaDB); err != nil {
			fmt.Fprintf(pingRenderOpts.Out, "%s: %v\n", t.Value, err)
		}
	}
	return nil
}

// pingTraceOne streams ping and trace concurrently. To keep output
// readable we serialize the two tables: ping prints first (rows
// flushed as replies arrive), trace prints after. Trace is started
// at t=0 in the background so its first hops are ready by the time
// the ping section finishes.
func pingTraceOne(ctx context.Context, tgt target.Target, f *rootFlags, pingRenderOpts, traceRenderOpts render.Options, csvW *csvexport.Writer, jsonW *jsonreport.Writer, pingOpts probe.PingOptions, traceOpts probe.TraceOptions, enr enrichers, scanPorts []int, ianaDB iana.DB) error {
	target := tgt.Value
	// For ping, further filter DNS columns based on the target IP: a private
	// target will never populate public_dns and a public target will never
	// populate private_dns, so hide whichever column would always be empty.
	if enrich.IsPrivateIP(target) {
		for _, col := range []string{"public_dns", "org", "asn", "location", "net_type", "policy"} {
			pingRenderOpts.DefaultColumns = render.RemoveColumn(pingRenderOpts.DefaultColumns, col)
		}
	} else {
		pingRenderOpts.DefaultColumns = render.RemoveColumn(pingRenderOpts.DefaultColumns, "private_dns")
	}
	// Kick off trace early in parallel; we'll print its output once
	// the ping table is done.
	type traceCollect struct {
		hops    chan probe.TraceHop
		res     chan probe.TraceResult
		started time.Time
	}
	var tc *traceCollect
	if !f.noTrace {
		evCh, doneCh, err := probe.TraceStream(ctx, target, traceOpts)
		if err != nil {
			fmt.Fprintf(traceRenderOpts.Out, "trace %s: %v\n", target, err)
		} else {
			tc = &traceCollect{
				hops:    make(chan probe.TraceHop, 64),
				res:     make(chan probe.TraceResult, 1),
				started: time.Now(),
			}
			go func() {
				defer close(tc.hops)
				for e := range evCh {
					tc.hops <- e.Hop
				}
				tc.res <- <-doneCh
			}()
		}
	}

	// firstEnriched captures the first successful ping reply; it carries
	// the enrichment data (ipinfo, PeeringDB) reused in the scan CSV row.
	var firstEnriched *probe.PingReply

	if !f.noPing {
		evCh, doneCh, err := probe.PingStream(ctx, target, pingOpts)
		if err != nil {
			fmt.Fprintf(pingRenderOpts.Out, "ping %s: %v\n", target, err)
		} else {
			pingStart := time.Now()
			fmt.Fprintln(pingRenderOpts.Out, render.SectionHeading("PING "+target, pingRenderOpts.NoColor))
			if f.summary {
				prog := render.NewProgress(pingRenderOpts.Out, "pinging "+target+" · waiting for first reply", pingRenderOpts.NoColor)
				prog.Start()
				received := 0
				var enrichedPackets []probe.PingReply
				for ev := range evCh {
					enrichReply(ctx, &ev.Reply, enr)
					enrichedPackets = append(enrichedPackets, ev.Reply)
					received++
					prog.Update(fmt.Sprintf("ping %s · received %d", target, received))
				}
				res := <-doneCh
				res.Packets = enrichedPackets
				if p := firstOKReply(res.Packets); p != nil {
					firstEnriched = p
				}
				prog.Stop()
				render.PingStreamFooter(pingRenderOpts.Out, res)
				durMs := time.Since(pingStart).Milliseconds()
				if csvW != nil {
					_ = csvW.PingSummary(target, res)
				}
				if jsonW != nil {
					jsonW.AppendPing(target, tgt.Source, durMs, res)
				}
			} else {
				st := render.NewPingStreamTable(pingRenderOpts.Out, pingRenderOpts)
				st.StartStatus("pinging " + target + " · waiting for first reply")
				received := 0
				var enrichedPackets []probe.PingReply
				for ev := range evCh {
					enrichReply(ctx, &ev.Reply, enr)
					render.PingStreamRow(st, ev.Reply)
					enrichedPackets = append(enrichedPackets, ev.Reply)
					received++
					st.SetStatus(fmt.Sprintf("ping %s · received %d", target, received))
				}
				res := <-doneCh
				res.Packets = enrichedPackets
				if p := firstOKReply(res.Packets); p != nil {
					firstEnriched = p
				}
				st.Close()
				render.PingStreamFooter(pingRenderOpts.Out, res)
				durMs := time.Since(pingStart).Milliseconds()
				if csvW != nil {
					_ = csvW.Ping(target, res)
				}
				if jsonW != nil {
					jsonW.AppendPing(target, tgt.Source, durMs, res)
				}
			}
		}
	}

	// Kick off port scan concurrently while trace is printing (or right
	// after ping if trace is disabled).
	type scanCollect struct{ results []portscan.Result }
	var scanCh chan scanCollect
	if len(scanPorts) > 0 {
		scanCh = make(chan scanCollect, 1)
		go func() {
			timeout := time.Duration(f.portTimeout) * time.Millisecond
			results := portscan.Scan(ctx, target, scanPorts, timeout, f.portConcurrency, ianaDB)
			scanCh <- scanCollect{results}
		}()
	}

	if tc != nil {
		if !f.noPing {
			fmt.Fprintln(traceRenderOpts.Out)
		}
		fmt.Fprintln(traceRenderOpts.Out, render.SectionHeading("TRACE "+target, traceRenderOpts.NoColor))
		if f.summary {
			prog := render.NewProgress(traceRenderOpts.Out, "tracing "+target+" · waiting for first hop", traceRenderOpts.NoColor)
			prog.Start()
			hopsCount := 0
			var enrichedHops []probe.TraceHop
			for hop := range tc.hops {
				enrichHop(ctx, &hop, enr)
				enrichedHops = append(enrichedHops, hop)
				hopsCount++
				prog.Update(fmt.Sprintf("trace %s · hop %d", target, hopsCount))
			}
			res := <-tc.res
			res.Hops = enrichedHops
			prog.Stop()
			render.TraceStreamFooter(traceRenderOpts.Out, res)
			durMs := time.Since(tc.started).Milliseconds()
			if csvW != nil {
				_ = csvW.Trace(target, res)
			}
			if jsonW != nil {
				jsonW.AppendTrace(target, tgt.Source, durMs, res)
			}
		} else {
			st := render.NewTraceStreamTable(traceRenderOpts.Out, traceRenderOpts)
			st.StartStatus("tracing " + target + " · waiting for first hop")
			hopsCount := 0
			var enrichedHops []probe.TraceHop
			for hop := range tc.hops {
				enrichHop(ctx, &hop, enr)
				render.TraceStreamRow(st, hop)
				enrichedHops = append(enrichedHops, hop)
				hopsCount++
				st.SetStatus(fmt.Sprintf("trace %s · hop %d", target, hopsCount))
			}
			res := <-tc.res
			res.Hops = enrichedHops
			st.Close()
			render.TraceStreamFooter(traceRenderOpts.Out, res)
			durMs := time.Since(tc.started).Milliseconds()
			if csvW != nil {
				_ = csvW.Trace(target, res)
			}
			if jsonW != nil {
				jsonW.AppendTrace(target, tgt.Source, durMs, res)
			}
		}
	}
	// Print port scan results (waited until after ping+trace so the
	// section appears at the bottom, not mid-stream).
	if scanCh != nil {
		prog := render.NewProgress(pingRenderOpts.Out,
			fmt.Sprintf("scanning %d ports on %s", len(scanPorts), target),
			pingRenderOpts.NoColor)
		prog.Start()
		sc := <-scanCh
		prog.Stop()
		fmt.Fprintln(pingRenderOpts.Out)
		render.ScanSection(pingRenderOpts.Out, target, sc.results, f.wide, pingRenderOpts.NoColor)
		if csvW != nil {
			src := ""
			if firstEnriched != nil {
				src = firstEnriched.Source
			}
			_ = csvW.Scan(src, target, sc.results)
		}
	}
	return nil
}

// firstOKReply returns a pointer to the first successful ping reply, or nil.
func firstOKReply(packets []probe.PingReply) *probe.PingReply {
	for i := range packets {
		if packets[i].Status == "ok" {
			return &packets[i]
		}
	}
	return nil
}

func runMTR(ctx context.Context, targets []target.Target, opts render.Options, csvW *csvexport.Writer, jsonW *jsonreport.Writer, traceOpts probe.TraceOptions, interval time.Duration, cycles int) error {
	if len(targets) > 1 {
		for i, t := range targets {
			if i > 0 {
				fmt.Fprintln(opts.Out)
			}
			if err := mtrOne(ctx, t.Value, opts, csvW, jsonW, traceOpts, interval, cycles); err != nil {
				return err
			}
		}
		return nil
	}
	return mtrOne(ctx, targets[0].Value, opts, csvW, jsonW, traceOpts, interval, cycles)
}

func mtrOne(ctx context.Context, target string, opts render.Options, csvW *csvexport.Writer, jsonW *jsonreport.Writer, traceOpts probe.TraceOptions, interval time.Duration, cycles int) error {
	updates := probe.MTR(ctx, target, cycles, interval, traceOpts)

	piped := make(chan probe.MTRUpdate, 4)
	var lastSnap probe.MTRResult
	var lastCmd string
	var snapMu sync.Mutex
	go func() {
		defer close(piped)
		for u := range updates {
			if csvW != nil {
				_ = csvW.MTRCycle(target, u.Cycle, u.Snapshot)
			}
			snapMu.Lock()
			lastSnap = u.Snapshot
			snapMu.Unlock()
			piped <- u
		}
	}()

	recordMTR := func(snap probe.MTRResult) {
		if jsonW == nil {
			return
		}
		var planned *int
		if cycles > 0 {
			c := cycles
			planned = &c
		}
		jsonW.AppendMTR(target, planned, interval.Milliseconds(), lastCmd, snap)
	}

	if render.IsTTY() && !opts.NoColor {
		p := mtrtui.NewProgram(target, cycles, piped, opts)
		finalModel, err := p.Run()
		if err != nil {
			return err
		}
		// Alt-screen wipes the terminal on exit, so the live MTR
		// table vanishes. Reprint the final snapshot to the main
		// screen so users still see their results.
		if m, ok := finalModel.(mtrtui.Model); ok {
			snap := m.Snapshot()
			if snap.Cycles > 0 {
				render.MTR(target, snap, opts)
				recordMTR(snap)
				return nil
			}
		}
		snapMu.Lock()
		snap := lastSnap
		snapMu.Unlock()
		if snap.Cycles > 0 {
			recordMTR(snap)
		}
		return nil
	}
	final := mtrtui.RunFallback(ctx, target, piped, opts)
	render.MTR(target, final, opts)
	recordMTR(final)
	return nil
}
