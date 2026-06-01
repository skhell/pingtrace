import { isIP } from "node:net";
import process from "node:process";
import chalk from "chalk";
import type { EnrichmentService } from "./enrichment.js";

interface ColumnDef {
  key: string;
  header?: string;
  minWidth: number;
  idealWidth: number;
  priority: number;
  truncatable: boolean;
}

interface FitOptions {
  wide?: boolean;
  columns?: string[] | undefined;
  terminalWidth?: number;
}

interface FittedColumn {
  key: string;
  header: string;
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

const TERMINAL_WIDTH_FALLBACK = 120;
const TERMINAL_WIDTH_FLOOR = 60;
const ELLIPSIS = "\u2026";

function buildPingColumns(service: EnrichmentService): ColumnDef[] {
  const cols: ColumnDef[] = [
    { key: "seq", minWidth: 3, idealWidth: 4, priority: 100, truncatable: false },
    { key: "bytes", minWidth: 4, idealWidth: 5, priority: 55, truncatable: false },
    { key: "reply", minWidth: 10, idealWidth: 25, priority: 60, truncatable: true },
    { key: "ip", minWidth: 11, idealWidth: 15, priority: 95, truncatable: false },
    { key: "ttl", minWidth: 3, idealWidth: 4, priority: 70, truncatable: false },
    { key: "time_ms", minWidth: 6, idealWidth: 7, priority: 90, truncatable: false },
  ];

  if (service.hasPrivateDns()) {
    cols.push({ key: "private_dns", minWidth: 12, idealWidth: 35, priority: 20, truncatable: true });
  }
  if (service.hasPublicDns()) {
    cols.push({ key: "public_dns", minWidth: 12, idealWidth: 35, priority: 50, truncatable: true });
  }
  if (service.hasIpinfo()) {
    cols.push({ key: "org", minWidth: 8, idealWidth: 20, priority: 40, truncatable: true });
    cols.push({ key: "asn", minWidth: 6, idealWidth: 10, priority: 30, truncatable: true });
    cols.push({ key: "location", minWidth: 10, idealWidth: 25, priority: 25, truncatable: true });
  }
  if (service.hasPeeringDb()) {
    cols.push({ key: "net_type", minWidth: 7, idealWidth: 12, priority: 15, truncatable: true });
    cols.push({ key: "policy", minWidth: 7, idealWidth: 12, priority: 10, truncatable: true });
  }

  cols.push({ key: "status", minWidth: 4, idealWidth: 7, priority: 100, truncatable: false });
  return cols;
}

function buildTraceColumns(service: EnrichmentService): ColumnDef[] {
  const cols: ColumnDef[] = [
    { key: "hop", minWidth: 2, idealWidth: 3, priority: 100, truncatable: false },
    { key: "host", minWidth: 10, idealWidth: 25, priority: 60, truncatable: true },
    { key: "ip", minWidth: 11, idealWidth: 15, priority: 95, truncatable: false },
    { key: "probe_1_ms", minWidth: 7, idealWidth: 10, priority: 90, truncatable: false },
    { key: "probe_2_ms", minWidth: 7, idealWidth: 10, priority: 80, truncatable: false },
    { key: "probe_3_ms", minWidth: 7, idealWidth: 10, priority: 75, truncatable: false },
  ];

  if (service.hasPrivateDns()) {
    cols.push({ key: "private_dns", minWidth: 12, idealWidth: 35, priority: 20, truncatable: true });
  }
  if (service.hasPublicDns()) {
    cols.push({ key: "public_dns", minWidth: 12, idealWidth: 35, priority: 50, truncatable: true });
  }
  if (service.hasIpinfo()) {
    cols.push({ key: "org", minWidth: 8, idealWidth: 20, priority: 40, truncatable: true });
    cols.push({ key: "asn", minWidth: 6, idealWidth: 10, priority: 30, truncatable: true });
    cols.push({ key: "location", minWidth: 10, idealWidth: 25, priority: 25, truncatable: true });
  }
  if (service.hasPeeringDb()) {
    cols.push({ key: "net_type", minWidth: 7, idealWidth: 12, priority: 15, truncatable: true });
    cols.push({ key: "policy", minWidth: 7, idealWidth: 12, priority: 10, truncatable: true });
  }

  cols.push({ key: "status", minWidth: 4, idealWidth: 7, priority: 100, truncatable: false });
  return cols;
}

function getTerminalWidth(): number {
  const cols = process.stdout?.columns ?? 0;
  if (!cols || cols < TERMINAL_WIDTH_FLOOR) return TERMINAL_WIDTH_FALLBACK;
  return cols;
}

// "│ a │ b │" -> 4 chars overhead per column + 1 trailing.
function frameOverhead(columnCount: number): number {
  return columnCount * 3 + 1;
}

// Filter, drop, and shrink columns to fit the terminal width.
export function fitColumns(all: ColumnDef[], opts: FitOptions = {}): FittedColumn[] {
  let pool = all;

  if (opts.columns && opts.columns.length > 0) {
    const requested = new Set(opts.columns.map((c) => c.trim().toLowerCase()));
    pool = pool.filter((c) => requested.has(c.key.toLowerCase()));
    if (pool.length === 0) pool = all;
  }

  if (opts.wide) {
    return pool.map((c) => ({ key: c.key, header: c.header ?? c.key, width: c.idealWidth }));
  }

  const termWidth = opts.terminalWidth ?? getTerminalWidth();
  const working = pool.map((c) => ({ ...c }));

  const totalIdeal = (): number =>
    working.reduce((sum, c) => sum + c.idealWidth, 0) + frameOverhead(working.length);

  // Iterate: prefer shrinking truncatable columns from lowest priority first;
  // when no slack remains, drop the lowest-priority column and continue.
  let safety = working.length * 50;
  while (totalIdeal() > termWidth && safety-- > 0) {
    const shrinkable = working
      .map((c, i) => ({ col: c, i }))
      .filter(({ col }) => col.truncatable && col.idealWidth > col.minWidth);

    if (shrinkable.length > 0) {
      shrinkable.sort((a, b) => {
        if (a.col.priority !== b.col.priority) return a.col.priority - b.col.priority;
        const slackA = a.col.idealWidth - a.col.minWidth;
        const slackB = b.col.idealWidth - b.col.minWidth;
        return slackB - slackA;
      });
      shrinkable[0]!.col.idealWidth -= 1;
      continue;
    }

    if (working.length <= 1) break;
    let dropIdx = 0;
    for (let i = 1; i < working.length; i++) {
      if (working[i]!.priority < working[dropIdx]!.priority) dropIdx = i;
    }
    working.splice(dropIdx, 1);
  }

  return working.map((c) => ({ key: c.key, header: c.header ?? c.key, width: c.idealWidth }));
}

class StreamingTableRenderer {
  private headerPrinted = false;
  private readonly fitted: FittedColumn[];

  constructor(
    columns: ColumnDef[],
    private readonly label: string,
    opts: FitOptions,
  ) {
    this.fitted = fitColumns(columns, opts);
  }

  private ensureHeader(): void {
    if (this.headerPrinted) return;
    this.headerPrinted = true;
    const widths = this.fitted.map((c) => c.width);
    console.log(chalk.cyan(`  ${this.label}`));
    console.log(drawBorder("\u250C", "\u252C", "\u2510", widths));
    console.log(drawRow(this.fitted.map((c) => c.header), widths));
    console.log(drawBorder("\u251C", "\u253C", "\u2524", widths));
  }

  row(values: TableRow): void {
    this.ensureHeader();
    const widths = this.fitted.map((c) => c.width);
    const cells = this.fitted.map((c) => values[c.key] ?? "");
    console.log(drawRow(cells, widths));
  }

  finish(): void {
    if (!this.headerPrinted) return;
    const widths = this.fitted.map((c) => c.width);
    console.log(drawBorder("\u2514", "\u2534", "\u2518", widths));
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
    opts: FitOptions = {},
  ) {
    this.table = new StreamingTableRenderer(
      buildPingColumns(enrichmentService),
      "detailed ping output",
      opts,
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
    opts: FitOptions = {},
  ) {
    this.table = new StreamingTableRenderer(
      buildTraceColumns(enrichmentService),
      "detailed trace output",
      opts,
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
  return `${left}${widths.map((w) => "\u2500".repeat(w + 2)).join(middle)}${right}`;
}

function drawRow(values: string[], widths: number[]): string {
  return `\u2502 ${values.map((v, i) => fitAnsi(v, widths[i] ?? visibleLength(v))).join(" \u2502 ")} \u2502`;
}

// Truncate to width with an ellipsis if too long, otherwise pad to width.
function fitAnsi(value: string, width: number): string {
  const visible = visibleLength(value);
  if (visible <= width) {
    return `${value}${" ".repeat(width - visible)}`;
  }
  if (width <= 1) {
    return truncatePlain(value, width);
  }
  return `${truncatePlain(value, width - 1)}${ELLIPSIS}`;
}

// Truncate an ANSI string to a visible character budget, preserving escape codes.
function truncatePlain(value: string, budget: number): string {
  if (budget <= 0) return "";
  let out = "";
  let visible = 0;
  const ansiPattern = /\u001B\[[0-9;]*m/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = ansiPattern.exec(value))) {
    const chunk = value.slice(lastIndex, match.index);
    for (const ch of chunk) {
      if (visible >= budget) return out;
      out += ch;
      visible += 1;
    }
    out += match[0];
    lastIndex = match.index + match[0].length;
  }
  for (const ch of value.slice(lastIndex)) {
    if (visible >= budget) return out;
    out += ch;
    visible += 1;
  }
  return out;
}

function visibleLength(value: string): number {
  return stripAnsi(value).length;
}

function stripAnsi(value: string): string {
  return value.replace(/\u001B\[[0-9;]*m/g, "");
}
