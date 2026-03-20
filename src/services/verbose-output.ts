import { isIP } from "node:net";

import chalk from "chalk";

import type { EnrichmentService } from "./enrichment.js";

interface ColumnDef {
  key: string;
  width: number;
}

type TableRow = Record<string, string>;

interface PingRow {
  bytes: string;
  ip: string;
  seq: string;
  status: string;
  target: string;
  timeMs: string;
  ttl: string;
}

interface TraceRow {
  hop: string;
  host: string;
  ip: string;
  probe1Ms: string;
  probe2Ms: string;
  probe3Ms: string;
  status: string;
}

function buildPingColumns(service: EnrichmentService): ColumnDef[] {
  const cols: ColumnDef[] = [
    { key: "seq", width: 4 },
    { key: "bytes", width: 5 },
    { key: "reply", width: 25 },
    { key: "ip", width: 15 },
    { key: "ttl", width: 4 },
    { key: "time_ms", width: 7 },
  ];

  if (service.hasPrivateDns()) {
    cols.push({ key: "private_dns", width: 35 });
  }

  if (service.hasPublicDns()) {
    cols.push({ key: "public_dns", width: 35 });
  }

  if (service.hasIpinfo()) {
    cols.push({ key: "org", width: 20 });
    cols.push({ key: "asn", width: 10 });
    cols.push({ key: "location", width: 25 });
  }

  if (service.hasPeeringDb()) {
    cols.push({ key: "net_type", width: 12 });
    cols.push({ key: "policy", width: 12 });
  }

  cols.push({ key: "status", width: 7 });
  return cols;
}

function buildTraceColumns(service: EnrichmentService): ColumnDef[] {
  const cols: ColumnDef[] = [
    { key: "hop", width: 3 },
    { key: "host", width: 25 },
    { key: "ip", width: 15 },
    { key: "probe_1_ms", width: 10 },
    { key: "probe_2_ms", width: 10 },
    { key: "probe_3_ms", width: 10 },
  ];

  if (service.hasPrivateDns()) {
    cols.push({ key: "private_dns", width: 35 });
  }

  if (service.hasPublicDns()) {
    cols.push({ key: "public_dns", width: 35 });
  }

  if (service.hasIpinfo()) {
    cols.push({ key: "org", width: 20 });
    cols.push({ key: "asn", width: 10 });
    cols.push({ key: "location", width: 25 });
  }

  if (service.hasPeeringDb()) {
    cols.push({ key: "net_type", width: 12 });
    cols.push({ key: "policy", width: 12 });
  }

  cols.push({ key: "status", width: 7 });
  return cols;
}

class StreamingTableRenderer {
  private headerPrinted = false;
  private readonly widths: number[];
  private readonly keys: string[];

  constructor(
    columns: ColumnDef[],
    private readonly label: string,
  ) {
    this.widths = columns.map((c) => c.width);
    this.keys = columns.map((c) => c.key);
  }

  private ensureHeader(): void {
    if (this.headerPrinted) return;
    this.headerPrinted = true;
    console.log(chalk.cyan(`  ${this.label}`));
    console.log(drawBorder("┌", "┬", "┐", this.widths));
    console.log(drawRow(this.keys, this.widths));
    console.log(drawBorder("├", "┼", "┤", this.widths));
  }

  row(values: TableRow): void {
    this.ensureHeader();
    console.log(drawRow(this.keys.map((k) => values[k] ?? ""), this.widths));
  }

  finish(): void {
    if (!this.headerPrinted) return;
    console.log(drawBorder("└", "┴", "┘", this.widths));
  }
}

export class StreamingPingRenderer {
  private readonly table: StreamingTableRenderer;
  private receivedCount = 0;
  private totalCount = 0;
  private readonly rtts: number[] = [];
  private readonly collectedRows: Record<string, string>[] = [];

  constructor(
    private readonly target: string,
    private readonly enrichmentService: EnrichmentService,
  ) {
    this.table = new StreamingTableRenderer(
      buildPingColumns(enrichmentService),
      "detailed ping output",
    );
  }

  async processLine(line: string): Promise<void> {
    const row = parsePingLine(line);
    if (!row) return;

    this.totalCount++;
    if (row.status === "ok" && row.timeMs) {
      this.receivedCount++;
      this.rtts.push(parseFloat(row.timeMs));
    }

    const enrichment = row.ip ? await this.enrichmentService.enrichIp(row.ip) : undefined;
    const isFailedRow = row.status !== "ok";

    const rawRow: Record<string, string> = {
      target: this.target,
      seq: row.seq,
      bytes: row.bytes,
      reply: row.target || this.target,
      ip: row.ip,
      ttl: row.ttl,
      time_ms: row.timeMs,
      private_dns: enrichment?.privateDns ?? "",
      public_dns: enrichment?.publicDns ?? "",
      org: enrichment?.org ?? "",
      asn: enrichment?.asn ?? "",
      location: enrichment ? this.enrichmentService.formatLocation(enrichment) : "",
      net_type: enrichment?.peeringdbType ?? "",
      policy: enrichment?.peeringdbPolicy ?? "",
      status: row.status,
    };

    this.collectedRows.push(rawRow);
    this.table.row({
      ...rawRow,
      time_ms: colorizeValue(row.timeMs, isFailedRow),
      status: colorizeStatus(row.status),
    });
  }

  finish(): void {
    this.table.finish();
  }

  getRows(): Record<string, string>[] {
    return this.collectedRows;
  }

  hasAnyReply(): boolean {
    return this.receivedCount > 0;
  }

  getSummary(durationMs: number): string {
    if (this.totalCount === 0) return `duration ${durationMs} ms`;
    const lossPercent = (
      ((this.totalCount - this.receivedCount) / this.totalCount) *
      100
    ).toFixed(1);
    if (this.rtts.length > 0) {
      const avg = (this.rtts.reduce((a, b) => a + b, 0) / this.rtts.length).toFixed(3);
      return `loss ${lossPercent}%, avg ${avg} ms`;
    }
    return `loss ${lossPercent}%, duration ${durationMs} ms`;
  }
}

export class StreamingTraceRenderer {
  private readonly table: StreamingTableRenderer;
  private readonly collectedRows: Record<string, string>[] = [];

  constructor(
    private readonly enrichmentService: EnrichmentService,
    private readonly target: string = "",
  ) {
    this.table = new StreamingTableRenderer(
      buildTraceColumns(enrichmentService),
      "detailed trace output",
    );
  }

  async processLine(line: string): Promise<void> {
    const row = parseTraceLine(line);
    if (!row) return;

    const enrichment = row.ip ? await this.enrichmentService.enrichIp(row.ip) : undefined;
    const isFailedRow = row.status !== "ok";

    const rawRow: Record<string, string> = {
      target: this.target,
      hop: row.hop,
      host: row.host || row.ip,
      ip: row.ip,
      probe_1_ms: row.probe1Ms,
      probe_2_ms: row.probe2Ms,
      probe_3_ms: row.probe3Ms,
      private_dns: enrichment?.privateDns ?? "",
      public_dns: enrichment?.publicDns ?? "",
      org: enrichment?.org ?? "",
      asn: enrichment?.asn ?? "",
      location: enrichment ? this.enrichmentService.formatLocation(enrichment) : "",
      net_type: enrichment?.peeringdbType ?? "",
      policy: enrichment?.peeringdbPolicy ?? "",
      status: row.status,
    };

    this.collectedRows.push(rawRow);
    this.table.row({
      ...rawRow,
      probe_1_ms: colorizeProbeValue(row.probe1Ms, isFailedRow),
      probe_2_ms: colorizeProbeValue(row.probe2Ms, isFailedRow),
      probe_3_ms: colorizeProbeValue(row.probe3Ms, isFailedRow),
      status: colorizeStatus(row.status),
    });
  }

  finish(): void {
    this.table.finish();
  }

  getRows(): Record<string, string>[] {
    return this.collectedRows;
  }
}

function parsePingLine(line: string): PingRow | null {
  const trimmed = line.trim();
  if (trimmed.length === 0 || trimmed.startsWith("PING ") || trimmed.startsWith("---")) {
    return null;
  }

  const unixReply = trimmed.match(
    /^(?<bytes>\d+)\s+bytes\s+from\s+(?<endpoint>.+?):\s+(?:icmp_seq=(?<seq>\d+)\s+)?ttl=(?<ttl>\d+)\s+time[=<]?(?<time>[\d.]+)\s*ms/i,
  );
  if (unixReply?.groups) {
    const endpoint = splitEndpoint(unixReply.groups.endpoint ?? "");
    return {
      bytes: unixReply.groups.bytes ?? "",
      ip: endpoint.ip,
      seq: unixReply.groups.seq ?? "",
      status: "ok",
      target: endpoint.label,
      timeMs: unixReply.groups.time ?? "",
      ttl: unixReply.groups.ttl ?? "",
    };
  }

  const windowsReply = trimmed.match(
    /^Reply from (?<ip>[^\s:]+): bytes=(?<bytes>\d+) time[=<]?(?<time>[\d<]+)ms TTL=(?<ttl>\d+)/i,
  );
  if (windowsReply?.groups) {
    return {
      bytes: windowsReply.groups.bytes ?? "",
      ip: windowsReply.groups.ip ?? "",
      seq: "",
      status: "ok",
      target: windowsReply.groups.ip ?? "",
      timeMs: windowsReply.groups.time ?? "",
      ttl: windowsReply.groups.ttl ?? "",
    };
  }

  const unixTimeout = trimmed.match(/^Request timeout for icmp_seq\s*(?<seq>\d+)/i);
  if (unixTimeout?.groups) {
    return {
      bytes: "",
      ip: "",
      seq: unixTimeout.groups.seq ?? "",
      status: "timeout",
      target: "",
      timeMs: "",
      ttl: "",
    };
  }

  if (/^Request timed out\./i.test(trimmed)) {
    return {
      bytes: "",
      ip: "",
      seq: "",
      status: "timeout",
      target: "",
      timeMs: "",
      ttl: "",
    };
  }

  return null;
}

function parseTraceLine(line: string): TraceRow | null {
  const trimmed = line.trim();
  if (
    trimmed.length === 0 ||
    /^traceroute to /i.test(trimmed) ||
    /^Tracing route to /i.test(trimmed) ||
    /^over a maximum of /i.test(trimmed)
  ) {
    return null;
  }

  const hopMatch = trimmed.match(/^(?<hop>\d+)\s+(?<rest>.+)$/);
  if (!hopMatch?.groups) {
    return null;
  }

  const hop = hopMatch.groups.hop ?? "";
  const rest = hopMatch.groups.rest ?? "";

  if (/^Request timed out\./i.test(rest) || /^(?:\*\s*)+$/.test(rest)) {
    return {
      hop,
      host: "",
      ip: "",
      probe1Ms: "*",
      probe2Ms: "*",
      probe3Ms: "*",
      status: "timeout",
    };
  }

  const probeValues = [...rest.matchAll(/(<?\d+(?:\.\d+)?)\s*ms|\*/gi)].map(
    (match) => match[1] ?? "*",
  );
  if (probeValues.length === 0) {
    return null;
  }

  const descriptor = rest
    .replace(/(<?\d+(?:\.\d+)?)\s*ms|\*/gi, "")
    .replace(/\s+/g, " ")
    .trim();

  const endpoint = splitEndpoint(descriptor);

  return {
    hop,
    host: endpoint.host,
    ip: endpoint.ip,
    probe1Ms: probeValues[0] ?? "",
    probe2Ms: probeValues[1] ?? "",
    probe3Ms: probeValues[2] ?? "",
    status: probeValues.every((v) => v === "*") ? "timeout" : "ok",
  };
}

function splitEndpoint(input: string): { host: string; ip: string; label: string } {
  const trimmed = input.trim();

  const roundBrackets = trimmed.match(/^(?<host>.+?)\s+\((?<ip>[\da-fA-F:.]+)\)$/);
  if (roundBrackets?.groups) {
    const host = roundBrackets.groups.host ?? "";
    const ip = roundBrackets.groups.ip ?? "";
    return { host, ip, label: host };
  }

  const squareBrackets = trimmed.match(/^(?<host>.+?)\s+\[(?<ip>[\da-fA-F:.]+)\]$/);
  if (squareBrackets?.groups) {
    const host = squareBrackets.groups.host ?? "";
    const ip = squareBrackets.groups.ip ?? "";
    return { host, ip, label: host };
  }

  if (isIP(trimmed) !== 0) {
    return { host: "", ip: trimmed, label: trimmed };
  }

  return { host: trimmed, ip: "", label: trimmed };
}

function colorizeStatus(status: string): string {
  if (status === "ok") return chalk.green(status);
  return chalk.red(status);
}

function colorizeValue(value: string, highlight: boolean): string {
  if (!highlight || value.length === 0) return value;
  return chalk.red(value);
}

function colorizeProbeValue(value: string, highlight: boolean): string {
  if (value === "*") return chalk.red(value);
  return colorizeValue(value, highlight);
}

function drawBorder(left: string, middle: string, right: string, widths: number[]): string {
  return `${left}${widths.map((w) => "─".repeat(w + 2)).join(middle)}${right}`;
}

function drawRow(values: string[], widths: number[]): string {
  return `│ ${values.map((v, i) => padAnsi(v, widths[i] ?? visibleLength(v))).join(" │ ")} │`;
}

function padAnsi(value: string, width: number): string {
  return `${value}${" ".repeat(Math.max(0, width - visibleLength(value)))}`;
}

function visibleLength(value: string): number {
  return stripAnsi(value).length;
}

function stripAnsi(value: string): string {
  return value.replace(/\u001B\[[0-9;]*m/g, "");
}
