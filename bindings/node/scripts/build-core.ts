// Builds mimicry-core for the tests (bun run test runs it first): the command
// at the repository's root, as released (CGO_ENABLED=0), into .core/. The
// tests use it through MIMICRY_CORE unless that is already set.

import { execFileSync } from "node:child_process";
import { join } from "node:path";

const root = join(import.meta.dirname, "..");
const out = join(root, ".core", process.platform === "win32" ? "mimicry-core.exe" : "mimicry-core");

execFileSync("go", ["build", "-o", out, "./cmd/mimicry-core"], {
  cwd: join(root, "..", ".."),
  env: { ...process.env, CGO_ENABLED: "0" },
  stdio: "inherit",
});
