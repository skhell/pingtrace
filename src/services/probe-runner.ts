import { spawn } from "node:child_process";
import os from "node:os";
import process from "node:process";

import chalk from "chalk";

import { getRuntimeConfig } from "../config/store.js";
import type { ExecutionPlan, OperationKind, ProbeResult, ResolvedTarget } from "../domain/types.js";
import { EnrichmentService } from "./enrichment.js";
import { StreamingPingRenderer, StreamingTraceRenderer } from "./verbose-output.js";

interface CommandSpec {
  command: string;
  args: string[];
}

interface CommandExecution {
  stdout: string;
  stderr: string;
  exitCode: number | null;
  durationMs: number;
  missing: boolean;
}

const BULK_THRESHOLD = 254;
const BULK_CONCURRENCY = 10;

class Semaphore {
  private running = 0;
  private readonly queue: Array<() => void> = [];

  constructor(private readonly limit: number) {}

  async run<T>(fn: () => Promise<T>): Promise<T> {
    await this.acquire();
    try {
      return await fn();
    } finally {
      this.release();
    }
  }

  private acquire(): Promise<void> {
    if (this.running < this.limit) {
      this.running++;
      return Promise.resolve();
    }
    return new Promise((resolve) => this.queue.push(resolve));
  }

  private release(): void {
    this.running--;
    const next = this.queue.shift();
    if (next) {
      this.running++;
      next();
    }
  }
}

export async function runProbePlan(plan: ExecutionPlan): Promise<ProbeResult[]> {
  const config = getRuntimeConfig();
  const platform = os.platform();
  const enrichmentService = new EnrichmentService(config);

  if (plan.bulk) {
    return runBulkPlan(plan, config, platform, enrichmentService);
  }

  const results: ProbeResult[] = [];

  for (const target of plan.targets) {
    console.log(chalk.bold(`${target.value}`));

    for (const operation of plan.operations) {
      const result = await runOperation(
        target,
        operation,
        platform,
        config.ping,
        config.trace,
        plan.verbose ?? true,
        enrichmentService,
      );

      renderProbeResult(result);
      results.push(result);
    }

    console.log("");
  }

  return results;
}

async function runBulkPlan(
  plan: ExecutionPlan,
  config: ReturnType<typeof getRuntimeConfig>,
  platform: NodeJS.Platform,
  enrichmentService: EnrichmentService,
): Promise<ProbeResult[]> {
  const semaphore = new Semaphore(BULK_CONCURRENCY);
  let completed = 0;
  const total = plan.targets.length;

  const promises = plan.targets.map((target) =>
    semaphore.run(async () => {
      const targetResults: ProbeResult[] = [];

      for (const operation of plan.operations) {
        const result = await runOperation(
          target,
          operation,
          platform,
          config.ping,
          config.trace,
          false,
          enrichmentService,
        );
        targetResults.push(result);
      }

      completed++;
      const progress = `[${completed}/${total}]`;
      const parts = targetResults.map((r) => {
        const label = r.status === "completed" ? chalk.green("ok") : chalk.red("fail");
        return `${label} ${r.operation}: ${r.summary}`;
      });
      console.log(`  ${chalk.bold(target.value.padEnd(18))}  ${parts.join("  |  ")}  ${chalk.dim(progress)}`);

      return targetResults;
    }),
  );

  const allResults = await Promise.all(promises);
  return allResults.flat();
}

async function runOperation(
  target: ResolvedTarget,
  operation: OperationKind,
  platform: NodeJS.Platform,
  pingConfig: { packetSize: number; packetCount: number; timeoutSeconds: number },
  traceConfig: { maxHops: number; timeoutSeconds: number; numericOnly: boolean },
  verbose: boolean,
  enrichmentService: EnrichmentService,
): Promise<ProbeResult> {
  if (operation === "ping") {
    return runPing(target, platform, pingConfig, verbose, enrichmentService);
  }

  return runTrace(target, platform, traceConfig, verbose, enrichmentService);
}

async function runPing(
  target: ResolvedTarget,
  platform: NodeJS.Platform,
  pingConfig: { packetSize: number; packetCount: number; timeoutSeconds: number },
  verbose: boolean,
  enrichmentService: EnrichmentService,
): Promise<ProbeResult> {
  const spec = createPingCommand(target.value, platform, pingConfig);

  if (verbose) {
    const renderer = new StreamingPingRenderer(target.value, enrichmentService);

    if (platform !== "win32") {
      // Unix: run ping -c 1 per packet to bypass C library pipe buffering.
      const { missing, durationMs } = await streamPingPackets(
        target.value,
        platform,
        pingConfig,
        renderer,
      );
      renderer.finish();

      if (missing) {
        return makeMissingResult(target, "ping", formatCommand(spec));
      }

      return {
        target: target.value,
        source: target.source,
        operation: "ping",
        status: renderer.hasAnyReply() ? "completed" : "failed",
        summary: renderer.getSummary(durationMs),
        notes: renderer.getSummary(durationMs),
        durationMs,
        command: formatCommand(spec),
        detailRows: renderer.getRows(),
      };
    }

    // Windows: stream via promise-chain (console output is usually not buffered).
    const execution = await executeCommandStreaming(spec, (line) => renderer.processLine(line));
    renderer.finish();

    const combinedOutput = [execution.stdout, execution.stderr].filter(Boolean).join("\n");
    const summary = summarizePing(
      combinedOutput,
      execution.durationMs,
      execution.exitCode,
      execution.missing,
    );

    return {
      target: target.value,
      source: target.source,
      operation: "ping",
      status: execution.missing || execution.exitCode !== 0 ? "failed" : "completed",
      summary,
      notes: summarizeCommandOutput(combinedOutput),
      durationMs: execution.durationMs,
      command: formatCommand(spec),
      detailRows: renderer.getRows(),
    };
  }

  // Summary (non-verbose) mode - buffered.
  const execution = await executeCommand(spec);
  const combinedOutput = [execution.stdout, execution.stderr].filter(Boolean).join("\n");
  const summary = summarizePing(
    combinedOutput,
    execution.durationMs,
    execution.exitCode,
    execution.missing,
  );

  return {
    target: target.value,
    source: target.source,
    operation: "ping",
    status: execution.missing || execution.exitCode !== 0 ? "failed" : "completed",
    summary,
    notes: summarizeCommandOutput(combinedOutput),
    durationMs: execution.durationMs,
    command: formatCommand(spec),
  };
}

async function runTrace(
  target: ResolvedTarget,
  platform: NodeJS.Platform,
  traceConfig: { maxHops: number; timeoutSeconds: number; numericOnly: boolean },
  verbose: boolean,
  enrichmentService: EnrichmentService,
): Promise<ProbeResult> {
  const candidates = createTraceCommands(target.value, platform, traceConfig);

  if (verbose && platform !== "win32") {
    // Unix: run traceroute one hop at a time to bypass C library pipe buffering.
    const renderer = new StreamingTraceRenderer(enrichmentService, target.value);
    const result = await streamTraceHops(target.value, platform, traceConfig, renderer);

    if (!result.missing) {
      renderer.finish();
      const status = result.exitCode === 0 || result.stdout.length > 0 ? "completed" : "failed";

      return {
        target: target.value,
        source: target.source,
        operation: "trace",
        status,
        summary: summarizeTrace(result.stdout, result.durationMs, result.exitCode),
        notes: summarizeCommandOutput(result.stdout),
        durationMs: result.durationMs,
        command: formatCommand(candidates[0] ?? { command: "traceroute", args: [] }),
        detailRows: renderer.getRows(),
      };
    }

    // traceroute not found on Unix - try tracepath with streaming fallback.
    const tracepathSpec = candidates.find((c) => c.command === "tracepath");
    if (tracepathSpec) {
      const tracepathRenderer = new StreamingTraceRenderer(enrichmentService, target.value);
      const execution = await executeCommandStreaming(tracepathSpec, (line) =>
        tracepathRenderer.processLine(line),
      );
      if (!execution.missing) {
        tracepathRenderer.finish();
        const combinedOutput = [execution.stdout, execution.stderr].filter(Boolean).join("\n");

        return {
          target: target.value,
          source: target.source,
          operation: "trace",
          status: execution.exitCode === 0 ? "completed" : "failed",
          summary: summarizeTrace(combinedOutput, execution.durationMs, execution.exitCode),
          notes: summarizeCommandOutput(combinedOutput),
          durationMs: execution.durationMs,
          command: formatCommand(tracepathSpec),
          detailRows: tracepathRenderer.getRows(),
        };
      }
    }

    return makeNoTracerouteResult(target, candidates);
  }

  // Summary mode or Windows: buffered execution.
  for (const spec of candidates) {
    let execution: CommandExecution;
    let verboseRenderer: StreamingTraceRenderer | undefined;

    if (verbose) {
      // Windows verbose: stream via promise-chain.
      verboseRenderer = new StreamingTraceRenderer(enrichmentService, target.value);
      execution = await executeCommandStreaming(spec, (line) => verboseRenderer!.processLine(line));
      if (execution.missing) continue;
      verboseRenderer.finish();
    } else {
      execution = await executeCommand(spec);
      if (execution.missing) continue;
    }

    const combinedOutput = [execution.stdout, execution.stderr].filter(Boolean).join("\n");

    return {
      target: target.value,
      source: target.source,
      operation: "trace",
      status: execution.exitCode === 0 ? "completed" : "failed",
      summary: summarizeTrace(combinedOutput, execution.durationMs, execution.exitCode),
      notes: summarizeCommandOutput(combinedOutput),
      durationMs: execution.durationMs,
      command: formatCommand(spec),
      detailRows: verboseRenderer?.getRows() ?? [],
    };
  }

  return makeNoTracerouteResult(target, candidates);
}

// Runs ping -c 1 once per packet so each process exits immediately,
// bypassing the C library's full-buffering on pipes.
async function streamPingPackets(
  target: string,
  platform: NodeJS.Platform,
  pingConfig: { packetSize: number; packetCount: number; timeoutSeconds: number },
  renderer: StreamingPingRenderer,
): Promise<{ missing: boolean; durationMs: number }> {
  const startedAt = Date.now();
  const singlePacketConfig = { ...pingConfig, packetCount: 1 };

  for (let i = 0; i < pingConfig.packetCount; i++) {
    const spec = createPingCommand(target, platform, singlePacketConfig);
    const exec = await executeCommand(spec);

    if (exec.missing) {
      return { missing: true, durationMs: Date.now() - startedAt };
    }

    const output = [exec.stdout, exec.stderr].filter(Boolean).join("\n");
    for (const line of output.split(/\r?\n/)) {
      await renderer.processLine(line);
    }
  }

  return { missing: false, durationMs: Date.now() - startedAt };
}

// Runs traceroute -f <hop> -m <hop> one hop at a time on Unix,
// bypassing C library pipe buffering and showing each hop as it arrives.
async function streamTraceHops(
  target: string,
  platform: NodeJS.Platform,
  traceConfig: { maxHops: number; timeoutSeconds: number; numericOnly: boolean },
  renderer: StreamingTraceRenderer,
): Promise<{ stdout: string; exitCode: number | null; missing: boolean; durationMs: number }> {
  const startedAt = Date.now();
  let combinedOutput = "";
  let resolvedTargetIp = "";

  for (let hop = 1; hop <= traceConfig.maxHops; hop++) {
    const spec = createSingleHopTraceCommand(target, platform, traceConfig, hop);
    const exec = await executeCommand(spec);

    if (exec.missing) {
      return { stdout: combinedOutput, exitCode: null, missing: true, durationMs: Date.now() - startedAt };
    }

    const output = [exec.stdout, exec.stderr].filter(Boolean).join("\n");

    // Extract the resolved destination IP from the first hop's header line.
    if (hop === 1 && !resolvedTargetIp) {
      const m = output.match(/traceroute to .+?\((\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})\)/i);
      resolvedTargetIp = m?.[1] ?? target;
    }

    combinedOutput += output + "\n";

    for (const line of output.split(/\r?\n/)) {
      await renderer.processLine(line);
    }

    // Stop once the destination IP appears in a hop reply.
    if (resolvedTargetIp) {
      const hopLines = output.split(/\r?\n/).filter((l) => /^\s*\d+\s+/.test(l));
      if (hopLines.some((l) => l.includes(resolvedTargetIp))) break;
    }
  }

  return { stdout: combinedOutput, exitCode: 0, missing: false, durationMs: Date.now() - startedAt };
}

function createSingleHopTraceCommand(
  target: string,
  platform: NodeJS.Platform,
  traceConfig: { maxHops: number; timeoutSeconds: number; numericOnly: boolean },
  hop: number,
): CommandSpec {
  const args: string[] = [];

  if (traceConfig.numericOnly) args.push("-n");
  args.push("-f", String(hop), "-m", String(hop), "-w", String(traceConfig.timeoutSeconds), target);

  return { command: platform === "darwin" ? "traceroute" : "traceroute", args };
}

function makeMissingResult(
  target: ResolvedTarget,
  operation: OperationKind,
  command: string,
): ProbeResult {
  return {
    target: target.value,
    source: target.source,
    operation,
    status: "failed",
    summary: `${operation === "ping" ? "ping" : "traceroute"} command not found`,
    notes: `${operation === "ping" ? "ping" : "traceroute"} command not found`,
    durationMs: 0,
    command,
  };
}

function makeNoTracerouteResult(target: ResolvedTarget, candidates: CommandSpec[]): ProbeResult {
  return {
    target: target.value,
    source: target.source,
    operation: "trace",
    status: "failed",
    summary: "traceroute command not found",
    notes: "Tried traceroute/tracert command candidates but none were available on this system.",
    durationMs: 0,
    command: candidates.map((c) => c.command).join(" | "),
  };
}

function createPingCommand(
  target: string,
  platform: NodeJS.Platform,
  pingConfig: { packetSize: number; packetCount: number; timeoutSeconds: number },
): CommandSpec {
  const timeoutMs = pingConfig.timeoutSeconds * 1000;

  if (platform === "win32") {
    return {
      command: "ping",
      args: [
        "-n",
        String(pingConfig.packetCount),
        "-l",
        String(pingConfig.packetSize),
        "-w",
        String(timeoutMs),
        target,
      ],
    };
  }

  const args = ["-c", String(pingConfig.packetCount), "-s", String(pingConfig.packetSize)];

  if (platform === "darwin") {
    args.push("-W", String(timeoutMs));
  } else {
    args.push("-W", String(pingConfig.timeoutSeconds));
  }

  args.push(target);

  return { command: "ping", args };
}

function createTraceCommands(
  target: string,
  platform: NodeJS.Platform,
  traceConfig: { maxHops: number; timeoutSeconds: number; numericOnly: boolean },
): CommandSpec[] {
  if (platform === "win32") {
    const args = [
      "-h",
      String(traceConfig.maxHops),
      "-w",
      String(traceConfig.timeoutSeconds * 1000),
    ];

    if (traceConfig.numericOnly) {
      args.unshift("-d");
    }

    return [{ command: "tracert", args: [...args, target] }];
  }

  const tracerouteArgs = [];

  if (traceConfig.numericOnly) {
    tracerouteArgs.push("-n");
  }

  tracerouteArgs.push(
    "-m",
    String(traceConfig.maxHops),
    "-w",
    String(traceConfig.timeoutSeconds),
    target,
  );

  if (platform === "darwin") {
    return [{ command: "traceroute", args: tracerouteArgs }];
  }

  return [
    { command: "traceroute", args: tracerouteArgs },
    {
      command: "tracepath",
      args: [
        ...(traceConfig.numericOnly ? ["-n"] : []),
        "-m",
        String(traceConfig.maxHops),
        target,
      ],
    },
  ];
}

function executeCommand(spec: CommandSpec): Promise<CommandExecution> {
  return new Promise((resolve) => {
    const startedAt = Date.now();
    const child = spawn(spec.command, spec.args, {
      stdio: ["ignore", "pipe", "pipe"],
      env: process.env,
    });

    let stdout = "";
    let stderr = "";
    let settled = false;

    child.stdout.on("data", (chunk: Buffer | string) => {
      stdout += chunk.toString();
    });

    child.stderr.on("data", (chunk: Buffer | string) => {
      stderr += chunk.toString();
    });

    child.on("error", (error) => {
      if (settled) return;
      settled = true;
      resolve({
        stdout,
        stderr: error.message,
        exitCode: null,
        durationMs: Date.now() - startedAt,
        missing: (error as NodeJS.ErrnoException).code === "ENOENT",
      });
    });

    child.on("close", (exitCode) => {
      if (settled) return;
      settled = true;
      resolve({ stdout, stderr, exitCode, durationMs: Date.now() - startedAt, missing: false });
    });
  });
}

// Fallback streaming function used for Windows and tracepath.
// On Unix, the C library buffers pipe output so this may not stream
// per-line; use streamPingPackets / streamTraceHops for true streaming.
function executeCommandStreaming(
  spec: CommandSpec,
  onLine: (line: string) => Promise<void>,
): Promise<CommandExecution> {
  return new Promise((resolve) => {
    const startedAt = Date.now();
    const child = spawn(spec.command, spec.args, {
      stdio: ["ignore", "pipe", "pipe"],
      env: process.env,
    });

    let stdout = "";
    let stderr = "";
    let settled = false;
    let lineBuffer = "";
    let processingChain: Promise<void> = Promise.resolve();

    child.stdout.on("data", (chunk: Buffer | string) => {
      const text = chunk.toString();
      stdout += text;
      lineBuffer += text;

      const parts = lineBuffer.split(/\r?\n/);
      lineBuffer = parts.pop() ?? "";

      for (const line of parts) {
        const captured = line;
        processingChain = processingChain.then(() => onLine(captured));
      }
    });

    child.stderr.on("data", (chunk: Buffer | string) => {
      stderr += chunk.toString();
    });

    child.on("error", (error) => {
      if (settled) return;
      settled = true;
      resolve({
        stdout,
        stderr: error.message,
        exitCode: null,
        durationMs: Date.now() - startedAt,
        missing: (error as NodeJS.ErrnoException).code === "ENOENT",
      });
    });

    child.on("close", (exitCode) => {
      if (settled) return;

      if (lineBuffer.length > 0) {
        const remaining = lineBuffer;
        lineBuffer = "";
        processingChain = processingChain.then(() => onLine(remaining));
      }

      void processingChain.then(() => {
        if (settled) return;
        settled = true;
        resolve({ stdout, stderr, exitCode, durationMs: Date.now() - startedAt, missing: false });
      });
    });
  });
}

function summarizePing(
  output: string,
  durationMs: number,
  exitCode: number | null,
  missing: boolean,
): string {
  if (missing) return "ping command not found";

  const packetLoss = output.match(/(\d+(?:\.\d+)?)%\s*(?:packet )?loss/i)?.[1];
  const unixAverage = output.match(/=\s*[\d.]+\/([\d.]+)\/[\d.]+\/[\d.]+\s*ms/i)?.[1];
  const windowsAverage = output.match(/Average\s*=\s*(\d+)ms/i)?.[1];
  const average = unixAverage ?? windowsAverage;

  if (packetLoss && average) return `loss ${packetLoss}%, avg ${average} ms`;
  if (packetLoss) return `loss ${packetLoss}%, duration ${durationMs} ms`;
  if (exitCode !== null) return `ping exited with code ${exitCode}`;

  return summarizeCommandOutput(output);
}

function summarizeTrace(output: string, durationMs: number, exitCode: number | null): string {
  const hopMatches = [...output.matchAll(/^\s*(\d+)\s+/gm)];
  const lastHop = hopMatches.at(-1)?.[1];

  if (lastHop) return `completed in ${lastHop} hop(s)`;
  if (exitCode !== null) return `trace exited with code ${exitCode}`;

  return `finished in ${durationMs} ms`;
}

function summarizeCommandOutput(output: string): string {
  const line = output
    .split(/\r?\n/)
    .map((entry) => entry.trim())
    .find((entry) => entry.length > 0);

  return line ?? "No output captured.";
}

function formatCommand(spec: CommandSpec): string {
  return [spec.command, ...spec.args].join(" ");
}

function renderProbeResult(result: ProbeResult): void {
  const statusLabel = result.status === "completed" ? chalk.green("ok") : chalk.red("fail");
  console.log(`  ${statusLabel} ${result.operation}: ${result.summary}`);
}
