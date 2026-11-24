import {spawnSync} from 'node:child_process';
import {mkdtemp, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import assert from 'node:assert/strict';
import {test} from 'node:test';

const launch = fileURLToPath(new URL('./launch.mjs', import.meta.url));

for (const event of ['before', 'after']) {
  test(`native ${event} hook failure overrides a successful sandbox sentinel`, async () => {
    const cwd = await mkdtemp(join(tmpdir(), 'ww-hook-test-'));
    try {
      await writeFile(join(cwd, 'required.txt'), 'wrong');
      const profile = {
        cwd, read_only: true, read_roots: [],
        lifecycle_hook: {event, checks: [
          {name: 'required-content', command: ['/bin/sh', '-c', 'grep -qx expected required.txt || { echo required-content-failed; exit 1; }'], timeout_seconds: 10},
        ]},
      };
      const run = () => spawnSync(process.execPath, [launch, JSON.stringify(profile), '--', '/usr/bin/true'], {encoding: 'utf8', timeout: 20000});
      const failure = run();
      assert.equal(failure.status, 86, failure.stderr);
      assert.match(failure.stdout, /required-content-failed/);
      await writeFile(join(cwd, 'required.txt'), 'expected');
      const pass = run();
      assert.equal(pass.status, 0, pass.stderr);
    } finally {
      await rm(cwd, {recursive: true, force: true});
    }
  });
}

test('coding cannot write or replace the authoritative settings file', async () => {
  const cwd = await mkdtemp(join(tmpdir(), 'ww-hook-settings-'));
  try {
    const {mkdir, readFile} = await import('node:fs/promises');
    await mkdir(join(cwd, 'config'));
    const settings = join(cwd, 'config', 'settings.yaml');
    await writeFile(settings, 'trusted');
    const profile = {cwd, read_only: false, allowed_paths: ['**'], protected_files: [settings]};
    const run = spawnSync(process.execPath, [launch, JSON.stringify(profile), '--', process.execPath, '-e', `
      const fs = require('node:fs');
      fs.writeFileSync('ordinary.txt', 'ok');
      for (const attempt of [
        () => fs.writeFileSync('config/settings.yaml', 'modified'),
        () => fs.renameSync('config', 'renamed'),
      ]) {
        try { attempt(); process.exitCode = 32; break; }
        catch (error) { if (!['EPERM', 'EACCES'].includes(error.code)) throw error; }
      }
    `], {encoding: 'utf8', timeout: 20000});
    assert.equal(run.status, 0, run.stderr);
    assert.equal(await readFile(settings, 'utf8'), 'trusted');
  } finally {
    await rm(cwd, {recursive: true, force: true});
  }
});
