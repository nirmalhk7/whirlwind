import { access, readFile, realpath, rename, rm, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { execFile } from "node:child_process";

const exec = promisify(execFile);

export async function configuration() {
  const root = await realpath(process.env.WHIRLWIND_ROOT || dirname(dirname(fileURLToPath(import.meta.url))));
  const data = resolve(root, process.env.WHIRLWIND_DATA_DIR || join(homedir(), ".local/share/whirlwind"));
  const listen = process.env.WHIRLWIND_LISTEN || "127.0.0.1:9877";
  const match = /^(127\.0\.0\.1|localhost|\[::1\]):(\d+)$/.exec(listen);
  const webPort = process.env.WHIRLWIND_WEB_PORT || "9876";
  const validPort = (value) => /^\d+$/.test(value) && Number(value) > 0 && Number(value) <= 65535;
  if (!match || !validPort(match[2])) throw new Error("WHIRLWIND_LISTEN must be a loopback host:port (1-65535).");
  if (!validPort(webPort)) throw new Error("WHIRLWIND_WEB_PORT must be a port (1-65535).");
  if (Number(match[2]) === Number(webPort)) throw new Error("Backend and frontend ports must differ.");
  const directory = join(data, "launcher");
  return {
    root, data, listen, webPort, backendURL: `http://${listen}`, webURL: `http://127.0.0.1:${webPort}`,
    host: match[1].replace(/[\[\]]/g, ""), backendPort: Number(match[2]),
    directory, stateFile: join(directory, "state.json"), log: join(directory, "whirlwind.log"),
    env: { ...process.env, WHIRLWIND_DATA_DIR: data,
      WHIRLWIND_SETTINGS_FILE: resolve(root, process.env.WHIRLWIND_SETTINGS_FILE || "settings.yaml"),
      WHIRLWIND_LISTEN: listen, WHIRLWIND_WEB_PORT: webPort,
      WHIRLWIND_BACKEND_URL: `http://${listen}` },
  };
}

export async function exists(path) {
  try { await access(path); return true; }
  catch (error) { if (error.code === "ENOENT") return false; throw error; }
}

export async function readState(config) {
  try {
    const state = JSON.parse(await readFile(config.stateFile, "utf8"));
    if (state.version !== 1 || !/^[a-f0-9]{32}$/.test(state.token) || !Number.isInteger(state.pid) || state.pid < 0) {
      throw new Error("invalid process registration");
    }
    return state;
  } catch (error) {
    if (error.code === "ENOENT") return null;
    throw new Error(`Cannot read ${config.stateFile}: ${error.message}`);
  }
}

export async function saveState(config, state) {
  const temporary = `${config.stateFile}.${process.pid}`;
  try {
    await writeFile(temporary, JSON.stringify(state, null, 2) + "\n", { mode: 0o600 });
    await rename(temporary, config.stateFile);
  } finally { await rm(temporary, { force: true }); }
}
