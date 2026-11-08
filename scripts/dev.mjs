#!/usr/bin/env node
import { spawn } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const child = spawn(process.execPath, [resolve(root, "bin/whirlwind"), "dashboard"], { cwd: root, stdio: "inherit" });
child.once("error", error => { console.error(`Whirlwind: ${error.message}`); process.exitCode = 1; });
child.once("close", (code, signal) => { process.exitCode = code ?? (signal ? 1 : 0); });
