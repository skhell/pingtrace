import chalk from "chalk";
import path from "node:path";

import type { ExecutionPlan, OperationKind, RunCommandOptions } from "../domain/types.js";
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
    const plan: ExecutionPlan = {
      operations,
      targets,
      ...(exportPath ? { exportPath } : {}),
      ...(options.verbose ? { verbose: true } : {}),
    };

    renderPlan(plan);

    const results = await runProbePlan(plan);

    const exportedFiles = exportPath ? await exportResults(results, exportPath) : [];

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

function renderPlan(plan: ExecutionPlan): void {
  console.log(chalk.bold("pingtrace"));
  console.log(`Targets: ${plan.targets.length}`);
  console.log(`Operations: ${plan.operations.join(", ")}`);

  if (plan.exportPath) {
    console.log(`CSV export directory: ${plan.exportPath}`);
  }

  if (plan.verbose) {
    console.log("Verbose: enabled");
  }

  for (const target of plan.targets) {
    console.log(`- ${target.value} (${target.source})`);
  }

  console.log(chalk.dim("Running probes...\n"));
}

function renderSummary(
  results: Array<{ status: "completed" | "failed" }>,
  exportedFiles: ExportedResultFile[],
): void {
  const completedCount = results.filter((result) => result.status === "completed").length;
  const failedCount = results.length - completedCount;

  console.log(chalk.green(`Completed ${completedCount} probe(s).`));

  if (failedCount > 0) {
    console.log(chalk.yellow(`Failed ${failedCount} probe(s).`));
    process.exitCode = 1;
  }

  for (const exportedFile of exportedFiles) {
    console.log(
      chalk.green(
        `Wrote ${exportedFile.operation} CSV (${exportedFile.rowCount} row(s)) to ${exportedFile.path}`,
      ),
    );
  }
}

function formatError(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }

  return String(error);
}
