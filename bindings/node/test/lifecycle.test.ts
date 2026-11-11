// The core's lifetime as a Node program sees it, in child Node processes:
// an idle core keeps nothing alive and exits with its parent, and objects
// nobody holds are freed in the core.

import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { join } from "node:path";
import { test } from "node:test";
import { promisify } from "node:util";

import { startServer } from "./helpers.ts";

const run = promisify(execFile);
const fixtures = join(import.meta.dirname, "fixtures");

function alive(pid: number): boolean {
  try {
    process.kill(pid, 0);

    return true;
  } catch {
    return false;
  }
}

test("Node exits with the SDK's objects open, and the core exits with it", async () => {
  const server = await startServer((_req, res) => {
    res.end("ok");
  });
  try {
    // A hang here (the core keeping Node alive) fails on the timeout.
    const { stdout } = await run(process.execPath, [join(fixtures, "idle.ts"), `${server.plain}/`], { timeout: 20_000 });
    const pid = Number(stdout.trim());
    assert.ok(pid > 0, stdout);

    // The core sees EOF when its parent exits; give it a moment to go.
    const deadline = Date.now() + 10_000;
    while (alive(pid) && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }

    assert.ok(!alive(pid), `mimicry-core ${pid} outlived its parent`);
  } finally {
    await server.close();
  }
});

test("objects garbage collected are freed in the core", async () => {
  const { stdout } = await run(process.execPath, ["--expose-gc", join(fixtures, "finalize.ts")], { timeout: 30_000 });
  assert.equal(Number(stdout.trim()), 20);
});
