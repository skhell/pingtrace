import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";

import { stringify } from "csv-stringify/sync";

import type { OperationKind, ProbeResult } from "../domain/types.js";

export interface ExportedResultFile {
  operation: OperationKind;
  path: string;
  rowCount: number;
}

export async function exportResults(
  results: ProbeResult[],
  exportPath: string,
): Promise<ExportedResultFile[]> {
  const resolvedPath = path.resolve(process.cwd(), exportPath);
  const exportDirectory =
    path.extname(resolvedPath).toLowerCase() === ".csv" ? path.dirname(resolvedPath) : resolvedPath;
  const timestamp = formatUtcTimestamp();
  const exportedFiles: ExportedResultFile[] = [];

  await mkdir(exportDirectory, { recursive: true });

  for (const operation of ["ping", "trace"] as const) {
    const operationResults = results.filter((result) => result.operation === operation);
    if (operationResults.length === 0) {
      continue;
    }

    const detailRows = operationResults.flatMap((r) => r.detailRows ?? []);
    const hasDetail = detailRows.length > 0;

    const csv = hasDetail
      ? stringify(detailRows, { header: true })
      : stringify(operationResults, {
          header: true,
          columns: [
            { key: "target", header: "target" },
            { key: "source", header: "source" },
            { key: "operation", header: "operation" },
            { key: "status", header: "status" },
            { key: "summary", header: "summary" },
            { key: "notes", header: "notes" },
            { key: "durationMs", header: "durationMs" },
            { key: "command", header: "command" },
          ],
        });

    const rowCount = hasDetail ? detailRows.length : operationResults.length;
    const filePath = path.join(exportDirectory, `${operation}_${timestamp}.csv`);
    await writeFile(filePath, csv, "utf8");
    exportedFiles.push({ operation, path: filePath, rowCount });
  }

  return exportedFiles;
}

function formatUtcTimestamp(): string {
  const now = new Date();
  const year = now.getUTCFullYear();
  const month = String(now.getUTCMonth() + 1).padStart(2, "0");
  const day = String(now.getUTCDate()).padStart(2, "0");
  const hours = String(now.getUTCHours()).padStart(2, "0");
  const minutes = String(now.getUTCMinutes()).padStart(2, "0");
  const seconds = String(now.getUTCSeconds()).padStart(2, "0");

  return `${year}-${month}-${day}-${hours}-${minutes}-${seconds}`;
}
