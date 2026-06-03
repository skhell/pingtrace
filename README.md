# pingtrace
[![GitHub license](https://img.shields.io/badge/license-MIT-blue.svg)](https://raw.githubusercontent.com/skhell/pingtrace/main/LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/skhell/pingtrace.svg)](https://pkg.go.dev/github.com/skhell/pingtrace)
[![Go Report Card](https://goreportcard.com/badge/github.com/skhell/pingtrace)](https://goreportcard.com/report/github.com/skhell/pingtrace)

![Pingtrace demo](.github/media/pingtrace.gif "pingtrace demo")

Get a clean, color-coded view of every reply and every hop in a single run enriched with private and public DNS, [ipinfo.io](ipinfo.io) and PeeringDB policies. Easily export the results in CSV and JSON format.

---

## Why pingtrace over the standard tools?

| You usually run                        | pingtrace gives you in one command                                     |
| -------------------------------------- | ---------------------------------------------------------------------- |
| `ping` then `traceroute` / `tracert` depending on OS then `mtr` | you run universally `pingtrace` that style the output and offer additional flags like MTR |
| `dig +short` for every hop | reverse DNS resolved automatically, with optional **private DNS** for internal hops |
| `for ip in $(seq …)` loops | simply `pingtrace 10.0.0.0/24` or `--file targets.csv` |
| copy/paste into a spreadsheet or note | `--export ./reports` writes timestamped UTC CSVs and `pingtrace 10.0.0.0/21` do it by default; add `--json` for a schema-validated JSON report alongside |

It's the same probes you already trust (the OS `ping` / `traceroute`) pingtrace just runs them with no additional latency overhead, parallelize requests for faster CIDR resolution, decorates outputs for clear cross-platform readability including export.

---

## Try it in 30 seconds

### Install from source

```sh
go install github.com/skhell/pingtrace/cmd/pingtrace@latest
pingtrace help
```

```sh
# the headline command ping + trace + enrichment
pingtrace 8.8.8.8

# live MTR (Ctrl+C / q to stop)
pingtrace 1.1.1.1 --mtr

# bounded MTR, 10 cycles, 2s apart, exported to CSV
pingtrace 1.1.1.1 -m --cycles 10 --interval 2 --export ./reports

# CSV + JSON report (JSON validates against schema/pingtrace.schema.json)
pingtrace 8.8.8.8 --export ./reports --json

# multiple targets, a CIDR, or a file
pingtrace 8.8.8.8,1.1.1.1,example.com
pingtrace 10.0.0.0/30
pingtrace --file ./targets.csv

# script-friendly: pick your columns
pingtrace 8.8.8.8 --no-trace --columns seq,ip,time_ms,status
```

Run `pingtrace --help` for the grouped, color-coded flag reference, and `pingtrace config` to open an interactive TUI for tokens, DNS, and thresholds.

---

## Operational notes

- `pingtrace` depends on system `ping` and `traceroute` tooling being available on `PATH`.
- On Windows, `tracert` is used.
- On Unix-like systems, `traceroute` is used, with `tracepath` as a fallback where available.
- `--export` without a path writes operation-specific CSV files in the current working directory.
- If `--export` points to a `.csv` file path, `pingtrace` uses that file's directory and still writes separate `ping_...csv` and `trace_...csv` files.
- Private DNS enrichment is automatically skipped if the configured server does not respond within 5 seconds.
- `--json` writes a sibling JSON report (`probe_...json` for ping/trace runs, `mtr_<target>_...json` per MTR target) into the same directory as `--export`, or the current working directory when `--export` is omitted. The document validates against [`schema/pingtrace.schema.json`](schema/pingtrace.schema.json) and lists any CSVs written in its `exportedFiles` section.
- PeeringDB and ipinfo.io enrichment is skipped for private/RFC-1918 IP addresses.

## Feedback

If `pingtrace` saved you time in a troubleshooting session, it was worth building.

- Star the project on [GitHub](https://github.com/skhell/pingtrace)
- Report bugs or request features in [Issues](https://github.com/skhell/pingtrace/issues)
- Buy a coffee (or a snack for my buddy Schnauzer Tyson) if you feel like it.
