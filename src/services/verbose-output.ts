import { isIP } from "node:net";

import chalk from "chalk";

import type { OperationKind } from "../domain/types.js";
import { EnrichmentService } from "./enrichment.js";

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

export async function renderVerboseOperation(
  operation: OperationKind,
  target: string,
  output: string,
  enrichmentService: EnrichmentService,
): Promise<void> {
  console.log(chalk.cyan(`  detailed ${operation} output`));

  if (operation === "ping") {
    await renderPingTable(target, output, enrichmentService);
    return;
  }

  await renderTraceTable(output, enrichmentService);
}

async function renderPingTable(
  target: string,
  output: string,
  enrichmentService: EnrichmentService,
): Promise<void> {
  const parsedRows = parsePingRows(output);
  if (parsedRows.length === 0) {
    renderRawFallback(output);
    return;
  }

  const detailedRows = await Promise.all(
    parsedRows.map(async (row) => {
      const enrichment = row.ip ? await enrichmentService.enrichIp(row.ip) : undefined;
      const isFailedRow = row.status !== "ok";

      return {
        seq: row.seq,
        bytes: row.bytes,
        reply: row.target || target,
        ip: row.ip,
        ttl: row.ttl,
        time_ms: colorizeValue(row.timeMs, isFailedRow),
        private_dns: enrichment?.privateDns ?? "",
        public_dns: enrichment?.publicDns ?? "",
        org: enrichment?.org ?? "",
        asn: enrichment?.asn ?? "",
        location: enrichment ? enrichmentService.formatLocation(enrichment) : "",
        status: colorizeStatus(row.status),
      };
    }),
  );

  renderTable(detailedRows);
}

async function renderTraceTable(
  output: string,
  enrichmentService: EnrichmentService,
): Promise<void> {
  const parsedRows = parseTraceRows(output);
  if (parsedRows.length === 0) {
    renderRawFallback(output);
    return;
  }

  const detailedRows = await Promise.all(
    parsedRows.map(async (row) => {
      const enrichment = row.ip ? await enrichmentService.enrichIp(row.ip) : undefined;
      const isFailedRow = row.status !== "ok";

      return {
        hop: row.hop,
        host: row.host || row.ip,
        ip: row.ip,
        probe_1_ms: colorizeProbeValue(row.probe1Ms, isFailedRow),
        probe_2_ms: colorizeProbeValue(row.probe2Ms, isFailedRow),
        probe_3_ms: colorizeProbeValue(row.probe3Ms, isFailedRow),
        private_dns: enrichment?.privateDns ?? "",
        public_dns: enrichment?.publicDns ?? "",
        org: enrichment?.org ?? "",
        asn: enrichment?.asn ?? "",
        location: enrichment ? enrichmentService.formatLocation(enrichment) : "",
        status: colorizeStatus(row.status),
      };
    }),
  );

  renderTable(detailedRows);
}

function parsePingRows(output: string): PingRow[] {
  return output
    .split(/\r?\n/)
    .map((line) => parsePingLine(line))
    .filter((row): row is PingRow => row !== null);
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

function parseTraceRows(output: string): TraceRow[] {
  return output
    .split(/\r?\n/)
    .map((line) => parseTraceLine(line))
    .filter((row): row is TraceRow => row !== null);
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
    status: probeValues.every((value) => value === "*") ? "timeout" : "ok",
  };
}

function splitEndpoint(input: string): { host: string; ip: string; label: string } {
  const trimmed = input.trim();

  const roundBrackets = trimmed.match(/^(?<host>.+?)\s+\((?<ip>[\da-fA-F:.]+)\)$/);
  if (roundBrackets?.groups) {
    const host = roundBrackets.groups.host ?? "";
    const ip = roundBrackets.groups.ip ?? "";

    return {
      host,
      ip,
      label: host,
    };
  }

  const squareBrackets = trimmed.match(/^(?<host>.+?)\s+\[(?<ip>[\da-fA-F:.]+)\]$/);
  if (squareBrackets?.groups) {
    const host = squareBrackets.groups.host ?? "";
    const ip = squareBrackets.groups.ip ?? "";

    return {
      host,
      ip,
      label: host,
    };
  }

  if (isIP(trimmed) !== 0) {
    return {
      host: "",
      ip: trimmed,
      label: trimmed,
    };
  }

  return {
    host: trimmed,
    ip: "",
    label: trimmed,
  };
}

function renderRawFallback(output: string): void {
  const lines = output
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
    .map((line) => ({ raw: line }));

  if (lines.length === 0) {
    renderTable([{ raw: "No detailed output captured." }]);
    return;
  }

  renderTable(lines);
}

function colorizeStatus(status: string): string {
  if (status === "ok") {
    return chalk.green(status);
  }

  return chalk.red(status);
}

function colorizeValue(value: string, highlight: boolean): string {
  if (!highlight || value.length === 0) {
    return value;
  }

  return chalk.red(value);
}

function colorizeProbeValue(value: string, highlight: boolean): string {
  if (value === "*") {
    return chalk.red(value);
  }

  return colorizeValue(value, highlight);
}

function renderTable(rows: TableRow[]): void {
  if (rows.length === 0) {
    return;
  }

  const firstRow = rows[0];
  if (!firstRow) {
    return;
  }

  const columns = Object.keys(firstRow);
  const widths = columns.map((column) =>
    Math.max(column.length, ...rows.map((row) => visibleLength(row[column] ?? ""))),
  );

  console.log(drawBorder("┌", "┬", "┐", widths));
  console.log(drawRow(columns, widths));
  console.log(drawBorder("├", "┼", "┤", widths));

  for (const row of rows) {
    console.log(drawRow(columns.map((column) => row[column] ?? ""), widths));
  }

  console.log(drawBorder("└", "┴", "┘", widths));
}

function drawBorder(left: string, middle: string, right: string, widths: number[]): string {
  return `${left}${widths.map((width) => "─".repeat(width + 2)).join(middle)}${right}`;
}

function drawRow(values: string[], widths: number[]): string {
  return `│ ${values
    .map((value, index) => padAnsi(value, widths[index] ?? value.length))
    .join(" │ ")} │`;
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
