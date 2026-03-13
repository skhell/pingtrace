# pingtrace

![pingtrace](https://raw.githubusercontent.com/skhell/pingtrace/main/.github/media/pingtrace.png)

`pingtrace` is a terminal-first CLI designed for rapid network troubleshooting, combining ping and traceroute with a clear, intuitive output enriched by DNS and ipinfo.io data..

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

Run with detailed verbose tables:

```bash
pingtrace 8.8.8.8 -v
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

Export to CSV in the current working directory:

```bash
pingtrace 8.8.8.8 --export
```

Export to a specific directory:

```bash
pingtrace 8.8.8.8 --export ./reports
```

When export is enabled, `pingtrace` writes separate files per operation:

- `ping_UTCdate(YYYY-MM-DD-HH-MM-SS).csv`
- `trace_UTCdate(YYYY-MM-DD-HH-MM-SS).csv`

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

If `providers.ipinfoToken` is configured, verbose mode also enriches public IP rows with `ipinfo` metadata.
Verbose output renders `ping` first and `trace` second, with separate tables for a clearer view.

Supported config keys:

- `dns.privateServers`
- `dns.publicServers`
- `providers.ipinfoToken`
- `providers.peeringdbEnabled`
- `ping.packetSize`
- `ping.packetCount`
- `ping.timeoutSeconds`
- `trace.maxHops`
- `trace.timeoutSeconds`
- `trace.numericOnly`

## Command cheatsheet

```bash
pingtrace help
pingtrace --version
pingtrace <target-or-targets-or-cidr>
pingtrace --file <path>
pingtrace <target> -v
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


## Feedback
If it saves you time from a troubleshooting session, it was worth building.
Star the project and if you want invite me for a coffe or a snack for my buddy Schnauzer Tyson.
