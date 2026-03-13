import { readFile } from "node:fs/promises";

import { parse } from "csv-parse/sync";

import type { ResolvedTarget, TargetSource } from "../domain/types.js";

const CSV_HEADER_HINTS = new Set(["target", "host", "hostname", "ip", "address"]);
const MAX_CIDR_TARGETS = 4096;

interface ResolveTargetsInput {
  input?: string;
  csvPath?: string;
}

export async function resolveTargets({
  input,
  csvPath,
}: ResolveTargetsInput): Promise<ResolvedTarget[]> {
  const resolvedTargets: ResolvedTarget[] = [];
  const seenTargets = new Set<string>();

  if (input) {
    appendInlineTargets(input, "argument", resolvedTargets, seenTargets);
  }

  if (csvPath) {
    const fileContents = await readFile(csvPath, "utf8");
    const records = parse(fileContents, {
      trim: true,
      skipEmptyLines: true,
      relaxColumnCount: true,
    }) as string[][];

    for (const [index, row] of records.entries()) {
      const firstValue = row.find((value) => value.length > 0);

      if (!firstValue) {
        continue;
      }

      if (index === 0 && CSV_HEADER_HINTS.has(firstValue.toLowerCase())) {
        continue;
      }

      appendTokenTargets(firstValue, "csv", resolvedTargets, seenTargets);
    }
  }

  return resolvedTargets;
}

export function resolveInlineTargets(input: string): string[] {
  const resolvedTargets: ResolvedTarget[] = [];
  const seenTargets = new Set<string>();

  appendInlineTargets(input, "argument", resolvedTargets, seenTargets);

  return resolvedTargets.map((target) => target.value);
}

function appendInlineTargets(
  input: string,
  source: TargetSource,
  resolvedTargets: ResolvedTarget[],
  seenTargets: Set<string>,
): void {
  for (const token of splitTargets(input)) {
    appendTokenTargets(token, source, resolvedTargets, seenTargets);
  }
}

function splitTargets(input: string): string[] {
  return input
    .split(",")
    .map((token) => token.trim())
    .filter((token) => token.length > 0);
}

function appendTokenTargets(
  token: string,
  source: TargetSource,
  resolvedTargets: ResolvedTarget[],
  seenTargets: Set<string>,
): void {
  const expandedTargets = expandToken(token, source);

  for (const target of expandedTargets) {
    if (seenTargets.has(target.value)) {
      continue;
    }

    seenTargets.add(target.value);
    resolvedTargets.push(target);
  }
}

function expandToken(token: string, source: TargetSource): ResolvedTarget[] {
  if (token.includes("/")) {
    const cidrTargets = expandIpv4Cidr(token);
    return cidrTargets.map((value) => ({
      value,
      source: "cidr",
      originalInput: token,
    }));
  }

  return [
    {
      value: token,
      source,
      originalInput: token,
    },
  ];
}

function expandIpv4Cidr(cidr: string): string[] {
  const [address, prefixText] = cidr.split("/");

  if (!address || !prefixText) {
    throw new Error(`Invalid CIDR block: ${cidr}`);
  }

  if (address.includes(":")) {
    throw new Error(`IPv6 CIDR expansion is not implemented yet: ${cidr}`);
  }

  const prefix = Number(prefixText);
  if (!Number.isInteger(prefix) || prefix < 0 || prefix > 32) {
    throw new Error(`Invalid CIDR prefix: ${cidr}`);
  }

  const octets = parseIpv4(address);
  const totalAddresses = 2 ** (32 - prefix);

  if (totalAddresses > MAX_CIDR_TARGETS) {
    throw new Error(
      `CIDR block ${cidr} expands to ${totalAddresses} addresses. Limit is ${MAX_CIDR_TARGETS} for the initial CLI skeleton.`,
    );
  }

  const mask = prefix === 0 ? 0 : ((0xffffffff << (32 - prefix)) >>> 0);
  const network = ipv4ToInt(octets) & mask;
  const start = totalAddresses <= 2 ? network : network + 1;
  const end = totalAddresses <= 2 ? network + totalAddresses - 1 : network + totalAddresses - 2;

  const targets: string[] = [];
  for (let current = start; current <= end; current += 1) {
    targets.push(intToIpv4(current >>> 0));
  }

  return targets;
}

function parseIpv4(input: string): [number, number, number, number] {
  const rawOctets = input.split(".");

  if (rawOctets.length !== 4) {
    throw new Error(`Invalid IPv4 address: ${input}`);
  }

  const octets = rawOctets.map((octet) => Number(octet));
  for (const octet of octets) {
    if (!Number.isInteger(octet) || octet < 0 || octet > 255) {
      throw new Error(`Invalid IPv4 address: ${input}`);
    }
  }

  return octets as [number, number, number, number];
}

function ipv4ToInt([a, b, c, d]: [number, number, number, number]): number {
  return ((((a << 24) >>> 0) | (b << 16) | (c << 8) | d) >>> 0);
}

function intToIpv4(value: number): string {
  return [
    (value >>> 24) & 255,
    (value >>> 16) & 255,
    (value >>> 8) & 255,
    value & 255,
  ].join(".");
}
