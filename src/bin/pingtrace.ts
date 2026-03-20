#!/usr/bin/env node

import { createRequire } from "node:module";

import updateNotifier from "update-notifier";

import { createCli } from "../cli.js";

const require = createRequire(import.meta.url);
const pkg = require("../../package.json") as { name: string; version: string };

updateNotifier({ pkg }).notify();

await createCli().parseAsync(process.argv);
