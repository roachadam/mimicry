// Generates src/gen, the schema's code (proto/, buf.gen.yaml), with buf at
// the version the repository pins (moon.yml's proto task) and protoc-gen-es
// at package.json's. With --check it generates into a temporary directory
// instead and fails while that differs from the checked-in src/gen.
//
//   node scripts/gen.ts            regenerate src/gen
//   node scripts/gen.ts --check    fail if src/gen is stale

import { execFileSync } from "node:child_process";
import { mkdtempSync, readdirSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";

const buf = "github.com/bufbuild/buf/cmd/buf@v1.75.0";
const root = join(import.meta.dirname, "..");
const check = process.argv.includes("--check");

function files(dir: string): string[] {
  return readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((e) => e.isFile())
    .map((e) => relative(dir, join(e.parentPath, e.name)))
    .sort();
}

const out = check ? mkdtempSync(join(tmpdir(), "mimicry-gen-")) : root;
if (!check) {
  rmSync(join(root, "src/gen"), { recursive: true, force: true });
}

try {
  execFileSync("go", ["run", buf, "generate", "--template", "buf.gen.yaml", "-o", out], {
    cwd: root,
    stdio: "inherit",
  });

  if (check) {
    const want = join(out, "src/gen");
    const got = join(root, "src/gen");
    const wantFiles = files(want);
    const stale = wantFiles.join("\n") !== files(got).join("\n") ||
      wantFiles.some((f) => !readFileSync(join(want, f)).equals(readFileSync(join(got, f))));
    if (stale) {
      console.error("src/gen differs from what proto/ generates: run bun run gen");
      process.exitCode = 1;
    }
  }
} finally {
  if (check) {
    rmSync(out, { recursive: true, force: true });
  }
}
