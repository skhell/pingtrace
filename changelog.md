# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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