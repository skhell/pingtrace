import { createRequire } from "node:module";
import { Command } from "commander";

import { registerConfigCommands } from "./commands/config.js";
import { handleRunCommand } from "./commands/run.js";

const require = createRequire(import.meta.url);
const { version } = require("../package.json") as { version: string };

export function createCli(): Command {
  const program = new Command();

  program
    .name("pingtrace")
    .description("Run ping and traceroute workflows from a single CLI.")
    .version(version, "--version", "Show pingtrace version")
    .argument("[input]", "Target, comma-separated targets, or CIDR block")
    .option("-f, --file <path>", "Load targets from a CSV file")
    .option(
      "-e, --export [path]",
      "Export operation-specific CSV files. If omitted, files are created in the current working directory.",
    )
    .option("--summary", "Show compact summary output instead of detailed tables")
    .option("--no-ping", "Disable ping execution")
    .option("--no-trace", "Disable traceroute execution")
    .showHelpAfterError()
    .addHelpText(
      "after",
      `
Examples:
  pingtrace 8.8.8.8
  pingtrace 8.8.8.8 --summary
  pingtrace google.com,1.1.1.1 --export
  pingtrace 8.8.8.8 --export ./reports
  pingtrace 10.0.0.0/30
  pingtrace --file ./targets.csv --no-trace
  pingtrace config list
`,
    )
    .action(handleRunCommand);

  program
    .command("help")
    .description("Show help for pingtrace")
    .action(() => program.outputHelp());

  registerConfigCommands(program);

  return program;
}
