// Windows only: the core's stdio are libuv's named pipes
// (\\?\pipe\uv\<pointer>-<parent pid>, one instance each,
// FILE_FLAG_FIRST_PIPE_INSTANCE; src/win/pipe.c), whose client end the
// parent opens at once. No other process may reach the core through
// them (decision 0069): one that tries after the spawn is refused, and
// one racing the spawn either is refused or makes the spawn fail, never
// gets a core of ours.

import assert from "node:assert/strict";
import { type ChildProcess, spawn } from "node:child_process";
import { openSync, closeSync, readdirSync } from "node:fs";
import { test } from "node:test";

import { Core } from "../src/core.ts";
import { newClient } from "../src/index.ts";
import { profile, startServer } from "./helpers.ts";

const windows = process.platform === "win32";

// The parent's libuv pipes now: uv\<pointer>-<pid>.
function uvPipes(): Set<string> {
  const suffix = `-${process.pid}`;

  return new Set(readdirSync("\\\\.\\pipe\\").filter((n) => n.startsWith("uv\\") && n.endsWith(suffix)));
}

test("a core's stdio pipes refuse another client", { skip: !windows }, async () => {
  const before = uvPipes();
  const core = await Core.start();
  try {
    const pipes = [...uvPipes()].filter((n) => !before.has(n));
    assert.ok(pipes.length >= 2, `the core's pipes: ${pipes.join(", ")}`);

    for (const name of pipes) {
      for (const flags of ["r+", "r", "w"]) {
        assert.throws(
          () => {
            closeSync(openSync(`\\\\.\\pipe\\${name}`, flags));
          },
          (e: NodeJS.ErrnoException) => e.code === "EBUSY" || e.code === "EPERM" || e.code === "EACCES",
          `${name} (${flags}) opened`,
        );
      }
    }

    // And the core still answers its own parent.
    const r = await core.call({ case: "listProfiles", value: {} });
    assert.equal(r.result.case, "profiles");
  } finally {
    core.stop();
  }
});

// The racer: another process opening every pipe of ours it can see, as
// fast as it can. It prints how many it opened.
const racer = `
const fs = require("node:fs");
const suffix = "-" + process.argv[1];
let opened = 0;
let stop = false;
process.stdin.on("data", () => { stop = true; });
(function loop() {
  if (stop) { console.log(opened); process.exit(0); }
  for (const n of fs.readdirSync("\\\\\\\\.\\\\pipe\\\\")) {
    if (!n.startsWith("uv\\\\") || !n.endsWith(suffix)) continue;
    try { fs.closeSync(fs.openSync("\\\\\\\\.\\\\pipe\\\\" + n, "r+")); opened++; } catch {}
  }
  setImmediate(loop);
})();
`;

test("a client racing the spawn never gets a core's pipe", { skip: !windows }, async () => {
  const server = await startServer((_req, res) => {
    res.end("ok");
  });
  const r: ChildProcess = spawn(process.execPath, ["-e", racer, String(process.pid)], { stdio: ["pipe", "pipe", "inherit"] });
  let out = "";
  r.stdout?.on("data", (b: Buffer) => {
    out += b.toString();
  });

  let started = 0;
  let refused = 0;
  try {
    for (let i = 0; i < 30; i++) {
      let core: Core;
      try {
        core = await Core.start();
      } catch {
        // The racer took a pipe first: the spawn failed, closed.
        refused++;

        continue;
      }

      started++;
      try {
        const r = await core.call({ case: "listProfiles", value: {} });
        assert.equal(r.result.case, "profiles");
      } finally {
        core.stop();
      }
    }

    // The SDK's own default core works with the racer still running.
    const c = await newClient({ profile });
    await (await c.do({ url: `${server.plain}/` })).text();
    await c.close();
  } finally {
    const exited = Promise.withResolvers<void>();
    r.once("exit", () => {
      exited.resolve();
    });
    r.stdin?.write("stop");
    await exited.promise;
    await server.close();
  }

  const opened = Number(out.trim());
  console.log(`spawns: ${started} started, ${refused} refused; racer opened ${opened} pipes`);
  // Each pipe the racer opened made its spawn fail; none was ours to use.
  assert.ok(opened <= refused, `the racer opened ${opened} pipes but only ${refused} spawns failed`);
  assert.ok(started > 0);
});
