export interface PingtraceConfig {
  dns: {
    privateServers: string[];
    publicServers: string[];
  };
  providers: {
    ipinfoToken: string;
    peeringdbEnabled: boolean;
  };
  ping: {
    packetSize: number;
    packetCount: number;
    timeoutSeconds: number;
  };
  trace: {
    maxHops: number;
    timeoutSeconds: number;
    numericOnly: boolean;
  };
}

export interface RunCommandOptions {
  file?: string;
  export?: string | boolean;
  ping: boolean;
  trace: boolean;
  summary?: boolean;
  wide?: boolean;
  columns?: string;
}

export interface RenderOptions {
  wide?: boolean;
  columns?: string[];
}

export type OperationKind = "ping" | "trace";
export type TargetSource = "argument" | "csv" | "cidr";

export interface ResolvedTarget {
  value: string;
  source: TargetSource;
  originalInput: string;
}

export interface ExecutionPlan {
  operations: OperationKind[];
  targets: ResolvedTarget[];
  exportPath?: string;
  verbose?: boolean;
  bulk?: boolean;
  render?: RenderOptions;
}

export interface ProbeResult {
  target: string;
  source: TargetSource;
  operation: OperationKind;
  status: "completed" | "failed";
  summary: string;
  notes: string;
  durationMs: number;
  command: string;
  detailRows?: Record<string, string>[];
}
