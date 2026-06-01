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
    .description(
      "Run ping and traceroute side-by-side from one command with DNS, ipinfo.io, and PeeringDB enrichment.",
    )
    .version(version, "--version", "Show pingtrace version")
    .argument("[input]", "Target, comma-separated targets, or CIDR block")
    .option("-f, --file <path>", "Load targets from a CSV file (first column is the target)")
    .option("--no-ping", "Disable ping (requires --no-trace to be unset)")
    .option("--no-trace", "Disable traceroute (requires --no-ping to be unset)")
    .option("--summary", "Show one-line summary per target instead of detailed tables")
    .option(
      "--wide",
      "Disable responsive sizing and render full-width columns (use when piping to a file or wide screen)",
    )
    .option(
      "--columns <list>",
      "Comma-separated columns to render (e.g. seq,ip,time_ms,status). By default columns auto-drop to fit the terminal width.",
    )
    .option(
      "-e, --export [path]",
      "Export per-packet and per-hop CSV files. If a directory is omitted, writes to the current working directory.",
    )
    .showHelpAfterError()
    .addHelpText(
      "after",
      `
Examples:
  pingtrace 8.8.8.8                                # ping + trace one host
  pingtrace 8.8.8.8,1.1.1.1,example.com            # multiple targets
  pingtrace 10.0.0.0/30                            # IPv4 CIDR
  pingtrace --file ./targets.csv --no-trace        # ping only, from CSV
  pingtrace 8.8.8.8 --summary                      # compact one-line output
  pingtrace 8.8.8.8 --columns seq,ip,time_ms,status # script-friendly columns
  pingtrace 8.8.8.8 --wide                         # force full-width tables
  pingtrace 8.8.8.8 --export ./reports             # write CSV to ./reports

Configuration:
  pingtrace config           # interactive editor
  pingtrace config list      # show all values
  pingtrace config --help    # all configuration subcommands
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
