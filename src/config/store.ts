import Conf from "conf";
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";

import type { PingtraceConfig } from "../domain/types.js";
import { resolveInlineTargets } from "../services/target-resolver.js";

export const DEFAULT_CONFIG: PingtraceConfig = {
  dns: {
    privateServers: [],
    publicServers: ["8.8.8.8"],
  },
  providers: {
    ipinfoToken: "",
    peeringdbEnabled: false,
  },
  ping: {
    packetSize: 56,
    packetCount: 4,
    timeoutSeconds: 5,
  },
  trace: {
    maxHops: 16,
    timeoutSeconds: 2,
    numericOnly: true,
  },
};

const CONFIG_KEYS = [
  "dns.privateServers",
  "dns.publicServers",
  "providers.ipinfoToken",
  "providers.peeringdbEnabled",
  "ping.packetSize",
  "ping.packetCount",
  "ping.timeoutSeconds",
  "trace.maxHops",
  "trace.timeoutSeconds",
  "trace.numericOnly",
] as const;

type ConfigKey = (typeof CONFIG_KEYS)[number];

const BOOLEAN_KEYS = new Set<ConfigKey>(["providers.peeringdbEnabled", "trace.numericOnly"]);
const NUMBER_KEYS = new Set<ConfigKey>([
  "ping.packetCount",
  "ping.packetSize",
  "ping.timeoutSeconds",
  "trace.maxHops",
  "trace.timeoutSeconds",
]);
const TARGET_LIST_KEYS = new Set<ConfigKey>(["dns.privateServers", "dns.publicServers"]);
const MIRROR_CONFIG_PATH = path.resolve(process.cwd(), "pingtrace", "settings.json");

let store: Conf<PingtraceConfig> | undefined;

export function getConfig(): PingtraceConfig {
  const configStore = getStore();
  const normalizedConfig = normalizeConfig(configStore.store);

  if (!areConfigsEqual(configStore.store, normalizedConfig)) {
    configStore.store = normalizedConfig;
  }

  return normalizedConfig;
}

export function getRuntimeConfig(): PingtraceConfig {
  try {
    return getConfig();
  } catch {
    return structuredClone(DEFAULT_CONFIG);
  }
}

export function getConfigStorePath(): string {
  return getStore().path;
}

export function getMirrorConfigPath(): string {
  return MIRROR_CONFIG_PATH;
}

export function getConfigValue(key: string): unknown {
  const validatedKey = asConfigKey(key);
  return readPath(getConfig(), validatedKey);
}

export function setConfigValue(key: string, rawValue: string): unknown {
  const validatedKey = asConfigKey(key);
  const parsedValue = parseConfigValue(validatedKey, rawValue);
  const configStore = getStore();
  configStore.set(validatedKey, parsedValue);
  const normalizedConfig = getConfig();
  syncMirrorConfig(normalizedConfig);
  return readPath(normalizedConfig, validatedKey);
}

export function listConfigEntries(config: PingtraceConfig): Array<[ConfigKey, unknown]> {
  return CONFIG_KEYS.map((key) => [key, readPath(config, key)]);
}

export function resetConfig(): void {
  const configStore = getStore();
  configStore.clear();
  configStore.store = structuredClone(DEFAULT_CONFIG);
  syncMirrorConfig(configStore.store);
}

function getStore(): Conf<PingtraceConfig> {
  if (!store) {
    store = new Conf<PingtraceConfig>({
      projectName: "pingtrace",
      configName: "settings",
      defaults: DEFAULT_CONFIG,
    });
  }

  return store;
}

function asConfigKey(key: string): ConfigKey {
  if ((CONFIG_KEYS as readonly string[]).includes(key)) {
    return key as ConfigKey;
  }

  throw new Error(`Unknown config key: ${key}`);
}

function parseConfigValue(key: ConfigKey, rawValue: string): string | number | boolean | string[] {
  if (TARGET_LIST_KEYS.has(key)) {
    return parseTargetList(rawValue);
  }

  if (BOOLEAN_KEYS.has(key)) {
    if (rawValue === "true") {
      return true;
    }

    if (rawValue === "false") {
      return false;
    }

    throw new Error(`Expected a boolean for ${key}. Use true or false.`);
  }

  if (NUMBER_KEYS.has(key)) {
    const parsedValue = Number(rawValue);

    if (!Number.isFinite(parsedValue)) {
      throw new Error(`Expected a number for ${key}.`);
    }

    return parsedValue;
  }

  return rawValue;
}

function parseTargetList(rawValue: string): string[] {
  const trimmedValue = rawValue.trim();

  if (trimmedValue.length === 0) {
    return [];
  }

  return resolveInlineTargets(trimmedValue);
}

function readPath(config: PingtraceConfig, key: ConfigKey): unknown {
  const [section, field] = key.split(".") as [keyof PingtraceConfig, string];
  return config[section][field as keyof PingtraceConfig[typeof section]];
}

function normalizeConfig(rawConfig: unknown): PingtraceConfig {
  const config = (rawConfig ?? {}) as Record<string, unknown>;
  const dns = asRecord(config.dns);
  const providers = asRecord(config.providers);
  const ping = asRecord(config.ping);
  const trace = asRecord(config.trace);

  return {
    dns: {
      privateServers: normalizeServerList(
        dns.privateServers ?? dns.privateServer ?? DEFAULT_CONFIG.dns.privateServers,
      ),
      publicServers: normalizeServerList(
        dns.publicServers ?? dns.publicServer ?? DEFAULT_CONFIG.dns.publicServers,
      ),
    },
    providers: {
      ipinfoToken: normalizeString(providers.ipinfoToken, DEFAULT_CONFIG.providers.ipinfoToken),
      peeringdbEnabled: normalizeBoolean(
        providers.peeringdbEnabled,
        DEFAULT_CONFIG.providers.peeringdbEnabled,
      ),
    },
    ping: {
      packetSize: normalizeNumber(ping.packetSize, DEFAULT_CONFIG.ping.packetSize),
      packetCount: normalizeNumber(ping.packetCount, DEFAULT_CONFIG.ping.packetCount),
      timeoutSeconds: normalizeNumber(ping.timeoutSeconds, DEFAULT_CONFIG.ping.timeoutSeconds),
    },
    trace: {
      maxHops: normalizeNumber(trace.maxHops, DEFAULT_CONFIG.trace.maxHops),
      timeoutSeconds: normalizeNumber(trace.timeoutSeconds, DEFAULT_CONFIG.trace.timeoutSeconds),
      numericOnly: normalizeBoolean(trace.numericOnly, DEFAULT_CONFIG.trace.numericOnly),
    },
  };
}

function normalizeServerList(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value
      .filter((entry): entry is string => typeof entry === "string")
      .flatMap((entry) => resolveInlineTargets(entry));
  }

  if (typeof value === "string") {
    return value.trim().length > 0 ? resolveInlineTargets(value) : [];
  }

  return [];
}

function normalizeString(value: unknown, fallback: string): string {
  return typeof value === "string" ? value : fallback;
}

function normalizeNumber(value: unknown, fallback: number): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function normalizeBoolean(value: unknown, fallback: boolean): boolean {
  return typeof value === "boolean" ? value : fallback;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" ? (value as Record<string, unknown>) : {};
}

function areConfigsEqual(left: PingtraceConfig, right: PingtraceConfig): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

function syncMirrorConfig(config: PingtraceConfig): void {
  mkdirSync(path.dirname(MIRROR_CONFIG_PATH), { recursive: true });
  writeFileSync(MIRROR_CONFIG_PATH, `${JSON.stringify(config, null, 2)}\n`, "utf8");
}
