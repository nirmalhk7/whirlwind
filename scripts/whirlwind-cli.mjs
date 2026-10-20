import { configuration, readState } from "./launcher-config.mjs";
import { doctor, start } from "./launcher-commands.mjs";
import { report, stop } from "./launcher-processes.mjs";
import { withLock } from "./launcher-lock.mjs";

const help = `Usage: whirlwind <command>

  start    Build backend and production UI; start both in background
  status   Show process health, URLs, checkout, and log path
  stop     Stop managed backend, frontend, and descendants
  restart  Stop, rebuild, and start both services
  doctor   Check runtime, dependencies, ports, and service health

Add this checkout's bin directory to PATH, or symlink bin/whirlwind.
Defaults: UI http://127.0.0.1:9876, backend http://127.0.0.1:9877.
Overrides: WHIRLWIND_ROOT, WHIRLWIND_DATA_DIR, WHIRLWIND_LISTEN,
           WHIRLWIND_WEB_PORT, WHIRLWIND_SETTINGS_FILE.
`;

export async function runCLI(args) {
  const helpRequested = !args.length || (args.length === 1 && ["--help", "-h", "help"].includes(args[0]));
  if (helpRequested) console.log(help);
  const valid = args.length === 1 && ["start", "status", "stop", "restart", "doctor"].includes(args[0]);
  if (!helpRequested && !valid) console.error(help);
  if (helpRequested || !valid) return helpRequested ? 0 : 1;
  if (!["darwin", "linux"].includes(process.platform)) throw new Error("Whirlwind launcher supports macOS and Linux.");
  const config = await configuration();
  const commands = {
    status: async () => report(config, await readState(config)),
    doctor: () => doctor(config),
    start: () => withLock(config, () => start(config)),
    stop: () => withLock(config, () => stop(config)),
    restart: () => withLock(config, async () => { await stop(config, true); return start(config); }),
  };
  return commands[args[0]]();
}
