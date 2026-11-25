import { spawnSync } from "node:child_process";

const environment = JSON.parse(process.argv[2]);
const [command, ...args] = process.argv.slice(3);
const result = spawnSync(command, args, {
  stdio: "inherit",
  env: { ...process.env, ...environment },
});
if (result.error) console.error(result.error.message);
process.exitCode = result.error || result.signal ? 1 : result.status === 0 ? 0 : 86;
