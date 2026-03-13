import { spawn } from "node:child_process";
import os from "node:os";
import process from "node:process";

import chalk from "chalk";

import { getRuntimeConfig } from "../config/store.js";
import type { ExecutionPlan, OperationKind, ProbeResult, ResolvedTarget } from "../domain/types.js";
import { EnrichmentService } from "./enrichment.js";
import { renderVerboseOperation } from "./verbose-output.js";

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

export async function runProbePlan(plan: ExecutionPlan): Promise<ProbeResult[]> {
  const config = getRuntimeConfig();
  const platform = os.platform();
  const results: ProbeResult[] = [];
  const enrichmentService = new EnrichmentService(config);

  for (const target of plan.targets) {
    console.log(chalk.bold(`${target.value}`));

    for (const operation of plan.operations) {
      const result = await runOperation(
        target,
        operation,
        platform,
        config.ping,
        config.trace,
        plan.verbose ?? false,
        enrichmentService,
      );

      renderProbeResult(result);
      results.push(result);
    }

    console.log("");
  }

  return results;
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
  const execution = await executeCommand(spec);
  const combinedOutput = [execution.stdout, execution.stderr].filter(Boolean).join("\n");

  if (verbose) {
    await renderVerboseOperation("ping", target.value, combinedOutput, enrichmentService);
  }

  const summary = summarizePing(
    combinedOutput,
    execution.durationMs,
    execution.exitCode,
    execution.missing,
  );
  const status = execution.missing || execution.exitCode !== 0 ? "failed" : "completed";

  return {
    target: target.value,
    source: target.source,
    operation: "ping",
    status,
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

  for (const spec of candidates) {
    const execution = await executeCommand(spec);
    if (execution.missing) {
      continue;
    }

    const combinedOutput = [execution.stdout, execution.stderr].filter(Boolean).join("\n");

    if (verbose) {
      await renderVerboseOperation("trace", target.value, combinedOutput, enrichmentService);
    }

    const status = execution.exitCode === 0 ? "completed" : "failed";

    return {
      target: target.value,
      source: target.source,
      operation: "trace",
      status,
      summary: summarizeTrace(combinedOutput, execution.durationMs, execution.exitCode),
      notes: summarizeCommandOutput(combinedOutput),
      durationMs: execution.durationMs,
      command: formatCommand(spec),
    };
  }

  return {
    target: target.value,
    source: target.source,
    operation: "trace",
    status: "failed",
    summary: "traceroute command not found",
    notes: "Tried traceroute/tracert command candidates but none were available on this system.",
    durationMs: 0,
    command: candidates.map((candidate) => candidate.command).join(" | "),
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

  return {
    command: "ping",
    args,
  };
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

    return [
      {
        command: "tracert",
        args: [...args, target],
      },
    ];
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
    return [
      {
        command: "traceroute",
        args: tracerouteArgs,
      },
    ];
  }

  return [
    {
      command: "traceroute",
      args: tracerouteArgs,
    },
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
      if (settled) {
        return;
      }

      settled = true;
      const durationMs = Date.now() - startedAt;
      resolve({
        stdout,
        stderr: error.message,
        exitCode: null,
        durationMs,
        missing: (error as NodeJS.ErrnoException).code === "ENOENT",
      });
    });

    child.on("close", (exitCode) => {
      if (settled) {
        return;
      }

      settled = true;
      resolve({
        stdout,
        stderr,
        exitCode,
        durationMs: Date.now() - startedAt,
        missing: false,
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
  if (missing) {
    return "ping command not found";
  }

  const packetLoss = output.match(/(\d+(?:\.\d+)?)%\s*(?:packet )?loss/i)?.[1];
  const unixAverage = output.match(/=\s*[\d.]+\/([\d.]+)\/[\d.]+\/[\d.]+\s*ms/i)?.[1];
  const windowsAverage = output.match(/Average\s*=\s*(\d+)ms/i)?.[1];
  const average = unixAverage ?? windowsAverage;

  if (packetLoss && average) {
    return `loss ${packetLoss}%, avg ${average} ms`;
  }

  if (packetLoss) {
    return `loss ${packetLoss}%, duration ${durationMs} ms`;
  }

  if (exitCode !== null) {
    return `ping exited with code ${exitCode}`;
  }

  return summarizeCommandOutput(output);
}

function summarizeTrace(output: string, durationMs: number, exitCode: number | null): string {
  const hopMatches = [...output.matchAll(/^\s*(\d+)\s+/gm)];
  const lastHop = hopMatches.at(-1)?.[1];

  if (lastHop) {
    return `completed in ${lastHop} hop(s)`;
  }

  if (exitCode !== null) {
    return `trace exited with code ${exitCode}`;
  }

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
