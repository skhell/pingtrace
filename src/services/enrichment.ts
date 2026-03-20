import { Resolver } from "node:dns/promises";
import { isIP } from "node:net";

import chalk from "chalk";

import type { PingtraceConfig } from "../domain/types.js";

const PRIVATE_DNS_TIMEOUT_MS = 5000;

interface IpInfoPayload {
  city?: string;
  country?: string;
  hostname?: string;
  org?: string;
  region?: string;
  asn?: {
    asn?: string;
    name?: string;
  };
}

export interface IpEnrichment {
  asn: string;
  city: string;
  country: string;
  org: string;
  peeringdbPolicy: string;
  peeringdbType: string;
  privateDns: string;
  publicDns: string;
  region: string;
}

interface PeeringDbPayload {
  data?: Array<{
    info_type?: string;
    policy_general?: string;
  }>;
}

const EMPTY_ENRICHMENT: IpEnrichment = {
  asn: "",
  city: "",
  country: "",
  org: "",
  peeringdbPolicy: "",
  peeringdbType: "",
  privateDns: "",
  publicDns: "",
  region: "",
};

export class EnrichmentService {
  private readonly config: PingtraceConfig;
  private readonly privateResolver?: Resolver;
  private readonly publicResolver?: Resolver;
  private readonly reverseCache = new Map<string, Promise<string>>();
  private readonly ipinfoCache = new Map<string, Promise<Omit<IpEnrichment, "privateDns" | "publicDns" | "peeringdbType" | "peeringdbPolicy">>>();
  private readonly peeringdbCache = new Map<string, Promise<{ peeringdbType: string; peeringdbPolicy: string }>>();
  private privateDnsTimedOut = false;

  constructor(config: PingtraceConfig) {
    this.config = config;

    if (config.dns.privateServers.length > 0) {
      this.privateResolver = new Resolver();
      this.privateResolver.setServers(config.dns.privateServers);
    }

    if (config.dns.publicServers.length > 0) {
      this.publicResolver = new Resolver();
      this.publicResolver.setServers(config.dns.publicServers);
    }
  }

  hasPeeringDb(): boolean {
    return this.config.providers.peeringdbEnabled;
  }

  hasPrivateDns(): boolean {
    return !!this.privateResolver && !this.privateDnsTimedOut;
  }

  hasPublicDns(): boolean {
    return !!this.publicResolver;
  }

  hasIpinfo(): boolean {
    return !!this.config.providers.ipinfoToken;
  }

  async enrichIp(ip: string): Promise<IpEnrichment> {    if (isIP(ip) === 0) {
      return EMPTY_ENRICHMENT;
    }

    const [privateDns, publicDns, ipinfo] = await Promise.all([
      this.reverseLookup(ip, this.privateResolver, "private"),
      this.reverseLookup(ip, this.publicResolver, "public"),
      this.fetchIpinfo(ip),
    ]);

    const peeringdb = await this.fetchPeeringDb(ipinfo.asn);

    return {
      ...ipinfo,
      ...peeringdb,
      privateDns,
      publicDns,
    };
  }

  formatLocation(enrichment: Pick<IpEnrichment, "city" | "country" | "region">): string {
    return [enrichment.city, enrichment.region, enrichment.country].filter(Boolean).join(", ");
  }

  private reverseLookup(
    ip: string,
    resolver: Resolver | undefined,
    scope: "private" | "public",
  ): Promise<string> {
    if (!resolver) {
      return Promise.resolve("");
    }

    if (scope === "private" && this.privateDnsTimedOut) {
      return Promise.resolve("");
    }

    const cacheKey = `${scope}:${ip}`;
    const cached = this.reverseCache.get(cacheKey);
    if (cached) {
      return cached;
    }

    const resolvePromise = resolver
      .reverse(ip)
      .then((names) => names[0] ?? "")
      .catch(() => "");

    const lookupPromise =
      scope === "private"
        ? Promise.race([
            resolvePromise,
            new Promise<string>((resolve) =>
              setTimeout(() => {
                this.privateDnsTimedOut = true;
                console.warn(
                  chalk.yellow(
                    `\n[pingtrace] Warning: Private DNS lookup timed out after ${PRIVATE_DNS_TIMEOUT_MS / 1000}s. ` +
                      `Private DNS enrichment will be skipped for remaining targets.`,
                  ),
                );
                resolve("");
              }, PRIVATE_DNS_TIMEOUT_MS),
            ),
          ])
        : resolvePromise;

    this.reverseCache.set(cacheKey, lookupPromise);
    return lookupPromise;
  }

  private fetchPeeringDb(asn: string): Promise<{ peeringdbType: string; peeringdbPolicy: string }> {
    const empty = { peeringdbType: "", peeringdbPolicy: "" };

    if (!this.config.providers.peeringdbEnabled || !asn) {
      return Promise.resolve(empty);
    }

    const numericAsn = asn.replace(/^AS/i, "");
    if (!numericAsn || !/^\d+$/.test(numericAsn)) {
      return Promise.resolve(empty);
    }

    const cached = this.peeringdbCache.get(numericAsn);
    if (cached) return cached;

    const request = fetch(`https://www.peeringdb.com/api/net?asn=${numericAsn}&depth=0`, {
      signal: AbortSignal.timeout(3000),
    })
      .then(async (response) => {
        if (!response.ok) return empty;
        const payload = (await response.json()) as PeeringDbPayload;
        const net = payload.data?.[0];
        return {
          peeringdbType: net?.info_type ?? "",
          peeringdbPolicy: net?.policy_general ?? "",
        };
      })
      .catch(() => empty);

    this.peeringdbCache.set(numericAsn, request);
    return request;
  }

  private fetchIpinfo(ip: string): Promise<Omit<IpEnrichment, "privateDns" | "publicDns" | "peeringdbType" | "peeringdbPolicy">> {
    if (!this.config.providers.ipinfoToken || isPrivateLikeIp(ip)) {
      return Promise.resolve({
        asn: "",
        city: "",
        country: "",
        org: "",
        region: "",
      });
    }

    const cached = this.ipinfoCache.get(ip);
    if (cached) {
      return cached;
    }

    const request = fetch(`https://ipinfo.io/${ip}/json?token=${this.config.providers.ipinfoToken}`, {
      signal: AbortSignal.timeout(3000),
    })
      .then(async (response) => {
        if (!response.ok) {
          return EMPTY_ENRICHMENT;
        }

        const payload = (await response.json()) as IpInfoPayload;
        const parsedOrg = typeof payload.org === "string" ? payload.org : "";
        const parsedAsn =
          payload.asn?.asn ??
          (parsedOrg.startsWith("AS") ? parsedOrg.split(" ", 1)[0] ?? "" : "");

        return {
          asn: parsedAsn,
          city: payload.city ?? "",
          country: payload.country ?? "",
          org: parsedOrg || (payload.asn?.name ?? ""),
          region: payload.region ?? "",
        };
      })
      .catch(() => ({
        asn: "",
        city: "",
        country: "",
        org: "",
        region: "",
      }));

    this.ipinfoCache.set(ip, request);
    return request;
  }
}

function isPrivateLikeIp(ip: string): boolean {
  const version = isIP(ip);
  if (version !== 4) {
    return version === 6;
  }

  const octets = ip.split(".").map((entry) => Number(entry));
  const a = octets[0] ?? 0;
  const b = octets[1] ?? 0;

  if (a === 10) {
    return true;
  }

  if (a === 127) {
    return true;
  }

  if (a === 169 && b === 254) {
    return true;
  }

  if (a === 172 && b >= 16 && b <= 31) {
    return true;
  }

  if (a === 192 && b === 168) {
    return true;
  }

  return false;
}
