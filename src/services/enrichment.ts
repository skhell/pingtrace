import { Resolver } from "node:dns/promises";
import { isIP } from "node:net";

import type { PingtraceConfig } from "../domain/types.js";

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
  privateDns: string;
  publicDns: string;
  region: string;
}

const EMPTY_ENRICHMENT: IpEnrichment = {
  asn: "",
  city: "",
  country: "",
  org: "",
  privateDns: "",
  publicDns: "",
  region: "",
};

export class EnrichmentService {
  private readonly config: PingtraceConfig;
  private readonly privateResolver?: Resolver;
  private readonly publicResolver?: Resolver;
  private readonly reverseCache = new Map<string, Promise<string>>();
  private readonly ipinfoCache = new Map<string, Promise<Omit<IpEnrichment, "privateDns" | "publicDns">>>();

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

  async enrichIp(ip: string): Promise<IpEnrichment> {
    if (isIP(ip) === 0) {
      return EMPTY_ENRICHMENT;
    }

    const [privateDns, publicDns, ipinfo] = await Promise.all([
      this.reverseLookup(ip, this.privateResolver, "private"),
      this.reverseLookup(ip, this.publicResolver, "public"),
      this.fetchIpinfo(ip),
    ]);

    return {
      ...ipinfo,
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

    const cacheKey = `${scope}:${ip}`;
    const cached = this.reverseCache.get(cacheKey);
    if (cached) {
      return cached;
    }

    const lookupPromise = resolver
      .reverse(ip)
      .then((names) => names[0] ?? "")
      .catch(() => "");

    this.reverseCache.set(cacheKey, lookupPromise);
    return lookupPromise;
  }

  private fetchIpinfo(ip: string): Promise<Omit<IpEnrichment, "privateDns" | "publicDns">> {
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
