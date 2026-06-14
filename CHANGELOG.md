# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.1.0] - 2026-06-14

### Added
- `source` and `target` columns in ping and traceroute tables (CLI and export files). `source` is the local outbound IP resolved via routing-table lookup (no packets sent); `target` is the hostname or IP passed on the command line. Both appear on every row, making multi-target and bulk-mode exports self-describing without needing to cross-reference the filename. When exports from multiple jump hosts are merged into one dataset, rows can be split or filtered by `source`.
- `--compact-export` flag: omits columns that are entirely empty across all rows from the CSV output. Column set is determined on the first write to each file and held consistent for the rest of the run. Typical use: skips all PeeringDB columns (`net_type`, `policy`, `pdb_name`, `traffic`, `prefixes_v4`, `prefixes_v6`, `ixp_count`) when PeeringDB is not configured, and all ipinfo columns when no token is set.
- Export filenames now include source and target: `ping_from_<src>_to_<target>_UTC<stamp>.csv` and `trace_from_<src>_to_<target>_UTC<stamp>.csv`. For CIDR/bulk runs the target segment is the sanitized CIDR notation (e.g. `10.0.0.0_24`); for multiple plain targets it is the first value plus a count (`1.1.1.1+2more`). Source is the local outbound IP resolved at startup.

### Changed
- Ping CSV and CLI column order updated: `seq, bytes, source, target, ttl, time_ms, ...` (was `seq, bytes, ip, ttl, time_ms, ...`). The `ip` column is removed from ping, the `target` column carries the same information.
- Traceroute CSV and CLI column order updated: `hop, source, target, hostname, host_ip, probe_1_ms, ...` (was `hop, host, ip, probe_1_ms, ...`). The per-hop IP is renamed from `ip` to `host_ip` to distinguish it from the probe target.
- `host` column renamed to `hostname` in traceroute output (CLI table header and CSV). The value is unchanged - it is still the reverse DNS hostname from the traceroute output.
- CSV `hostname` column (ipinfo PTR record) renamed to `ipinfo_hostname` to avoid collision with the promoted `hostname` column (traceroute-parsed hostname).

## [1.0.1] - 2026-06-03

### Fixed
- CSV and JSON exports now include enrichment data (DNS, org, ASN, location, PeeringDB). Enriched values were previously lost because the probe result from the stream was written to disk before enrichment was applied.
- Private IP addresses now populate the `private_dns` column instead of `public_dns`. DNS lookups are now routed to the correct field based on IP class (RFC1918 -> `private_dns`, public -> `public_dns`) for both ping replies and traceroute hops.
- `private_dns` column was missing from the default ping and trace table layouts and never rendered. Both tables now include it alongside `public_dns`.

## [1.0.0] - 2026-06-03

### Deprecated (NPM)

Starting with 1.0.0, pingtrace is distributed as a single static Go binary, replacing the original npm library (v0.2.0 and earlier - see history below). No Node.js / npm runtime required. Works on locked-down corporate machines where `npm install -g` is not an option. Single static binary, trivial to ship via Homebrew / Scoop / curl / Docker / Ansible. ~10x faster startup, much lower memory, real concurrency for bulk probes. First-class TUI via the Charmbracelet stack (bubbletea, lipgloss, bubbles). The OS `ping` / `traceroute` binaries are still the source of truth - pingtrace just runs them concurrently, parses the output, decorates it, and presents it in a clear table format.

This is the first stable release of the Go rewrite and the new baseline for semantic versioning. It is a strict superset of npm 0.2.0 (live MTR, CIDR bulk mode, PeeringDB enrichment, interactive config TUI) plus the engine change - large enough in scope to warrant a clean 1.0 rather than continuing the 0.x line. Breaking CLI/config changes will bump the major; new features bump the minor.

### Added

#### Probes
- Streaming probe layer (`probe.PingStream`, `probe.TraceStream`). Replies and hops are parsed and rendered the instant the underlying binary prints them. First row appears in ~50-100ms (native ping's own RTT) with no overhead from the CLI wrapper.
- Cross-platform parsers verified by fixture tests and a GitHub Actions matrix (`ubuntu-latest`, `macos-latest`, `windows-latest`) that builds the binary and runs real `ping` / `traceroute` / `tracert` against 1.1.1.1 on every push. The fixture suite covers Linux iputils (both IP-only replies and the `host (ip)` form emitted when the target is a hostname), BSD ping (macOS), IPv6 replies, Windows ping (`time=Nms` and `time<1ms`), and Windows tracert (`host [ip]` form plus all-timeout hops).
- Live MTR (`--mtr` / `-m`) with `--cycles` and `--interval`. Bubbletea TUI for TTYs (with a pre-cycle loading spinner + hint while the first cycle completes), line-per-cycle fallback for non-TTY.
- Granular engine config keys with matching CLI flags:
  - Ping: `ping.count`, `ping.packet_size`, `ping.timeout_ms`,`ping.interval_ms` → `--count`, `--packet-size`,`--timeout`, `--ping-interval`.
  - Trace: `trace.max_hops`, `trace.queries`, `trace.wait_ms`, `trace.packet_size` → `--max-hops`, `--queries`, `--wait`, `--trace-packet-size`.
  - MTR: `mtr.interval_ms`, `mtr.cycles`.
- Precedence chain: CLI flag > env var > config file > built-in default.

#### Targets
- Multi-target input: comma-separated list, IPv4 CIDR and `--file <path>` (CSV; first column = target, extra columns reserved for future per-target overrides).
- **Bulk mode**: triggered automatically by any CIDR or by ≥5 targets. Worker pool (`--concurrency`, default 8), live progress bar (bubbles spinner + percentage + current target + elapsed timer), auto-CSV in the current directory, and a bordered SUMMARY table at the end (top fastest + first unreachable). Opt out with `--tables` to force per-target bordered tables.

#### Enrichment
- DNS enrichment with **split-horizon** support: configurable `dns.public` and `dns.private` resolver lists, auto-routing by IP class (RFC1918, loopback, link-local, ULA, CGNAT). Result lands in the `public_dns` column. `dns.timeout_ms` bounds the lookup.
- ipinfo.io enrichment (hostname, org, asn, city, region, country, loc) with a sync.Map cache and configurable `ipinfo.timeout_ms`.
- PeeringDB enrichment (policy, net_type, pdb_name, traffic, prefixes_v4, prefixes_v6, ixp_count). Anonymous reads supported; token raises the rate limit. Cache keyed by ASN.

#### Rendering
- Rounded lipgloss bordered tables for ping, trace, MTR, and bulk-mode summary. Cyan section headings, color-coded status cells.
- Persistent bottom border on streaming tables: each new row is inserted above the bottom rule via ANSI cursor control so the table never looks unfinished while data streams in.
- Integrated **animated status footer** beneath the bottom border: bubbles spinner + label + elapsed timer, refreshed every 100 ms. Tells the user the process is alive between rows.
- Width-aware renderer: drops `policy` → `net_type` → `private_dns` → `location` → `asn` → `org` → `public_dns` in that priority order to fit the terminal, instead of wrapping cells.
- `--columns`, `--summary`, `--wide`, `--no-color`, terminal-clear on start, cursor hidden during streaming (restored on exit and on Ctrl+C).
- Grouped, color-coded `--help` (lipgloss section headings, orange flag labels, green examples block, dim defaults, tip pointing users at `pingtrace config`).

#### Configuration
- `pingtrace config` (no subcommand) launches an interactive bubbletea TUI on a TTY: arrow keys to navigate, Enter/`e` to edit, `u` to unset, `q` to quit. Categorized list with descriptions and signup hints for ipinfo / PeeringDB.
- CLI subcommands `config list / get / set / unset / path / edit` with env overrides, token redaction in `list`, and categorized output.

#### Export
- `--export [DIR]` CSV writer. Separate files per operation,
  UTC-timestamped:
  - `ping_UTC<YYYY-MM-DD-HH-MM-SS>.csv` - one row per packet.
  - `trace_UTC<YYYY-MM-DD-HH-MM-SS>.csv` - one row per hop.
  - `mtr_UTC<YYYY-MM-DD-HH-MM-SS>.csv` - one row per hop per cycle when `--mtr` is used.
- Full 24-column enrichment in CSV: target, seq/hop, bytes, ip, ttl, time_ms/probe_*_ms, public_dns, private_dns, hostname, org, asn, location, city, region, country, loc, net_type, policy, pdb_name, traffic, prefixes_v4, prefixes_v6, ixp_count, status. No truncation - CSV is for downstream analysis.
- `csvexport.Writer` is thread-safe (sync.Mutex) so the bulk worker pool can write concurrently without garbled rows.
- `--json` writes a JSON report alongside CSV that validates against `schema/pingtrace.schema.json`. Two top-level shapes are emitted depending on mode:
  - **probe**: one `probe[_TAG]_UTC<stamp>.json` per run with `targets`, per-target `results` (ping + trace), and an `exportedFiles` section listing sibling CSVs and their row counts. Each row carries the same DNS / org / ASN / location / `net_type` / `policy` enrichment shown in the table UI.
  - **mtr**: one `mtr_<target>[_TAG]_UTC<stamp>.json` per MTR target with final per-hop stats (`sent`, `lost`, `lossPercent`, `last`, `best`, `worst`, `avg`, `stdev`). `--json` honors `--export DIR` for the output directory and defaults to the current directory otherwise. CSV export remains independent: pass `--export` alone for CSV only, `--json` alone for JSON only, or both for both.Fins

---

> [!NOTE]
> Below this line: changelog for the initial **npm distribution** of pingtrace. Kept here for historical reference; the Go CLI carries everything in npm 0.2.0 forward.

## [0.2.0] - 2026-05-29

Minor version bump (rather than patch) because the CSV export schema changed shape (one row per packet/hop instead of one row per probe), which is a behavior change for any script consuming the export.

### Added
- Responsive table rendering: ping and traceroute tables now adapt to the terminal width. Columns are dropped by priority (`policy`, `net_type`, `private_dns`, `location`, `asn`, `org`, `public_dns` in that order) and long values are truncated with an ellipsis instead of wrapping. Essentials (`seq`/`hop`, `ip`, `time`/`probe_*_ms`, `status`) are always preserved.
- `--wide` flag to opt out of responsive sizing and render full-width columns (previous behavior). Useful when piping to a file or viewing on a wide screen.
- `--columns <list>` flag to render only an explicit comma-separated set of columns (e.g. `--columns seq,ip,time_ms,status`). Useful for script-friendly output.
- Polished CLI header and footer powered by `@clack/prompts` (`intro`/`outro`/`log`), with automatic plain-text fallback when running under CI (detected via `ci-info`).
- Bulk mode: when a target set exceeds 254 hosts (larger than a /24 CIDR), pingtrace automatically switches to bulk mode. In bulk mode: streaming tables are disabled, up to 10 probes run concurrently, and a CSV is auto-exported to the current directory without requiring `--export`. A compact one-line summary per target is printed to the console as probes complete.

### Changed
- CSV export (`--export`) now writes one row per ping packet and one row per traceroute hop, with all enrichment columns included (target, DNS, org, ASN, location, net_type, policy). Previously exported only a single summary row per probe. Falls back to summary-only rows when running with `--summary`.
- README restructured for scannability: added badges, "Why pingtrace" comparison table, `npx` install option, grouped Quick Start (Targets / Operations / Output / Export), CSV input format example, and expanded command cheatsheet covering all flags.
- `--help` output grouped by purpose (input -> operations -> output -> export), with clearer flag descriptions, refreshed examples covering all flags, and a pointer to `pingtrace config --help`.

## [0.1.12] - 2026-03-20

### Added
- PeeringDB enrichment: when `providers.peeringdbEnabled` is set to `true`, each traceroute and ping hop is enriched with network type (NSP, Content, IXP, Enterprise, etc.) and peering policy (Open, Selective, Restrictive) sourced from the PeeringDB public API. Results are cached by ASN to minimize latency. Two new columns - `net_type` and `policy` - appear in the output table when enabled.
- Private DNS timeout guardrail: if a private DNS server does not respond within 5 seconds, a yellow warning is printed to the console and private DNS enrichment is skipped for all remaining targets, allowing pingtrace to proceed without interruption.

## [0.1.11] - 2026-03-20

### Added
- Update notifier: when a newer version of `pingtrace` is available on npm, a notification is shown at startup.
- Streaming output: ping packets and traceroute hops are now rendered row by row in real-time as they arrive, instead of waiting for the full operation to complete.
- `--summary` flag for compact output mode when detailed tables are not needed.
- Enrichment columns (DNS, org, ASN, location) are now shown conditionally - only when the corresponding DNS servers or ipinfo token are configured, keeping the default table width manageable.
- When more than 8 targets are loaded (CIDR expansion or large CSV), the plan header collapses the list into a grouped count per source instead of printing every address.
- Ping and traceroute now stream each row in true real-time by running `ping -c 1` per packet and `traceroute -f <hop> -m <hop>` per hop on Unix, bypassing the C library's full-buffering on pipes. Windows falls back to promise-chain streaming.

### Changed
- Detailed table output is now the default behavior. No flag is required to see per-packet and per-hop tables.
- Removed the `-v` / `--verbose` flag. Users relying on `-v` should remove it from any scripts - the detailed output is now always shown by default.

## [0.1.10] - 2026-03-13

### Changed
- Optimized npm package contents to publish only runtime artifacts and documentation.
- Removed `bin/` and `src/` from the published package file list.
- Tightened `.gitignore` to ignore generated exports and packed tarballs without ignoring all CSV files.

## [0.1.9] - 2026-03-13

### Changed
- Verbose ping and trace tables now color timeout/failure cells in red for better visibility.
- Traceroute `host` now falls back to the hop IP when no hostname is available.

## [0.1.8] - 2026-03-13

### Added
- Development roadmap now explicitly includes scheduled execution and export through configuration.

## [0.1.7] - 2026-03-04

### Added
- Export functionality now writes separate operation files: `ping_YYYY-MM-DD-HH-MM-SS.csv` and `trace_YYYY-MM-DD-HH-MM-SS.csv`.

### Changed
- Probe execution now runs `ping` first and `trace` second for each target.
- Verbose output remains separated by operation for a clearer view.
- `--export` now targets an export directory while still accepting a `.csv` path and using its parent directory.

## [0.1.6] - 2026-03-03

### Added
- Verbose tables for ping packets and traceroute hops.
- DNS enrichment columns sourced from configured private and public DNS servers.
- Optional `ipinfo` enrichment in verbose mode when `providers.ipinfoToken` is configured.

### Changed
- `-v` / `--verbose` now renders structured tables instead of raw probe lines.

## [0.1.5] - 2026-03-03

### Added
- `--version` command-line option to print the current `pingtrace` version.

## [0.1.4] - 2026-02-23

### Added
- `-v` / `--verbose` option to stream raw ping and trace output line-by-line while probes run.

## [0.1.3] - 2026-02-17

### Added
- Config changes are now mirrored to `pingtrace/settings.json` in the current working directory.

### Changed
- `pingtrace config` now clears and redraws a real interactive menu in TTY sessions.
- Legacy `dns.privateServer` and `dns.publicServer` values are migrated into the new plural DNS server lists.
- Added `prepack` script to ensure `npm pack` always builds the current `dist/` output.
- Removed the accidental self-dependency on the local `pingtrace` tarball.

## [0.1.2] - 2026-02-15

### Added
- Rewritten `README.md` with clearer installation, usage, configuration, and operational guidance.

## [0.1.1] - 2026-02-12

### Added
- Interactive `pingtrace config` mode for terminal users.
- DNS config values that accept a single target, comma-separated targets, or CIDR blocks for `dns.privateServers` and `dns.publicServers`.

### Changed
- Default `pingtrace config` behavior now launches an interactive editor on TTYs and falls back to plain listing in non-interactive environments.

## [0.1.0] - 2026-02-11

### Added
- Initial npm package structure for `pingtrace` with TypeScript, build scripts, and CLI wiring.
- Root CLI with support for `help`, target input parsing, CSV loading/export, and `config` subcommands.
- Target resolution for single targets, multi-targets, CSV rows, and IPv4 CIDR expansion with deduplication.
- Persistent config schema for DNS, provider, ping, and trace settings.
- Trace configuration keys for max hops, timeout, and numeric-only lookups.
- Real first-pass probe runner that invokes system `ping` and `traceroute`/`tracert`/`tracepath`.
- Export format including target, source, operation, status, summary, notes, and duration.

### Changed
- Replaced the initial skeleton probe planner with actual command execution and terminal summaries.
- Made config-store initialization lazy so `help` works without touching the config directory.
- Resolved `--export` paths against the user's current working directory immediately for predictable output locations.