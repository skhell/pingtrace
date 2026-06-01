import chalk from "chalk";
import { intro, log, outro } from "@clack/prompts";
import { isCI } from "ci-info";
import path from "node:path";

import type { ExecutionPlan, OperationKind, RenderOptions, RunCommandOptions } from "../domain/types.js";
import { exportResults, type ExportedResultFile } from "../services/exporter.js";
import { runProbePlan } from "../services/probe-runner.js";
import { resolveTargets } from "../services/target-resolver.js";

export async function handleRunCommand(
  input: string | undefined,
  options: RunCommandOptions,
): Promise<void> {
  try {
    if (!input && !options.file) {
      console.error("Provide a target, comma-separated targets, CIDR block, or --file <path>.");
      process.exitCode = 1;
      return;
    }

    const operations = resolveOperations(options);
    if (operations.length === 0) {
      console.error("At least one operation must be enabled. Remove --no-ping or --no-trace.");
      process.exitCode = 1;
      return;
    }

    const targets = await resolveTargets({
      ...(input ? { input } : {}),
      ...(options.file ? { csvPath: options.file } : {}),
    });

    if (targets.length === 0) {
      console.error("No valid targets were resolved from the provided input.");
      process.exitCode = 1;
      return;
    }

    const exportPath = resolveExportPath(options.export);
    const isBulk = targets.length > BULK_THRESHOLD;
    const effectiveExportPath = isBulk && !exportPath ? process.cwd() : exportPath;

    const render: RenderOptions = {
      ...(options.wide ? { wide: true } : {}),
      ...(options.columns
        ? { columns: options.columns.split(",").map((c) => c.trim()).filter(Boolean) }
        : {}),
    };

    const plan: ExecutionPlan = {
      operations,
      targets,
      ...(effectiveExportPath ? { exportPath: effectiveExportPath } : {}),
      verbose: isBulk ? false : !options.summary,
      bulk: isBulk,
      render,
    };

    renderPlan(plan);

    const results = await runProbePlan(plan);

    const exportedFiles = effectiveExportPath ? await exportResults(results, effectiveExportPath) : [];

    renderSummary(results, exportedFiles);
  } catch (error) {
    console.error(formatError(error));
    process.exitCode = 1;
  }
}

function resolveOperations(options: RunCommandOptions): OperationKind[] {
  const operations: OperationKind[] = [];

  if (options.ping) {
    operations.push("ping");
  }

  if (options.trace) {
    operations.push("trace");
  }

  return operations;
}

function resolveExportPath(exportOption: string | boolean | undefined): string | undefined {
  if (!exportOption) {
    return undefined;
  }

  if (typeof exportOption === "string") {
    return path.resolve(process.cwd(), exportOption);
  }

  return process.cwd();
}

const INLINE_LIST_THRESHOLD = 8;
const BULK_THRESHOLD = 254;

function renderPlan(plan: ExecutionPlan): void {
  const headerLine = `Targets: ${plan.targets.length}  -  Operations: ${plan.operations.join(", ")}`;

  if (isCI) {
    console.log(chalk.bold("pingtrace"));
    console.log(headerLine);
  } else {
    intro(chalk.bold.cyan("pingtrace"));
    log.info(headerLine);
  }

  if (plan.bulk) {
    const msg = `Bulk mode: ${plan.targets.length} targets exceed /24 - running concurrently, streaming disabled.`;
    isCI ? console.log(chalk.yellow(msg)) : log.warn(msg);
    if (plan.exportPath) {
      const exportMsg = `CSV export: ${plan.exportPath}`;
      isCI ? console.log(exportMsg) : log.info(exportMsg);
    }
  } else {
    if (plan.exportPath) {
      const msg = `CSV export directory: ${plan.exportPath}`;
      isCI ? console.log(msg) : log.info(msg);
    }
    if (!plan.verbose) {
      isCI ? console.log("Output: summary only") : log.info("Output: summary only");
    }
  }

  const lines: string[] = [];
  if (plan.targets.length <= INLINE_LIST_THRESHOLD) {
    for (const target of plan.targets) {
      lines.push(`- ${target.value} (${target.source})`);
    }
  } else {
    for (const [label, count] of groupTargets(plan.targets)) {
      lines.push(`- ${count} target(s) from ${label}`);
    }
  }
  if (lines.length > 0) {
    if (isCI) {
      for (const l of lines) console.log(`  ${l}`);
    } else {
      log.message(lines.join("\n"));
    }
  }

  if (isCI) {
    console.log(chalk.dim("Running probes...\n"));
  } else {
    log.step("Running probes...");
  }
}

function groupTargets(targets: ExecutionPlan["targets"]): Map<string, number> {
  const groups = new Map<string, number>();

  for (const target of targets) {
    const label = target.originalInput !== target.value
      ? `${target.originalInput} (${target.source})`
      : target.source;
    groups.set(label, (groups.get(label) ?? 0) + 1);
  }

  return groups;
}

function renderSummary(
  results: Array<{ status: "completed" | "failed" }>,
  exportedFiles: ExportedResultFile[],
): void {
  const completedCount = results.filter((result) => result.status === "completed").length;
  const failedCount = results.length - completedCount;

  const completedMsg = `Completed ${completedCount} probe(s).`;
  isCI ? console.log(chalk.green(completedMsg)) : log.success(completedMsg);

  if (failedCount > 0) {
    const failMsg = `Failed ${failedCount} probe(s).`;
    isCI ? console.log(chalk.yellow(failMsg)) : log.warn(failMsg);
    process.exitCode = 1;
  }

  for (const exportedFile of exportedFiles) {
    const msg = `Wrote ${exportedFile.operation} CSV (${exportedFile.rowCount} row(s)) to ${exportedFile.path}`;
    isCI ? console.log(chalk.green(msg)) : log.success(msg);
  }

  if (!isCI) {
    outro(chalk.cyan("done"));
  }
}

function formatError(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }

  return String(error);
}
