import { once } from "node:events";
import { open } from "node:fs/promises";
import { join, relative } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { localConfiguration } from "./local-supervisor.mjs";

async function write(chunk) {
  if (!process.stdout.write(chunk)) await once(process.stdout, "drain");
}

async function readLog(path, state, render) {
  let file;
  try { file = await open(path, "r"); }
  catch (error) { if (error.code === "ENOENT") return false; throw error; }
  try {
    const stat = await file.stat();
    if (!stat.isFile()) return false;
    const identity = `${stat.dev}:${stat.ino}`;
    let offset = state.identity === identity && stat.size >= state.offset ? state.offset : 0;
    const buffer = Buffer.alloc(64 * 1024);
    while (offset < stat.size) {
      const { bytesRead } = await file.read(buffer, 0, Math.min(buffer.length, stat.size - offset), offset);
      if (!bytesRead) break;
      await render(path, buffer.subarray(0, bytesRead));
      offset += bytesRead;
    }
    state.identity = identity;
    state.offset = offset;
    return true;
  } finally { await file.close(); }
}

export async function logs({ follow = false } = {}) {
  const config = await localConfiguration();
  const paths = [config.log, join(config.data, "launcher", "whirlwind.log")];
  const positions = paths.map(() => ({ offset: 0 }));
  const controller = new AbortController();
  const stop = () => controller.abort();
  let lastPath = "", found = false;
  const render = async (path, chunk) => {
    if (lastPath !== path) {
      await write(`\n==> ${relative(config.data, path)} <==\n`);
      lastPath = path;
    }
    await write(chunk);
  };
  if (follow) {
    process.on("SIGINT", stop);
    process.on("SIGTERM", stop);
  }
  try {
    do {
      for (const [index, path] of paths.entries()) found = await readLog(path, positions[index], render) || found;
      if (!follow || controller.signal.aborted) break;
      try { await delay(200, undefined, { signal: controller.signal }); }
      catch (error) { if (error.name !== "AbortError") throw error; }
    } while (!controller.signal.aborted);
    if (!follow && !found) console.error(`No Whirlwind logs found in ${config.data}.`);
    return 0;
  } finally {
    process.off("SIGINT", stop);
    process.off("SIGTERM", stop);
  }
}
