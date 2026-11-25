import assert from "node:assert/strict";
import test from "node:test";
import { codingRules } from "./scope.mjs";

test("coding grants support literal files, recursive directories, and filename globs", () => {
  const cwd = "/projects/Whirlwind (local)";
  const [literal, recursive, glob] = codingRules(
    { allowed_paths: ["README.md", "docs/**", "src/file-?.*"] },
    cwd,
  );
  assert.equal(literal, `(allow file-write* (literal ${JSON.stringify(`${cwd}/README.md`)}))`);
  assert.equal(recursive, `(allow file-write* (subpath ${JSON.stringify(`${cwd}/docs`)}))`);
  const pattern = JSON.parse(glob.match(/\(regex (.*)\)\)$/)[1]);
  assert.match(`${cwd}/src/file-a.go`, new RegExp(pattern));
  assert.doesNotMatch(`${cwd}/src/nested/file-a.go`, new RegExp(pattern));
});

test("read-only scope omits coding grants and whole-worktree scope uses a literal root", () => {
  const cwd = "/projects/Whirlwind";
  assert.deepEqual(codingRules({ read_only: true, allowed_paths: ["**"] }, cwd), []);
  assert.deepEqual(codingRules({ read_only: false, allowed_paths: ["**"] }, cwd), [
    `(allow file-write* (subpath ${JSON.stringify(cwd)}))`,
  ]);
});
