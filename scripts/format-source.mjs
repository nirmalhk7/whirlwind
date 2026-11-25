import { execFileSync, spawnSync } from "node:child_process";
import { readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import * as prettier from "prettier";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const args = process.argv.slice(2);
if (args.length > 1 || (args.length === 1 && args[0] !== "--write")) {
  throw new Error("Usage: node scripts/format-source.mjs [--write]");
}
const write = args[0] === "--write";
const deleted = new Set(
  execFileSync("git", ["ls-files", "--deleted", "-z"], {
    cwd: root,
    encoding: "utf8",
  }).split("\0"),
);
const files = [
  ...new Set(
    execFileSync("git", ["ls-files", "--cached", "--others", "--exclude-standard", "-z"], {
      cwd: root,
      encoding: "utf8",
    })
      .split("\0")
      .filter((file) => file && !deleted.has(file)),
  ),
].sort();
let failed = false;
function run(command, argv) {
  const result = spawnSync(command, argv, { cwd: root, encoding: "utf8" });
  if (result.stdout) process.stdout.write(result.stdout);
  if (result.stderr) process.stderr.write(result.stderr);
  if (result.error) console.error(`${command}: ${result.error.message}; see docs/development.md`);
  if (result.error || result.status !== 0 || (!write && result.stdout?.trim())) failed = true;
}
const goFiles = files.filter((file) => file.endsWith(".go"));
for (let offset = 0; offset < goFiles.length; offset += 100) {
  run(process.env.GOIMPORTS || resolve(root, ".tools/bin/goimports"), [
    "-local",
    "whirlwind",
    write ? "-w" : "-l",
    ...goFiles.slice(offset, offset + 100),
  ]);
}
const pythonFiles = files.filter((file) => file.endsWith(".py"));
if (pythonFiles.length) {
  // Black's safe mode verifies equivalent Python ASTs when writing.
  const result = spawnSync(
    process.env.PYTHON || "python3",
    [
      "-m",
      "black",
      "--required-version",
      "25.1.0",
      "--line-length",
      "100",
      ...(write ? [] : ["--check"]),
      ...pythonFiles,
    ],
    { cwd: root, stdio: "inherit" },
  );
  if (result.error) console.error(result.error.message);
  if (result.error || result.status !== 0) failed = true;
}
for (const file of files.filter(
  (file) =>
    file.endsWith(".mjs") ||
    file === "bin/whirlwind" ||
    [
      "package.json",
      ".prettierrc.json",
      "bridges/claude/package.json",
      "bridges/sandbox/package.json",
    ].includes(file),
)) {
  const absolute = resolve(root, file);
  const source = await readFile(absolute, "utf8");
  const options = await prettier.resolveConfig(absolute);
  const formatted = await prettier.format(source, {
    ...options,
    filepath: absolute,
    ...(file === "bin/whirlwind" ? { parser: "babel" } : {}),
  });
  if (source !== formatted) {
    if (write) await writeFile(absolute, formatted);
    else {
      console.error(file);
      failed = true;
    }
  }
}
if (failed) {
  console.error(
    "Source checks failed. Install development tools and run make format; see docs/development.md.",
  );
  process.exitCode = 1;
}
