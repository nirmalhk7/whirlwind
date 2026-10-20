import { mkdir, open, readFile, rm, stat } from "node:fs/promises";
import { setTimeout as delay } from "node:timers/promises";

function alive(pid) {
  try { process.kill(pid, 0); return true; }
  catch (error) { if (error.code === "ESRCH") return false; throw error; }
}

async function clearStaleLock(path) {
  try {
    const text = await readFile(path, "utf8");
    // Allow the creator to finish writing before treating an empty lock as stale.
    const owner = text ? JSON.parse(text).pid : null;
    if ((Number.isInteger(owner) && owner > 1 && !alive(owner)) || (!owner && Date.now() - (await stat(path)).mtimeMs > 2000)) {
      await rm(path, { force: true });
      return true;
    }
  } catch (error) { if (error.code !== "ENOENT") throw error; }
  return false;
}

async function acquireLock(path, deadline) {
  while (true) {
    try {
      const lock = await open(path, "wx", 0o600);
      try { await lock.writeFile(JSON.stringify({ pid: process.pid })); }
      catch (error) { await lock.close(); await rm(path, { force: true }); throw error; }
      return lock;
    } catch (error) {
      if (error.code !== "EEXIST") throw error;
      if (await clearStaleLock(path)) continue;
      if (Date.now() >= deadline) throw new Error(`Another whirlwind command is busy (${path}).`);
      await delay(100);
    }
  }
}

export async function withLock(config, action) {
  await mkdir(config.directory, { recursive: true, mode: 0o700 });
  const path = `${config.directory}/command.lock`;
  const lock = await acquireLock(path, Date.now() + 360_000);
  try { return await action(); }
  finally { await lock.close(); await rm(path, { force: true }); }
}
