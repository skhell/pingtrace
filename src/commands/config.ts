import process from "node:process";
import { createInterface } from "node:readline/promises";

import chalk from "chalk";
import { Command } from "commander";

import {
  getConfig,
  getMirrorConfigPath,
  getConfigStorePath,
  getConfigValue,
  listConfigEntries,
  resetConfig,
  setConfigValue,
} from "../config/store.js";

export function registerConfigCommands(program: Command): void {
  const configCommand = program
    .command("config")
    .description("Manage pingtrace configuration")
    .addHelpText(
      "after",
      `
Common keys:
  dns.privateServers
  dns.publicServers
  ping.packetSize
  ping.packetCount
  ping.timeoutSeconds
  trace.maxHops
  trace.timeoutSeconds
  trace.numericOnly
`,
    )
    .action(async () => {
      try {
        if (!process.stdin.isTTY || !process.stdout.isTTY) {
          printConfigList();
          return;
        }

        await runInteractiveConfig();
      } catch (error) {
        console.error(formatError(error));
        process.exitCode = 1;
      }
    });

  configCommand
    .command("set")
    .description("Set a configuration value")
    .argument("<key>", "Configuration key")
    .argument("<value>", "Configuration value")
    .action((key: string, value: string) => {
      try {
        const updatedValue = setConfigValue(key, value);
        console.log(`${chalk.green("updated")} ${key}=${String(updatedValue)}`);
      } catch (error) {
        console.error(formatError(error));
        process.exitCode = 1;
      }
    });

  configCommand
    .command("get")
    .description("Get a configuration value")
    .argument("<key>", "Configuration key")
    .action((key: string) => {
      try {
        console.log(String(getConfigValue(key)));
      } catch (error) {
        console.error(formatError(error));
        process.exitCode = 1;
      }
    });

  configCommand
    .command("list")
    .description("List configured values")
    .action(() => {
      try {
        printConfigList();
      } catch (error) {
        console.error(formatError(error));
        process.exitCode = 1;
      }
    });

  configCommand
    .command("reset")
    .description("Reset configuration to defaults")
    .action(() => {
      try {
        resetConfig();
        console.log(chalk.yellow("configuration reset to defaults"));
        printConfigList();
      } catch (error) {
        console.error(formatError(error));
        process.exitCode = 1;
      }
    });
}

function printConfigList(): void {
  console.log(chalk.bold("pingtrace config"));
  console.log(`Store: ${getConfigStorePath()}`);
  console.log(`Mirror: ${getMirrorConfigPath()}`);

  for (const [key, value] of listConfigEntries(getConfig())) {
    console.log(`${key}=${formatConfigValue(value)}`);
  }
}

async function runInteractiveConfig(): Promise<void> {
  const readline = createInterface({
    input: process.stdin,
    output: process.stdout,
  });

  try {
    while (true) {
      const entries = listConfigEntries(getConfig());
      renderInteractiveScreen(entries);

      for (const [index, [key, value]] of entries.entries()) {
        console.log(`${String(index + 1).padStart(2, " ")}. ${key} = ${formatConfigValue(value)}`);
      }

      console.log(" r. reset configuration");
      console.log(" l. list current values");
      console.log(" q. quit");

      const selection = (await readline.question("\nSelection: ")).trim().toLowerCase();

      if (selection === "q" || selection === "quit" || selection.length === 0) {
        return;
      }

      if (selection === "l" || selection === "list") {
        printConfigList();
        await readline.question("\nPress Enter to continue...");
        continue;
      }

      if (selection === "r" || selection === "reset") {
        const confirmation = (await readline.question("Type 'reset' to confirm: ")).trim().toLowerCase();

        if (confirmation === "reset") {
          resetConfig();
          console.log(chalk.yellow("configuration reset to defaults"));
        } else {
          console.log(chalk.dim("reset cancelled"));
        }

        await readline.question("\nPress Enter to continue...");

        continue;
      }

      const selectedIndex = Number(selection);
      if (!Number.isInteger(selectedIndex) || selectedIndex < 1 || selectedIndex > entries.length) {
        console.log(chalk.red("Invalid selection."));
        continue;
      }

      const selectedEntry = entries[selectedIndex - 1];
      if (!selectedEntry) {
        console.log(chalk.red("Invalid selection."));
        continue;
      }

      const [key, currentValue] = selectedEntry;
      const prompt = createValuePrompt(key, currentValue);
      const input = await readline.question(prompt);
      const nextValue = input.trim();

      if (nextValue.length === 0) {
        console.log(chalk.dim(`${key} unchanged`));
        await readline.question("\nPress Enter to continue...");
        continue;
      }

      try {
        const updatedValue = setConfigValue(key, nextValue);
        console.log(chalk.green(`updated ${key}=${formatConfigValue(updatedValue)}`));
      } catch (error) {
        console.log(chalk.red(formatError(error)));
      }

      await readline.question("\nPress Enter to continue...");
    }
  } finally {
    readline.close();
  }
}

function renderInteractiveScreen(entries: Array<[string, unknown]>): void {
  console.clear();
  console.log(chalk.bold("pingtrace config"));
  console.log(`Store: ${getConfigStorePath()}`);
  console.log(`Mirror: ${getMirrorConfigPath()}`);
  console.log(
    chalk.dim(
      "Select a number to edit a value. Use 'r' to reset everything, 'l' to print the current config, or 'q' to quit.",
    ),
  );
  console.log("");
  console.log(chalk.dim(`Loaded ${entries.length} config entries.`));
  console.log("");
}

function createValuePrompt(key: string, currentValue: unknown): string {
  if (key === "dns.privateServers" || key === "dns.publicServers") {
    return `New value for ${key} [${formatConfigValue(currentValue)}]\nUse one target, comma-separated targets, or CIDR blocks: `;
  }

  if (key === "providers.peeringdbEnabled" || key === "trace.numericOnly") {
    return `New value for ${key} [${formatConfigValue(currentValue)}]\nUse true or false: `;
  }

  return `New value for ${key} [${formatConfigValue(currentValue)}]: `;
}

function formatConfigValue(value: unknown): string {
  if (Array.isArray(value)) {
    return value.length > 0 ? value.join(",") : "(empty)";
  }

  if (value === "") {
    return "(empty)";
  }

  return String(value);
}

function formatError(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }

  return String(error);
}
