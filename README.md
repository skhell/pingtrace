# pingtrace
[![GitHub license](https://img.shields.io/badge/license-MIT-blue.svg)](https://raw.githubusercontent.com/skhell/pingtrace/main/LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/skhell/pingtrace.svg)](https://pkg.go.dev/github.com/skhell/pingtrace)
[![Go Report Card](https://goreportcard.com/badge/github.com/skhell/pingtrace)](https://goreportcard.com/report/github.com/skhell/pingtrace)

![Pingtrace demo](.github/media/pingtrace.gif "pingtrace demo")

`pingtrace` combines ping, traceroute, MTR, TCP service reachability checks, private and public DNS enrichment, [PeeringDB](https://www.peeringdb.com/), [ipinfo](https://ipinfo.io/) context and CSV/JSON export in a single cross-platform CLI.
It is built for who need fast and exportable answers during incidents without jumping between multiple windows with ping, traceroute, mtr, dig, nmap, spreadsheets and notes.

## Why pingtrace over the standard tools?

| You usually run                        | pingtrace gives you in one command                                     |
| -------------------------------------- | ---------------------------------------------------------------------- |
| `ping` then `traceroute` / `tracert` depending on OS then `mtr` | you run universally `pingtrace` that style the output and offer additional flags like MTR |
| `dig +short` for every hop | reverse DNS resolved automatically, with optional **private DNS** for internal hops |
| `for ip in $(seq …)` loops | simply `pingtrace 10.0.0.0/24` or `--file targets.csv` |
| copy/paste into a spreadsheet or note | `--export ./reports` writes timestamped UTC CSVs and `pingtrace 10.0.0.0/21` do it by default; add `--json` for a schema-validated JSON report alongside |
| `nmap` / `masscan` for port reachability | `--ports` TCP connect scan, no root required, IANA service names auto-resolved |

It's the same probes you already trust (the OS `ping` / `traceroute`) pingtrace just runs them with no additional latency overhead, parallelize requests for faster CIDR resolution, decorates outputs for clear cross-platform readability including export.

---

## Install

### macOS

```sh
brew tap skhell/pingtrace
brew install pingtrace
```

### Linux

Download the `.deb` or `.rpm` from the [latest GitHub Release](https://github.com/skhell/pingtrace/releases/latest):

```sh
# Debian / Ubuntu
sudo dpkg -i pingtrace_linux_amd64.deb

# Fedora / RHEL
sudo rpm -i pingtrace_linux_amd64.rpm
```

### Any platform (Go toolchain required)

```sh
go install github.com/skhell/pingtrace/cmd/pingtrace@latest
```

---

## Quick start

```sh
# the headline command ping + trace + enrichment
pingtrace 1.1.1.1

# live MTR (Ctrl+C / q to stop)
pingtrace 1.1.1.1 --mtr

# bounded MTR, 10 cycles, 2s apart, exported to CSV
pingtrace 1.1.1.1 -m --cycles 10 --interval 2 --export ./reports

# CSV + JSON report (JSON validates against schema/pingtrace.schema.json)
pingtrace 1.1.1.1 --export ./reports --json

# CSV with no empty columns (e.g. skips PeeringDB columns when not configured or get empty results)
pingtrace 1.1.1.1 --export ./reports --compact-export
pingtrace 1.1.1.1 --export --json ./reports --compact-export

# multiple targets, a CIDR, or a file
pingtrace 1.1.1.1,1.1.1.1,example.com
pingtrace 10.0.0.0/30
pingtrace --file ./targets.csv

# script-friendly: pick your columns
pingtrace 1.1.1.1 --no-trace --columns seq,ip,time_ms,status

# TCP port scan (all 65535 ports) with IANA service name enrichment
pingtrace 10.0.0.1 --ports
pingtrace 10.0.0.0/28 --ports
pingtrace domain.com --ports

# scan specific ports or ranges with IANA service name enrichment
pingtrace 10.0.0.1 --ports 22,80,443,8000-9000

# CIDR port scan + export to CSV with IANA service name enrichment
pingtrace 10.0.0.0/28 --ports --export ./reports
```

Run `pingtrace --help` for the grouped, color-coded flag reference, and `pingtrace config` to open an interactive TUI for tokens, DNS, and thresholds.

---

## Operational notes

- `pingtrace` depends on system `ping` and `traceroute` tooling being available on `PATH`.
- On Windows, `tracert` is used.
- On Unix-like systems, `traceroute` is used, with `tracepath` as a fallback where available.
- `--export` without a path writes operation-specific CSV files in the current working directory.
- If `--export` points to a `.csv` file path, `pingtrace` uses that file's directory and still writes separate `ping_...csv` and `trace_...csv` files.
- CSV rows include a `source` column (local outbound IP of the machine running pingtrace) and a `target` column (the hostname or IP passed on the command line). Both appear on every row so exports from multiple machines can be merged and filtered without ambiguity.
- `--compact-export` omits columns that are entirely empty across all rows (e.g. PeeringDB columns when PeeringDB is not configured, ipinfo columns when no token is set). The column set is locked on the first write and stays consistent for the whole file.
- Private DNS enrichment is automatically skipped if the configured server does not respond within 5 seconds.
- `--json` writes a sibling JSON report (`probe_...json` for ping/trace runs, `mtr_<target>_...json` per MTR target) into the same directory as `--export`, or the current working directory when `--export` is omitted. The document validates against [`schema/pingtrace.schema.json`](schema/pingtrace.schema.json) and lists any CSVs written in its `exportedFiles` section.
- PeeringDB and ipinfo.io enrichment is skipped for private/RFC-1918 IP addresses.
- `--ports` runs a TCP connect scan (no raw sockets, no root/admin required). On first use, pingtrace auto-downloads the IANA service-names registry and caches it in the user config directory. Subsequent runs use the cache. The scan still works without the registry - ports will display without service names if the download fails. Use `pingtrace config` to manually sync the IANA database or adjust the download URL in case it change.

## Feedback

If `pingtrace` saved you time in a troubleshooting session, it was worth building.

- Star the project on [GitHub](https://github.com/skhell/pingtrace)
- Report bugs or request features in [Issues](https://github.com/skhell/pingtrace/issues)
- [Buy me a coffee](https://buymeacoffee.com/skhell), or a snack for my buddy Schnauzer Tyson [GitHub](https://github.com/sponsors/skhell)  if you feel this project was useful to your or your team.
