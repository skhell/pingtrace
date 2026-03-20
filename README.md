# pingtrace

![pingtrace](https://raw.githubusercontent.com/skhell/pingtrace/main/.github/media/pingtrace.png)

`pingtrace` is a terminal-first CLI designed for rapid network troubleshooting, combining ping and traceroute with a clear, intuitive output enriched by DNS, ipinfo.io, and PeeringDB data.

It runs `ping` and `traceroute` in a single command against:

1. **Single target**: Quick check for a single host.
2. **Multi-target**: Separate multiple targets with commas for simultaneous checks.
3. **Bulk loading**: Import target lists directly from CSV files.
4. **CIDR blocks**: Automatically check against IPv4 CIDR blocks.

The focus is simple: fast input, clear output, and optional CSV export.


## Install

```bash
npm install -g pingtrace
pingtrace --help
```

### Quick start

Run both ping and trace against one host:

```bash
pingtrace 8.8.8.8
```

Run against multiple targets:

```bash
pingtrace 8.8.8.8,1.1.1.1,example.com
```

Run against a CIDR:

```bash
pingtrace 10.0.0.0/30
```

Run from CSV:

```bash
pingtrace --file ./targets.csv
```

Show compact summary output instead of full tables:

```bash
pingtrace 8.8.8.8 --summary
```

Export to CSV in the current working directory:

```bash
pingtrace 8.8.8.8 --export
```

Export to a specific directory:

```bash
pingtrace 8.8.8.8 --export ./reports
```

When export is enabled, `pingtrace` writes separate files per operation:

- `ping_UTCdate(YYYY-MM-DD-HH-MM-SS).csv` - one row per packet, with all enrichment columns
- `trace_UTCdate(YYYY-MM-DD-HH-MM-SS).csv` - one row per hop, with all enrichment columns

When running with `--summary`, the CSV falls back to one summary row per target instead of per-packet/per-hop detail.

Run only ping:

```bash
pingtrace 8.8.8.8 --no-trace
```

Run only trace:

```bash
pingtrace 8.8.8.8 --no-ping
```

## Config

Open the interactive editor:

```bash
pingtrace config
```

List current values:

```bash
pingtrace config list
```

Set a single value directly:

```bash
pingtrace config set ping.packetCount 3
```

Examples for DNS server target lists:

```bash
pingtrace config set dns.publicServers 8.8.8.8
pingtrace config set dns.publicServers 8.8.8.8,1.1.1.1
pingtrace config set dns.privateServers 10.0.0.0/30
```

Supported config keys:

| Key | Default | Description |
|---|---|---|
| `dns.privateServers` | _(empty)_ | Private/internal DNS servers for reverse lookups. Accepts IPs, comma-separated IPs, or CIDR. |
| `dns.publicServers` | `8.8.8.8` | Public DNS servers for reverse lookups. |
| `providers.ipinfoToken` | _(empty)_ | ipinfo.io API token. Enables `org`, `asn`, and `location` columns for public IPs. |
| `providers.peeringdbEnabled` | `false` | Enables PeeringDB enrichment. Adds `net_type` and `policy` columns for public IPs. Requires `providers.ipinfoToken`. |
| `ping.packetSize` | `56` | Ping packet size in bytes. |
| `ping.packetCount` | `4` | Number of ping packets per target. |
| `ping.timeoutSeconds` | `5` | Ping timeout in seconds. |
| `trace.maxHops` | `16` | Maximum traceroute hops. |
| `trace.timeoutSeconds` | `2` | Per-hop traceroute timeout in seconds. |
| `trace.numericOnly` | `true` | Skip hostname resolution in traceroute. |

## Enrichment

Output columns are shown conditionally - only when the corresponding provider is configured.

### DNS

Reverse DNS lookups are performed for every hop and ping reply. Configure up to two resolvers:

```bash
pingtrace config set dns.privateServers 10.0.0.1   # internal resolver
pingtrace config set dns.publicServers 8.8.8.8      # public resolver
```

If a private DNS server is unreachable, `pingtrace` will warn and skip it after a 5-second timeout so probes continue without interruption.

### ipinfo.io

Enriches public IPs with organisation, ASN, and geolocation. Get a free token at [ipinfo.io](https://ipinfo.io).

```bash
pingtrace config set providers.ipinfoToken <your-token>
```

Adds columns: `org`, `asn`, `location`.

### PeeringDB

Enriches public IPs with network type and peering policy sourced from the [PeeringDB](https://www.peeringdb.com) public API. Requires `providers.ipinfoToken` to resolve the ASN first. No additional credentials are needed for PeeringDB.

```bash
pingtrace config set providers.peeringdbEnabled true
```

Adds columns: `net_type`, `policy`.

| `net_type` value | Meaning |
|---|---|
| `NSP` | Network Service Provider - transit/backbone carrier |
| `Content` | Content delivery network (CDN) or hyperscaler |
| `IXP` | Internet Exchange Point |
| `Enterprise` | Enterprise or corporate network |
| `Educational` | University or research network |
| `Non-Profit` | Non-profit organisation |
| `Route Server` | Route server operator |

| `policy` value | Meaning |
|---|---|
| `Open` | Will peer with anyone |
| `Selective` | Peers on a case-by-case basis |
| `Restrictive` | Very limited peering |
| `No` | Does not peer |

## Bulk mode

When a target set exceeds 254 hosts (i.e., any CIDR larger than `/24` such as `/23`, `/22`, `/18`), pingtrace automatically enables bulk mode:

- Streaming tables are disabled - output is one compact summary line per target
- Up to 10 probes run concurrently to reduce total execution time
- A CSV is auto-exported to the current directory without requiring `--export`

```text
pingtrace 10.0.0.0/22

pingtrace
Targets: 1022
Operations: ping, trace
Bulk mode: 1022 targets exceed /24 - running concurrently, streaming disabled.
CSV export: /current/dir
Running probes...

  10.0.0.1            ok ping: loss 0.0%, avg 4ms  |  ok trace: 7 hop(s)  [1/1022]
  10.0.0.2            fail ping: loss 100%          |  fail trace: no route  [2/1022]
  ...

Completed 980 probe(s).
Failed 42 probe(s).
Wrote ping CSV (4088 row(s)) to /current/dir/ping_2026-03-20-...csv
Wrote trace CSV (7154 row(s)) to /current/dir/trace_2026-03-20-...csv
```

To speed up large runs further, combine with `--no-trace` (ping only) or `--no-ping` (trace only).

## Command cheatsheet

```bash
pingtrace help
pingtrace --version
pingtrace <target-or-targets-or-cidr>
pingtrace --file <path>
pingtrace --summary
pingtrace config
pingtrace config set <key> <value>
pingtrace config get <key>
pingtrace config list
pingtrace config reset
```

## Operational notes

- `pingtrace` depends on system `ping` and `traceroute` tooling being available on `PATH`.
- On Windows, `tracert` is used.
- On Unix-like systems, `traceroute` is used, with `tracepath` as a fallback where available.
- `--export` without a path writes operation-specific CSV files in the current working directory.
- If `--export` points to a `.csv` file path, `pingtrace` uses that file's directory and still writes separate `ping_...csv` and `trace_...csv` files.
- Private DNS enrichment is automatically skipped if the configured server does not respond within 5 seconds.
- PeeringDB and ipinfo.io enrichment is skipped for private/RFC-1918 IP addresses.


## Feedback
If it saves you time from a troubleshooting session, it was worth building.
Star the project and if you want invite me for a coffe or a snack for my buddy Schnauzer Tyson.
