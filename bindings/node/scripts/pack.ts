// Builds the npm packages (decision 0069, Packaging) into build/: the main
// package, whose optionalDependencies are one package per platform, and
// those platform packages, each holding its static mimicry-core with `os`
// and `cpu` set so npm installs only the host's. No install scripts, no
// download at install or run time. Nothing is published here.
//
// The platform packages are named <main>-<platform>-<arch> after package.json's
// name (the one place the provisional name is written), which is also how
// src/core.ts finds the binary. Linux has one package per architecture
// for glibc and musl alike: the core is static (CGO_ENABLED=0), so no
// `libc` field narrows it.
//
//   node scripts/pack.ts                 every platform
//   node scripts/pack.ts --host          this platform only (CI's smoke test)
//   node scripts/pack.ts --version 1.2.3 the version to stamp (default package.json's)

import { execFileSync } from "node:child_process";
import { chmodSync, copyFileSync, cpSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const root = join(import.meta.dirname, "..");
const repo = join(root, "..", "..");
const build = join(root, "build");

interface Manifest {
  name: string;
  version: string;
  description: string;
  license: string;
  repository: { type: string; url: string; directory: string };
  engines: Record<string, string>;
  scripts?: Record<string, string>;
  packageManager?: string;
  devDependencies?: Record<string, string>;
  optionalDependencies?: Record<string, string>;
  [k: string]: unknown;
}

const pkg = JSON.parse(readFileSync(join(root, "package.json"), "utf8")) as Manifest;
const args = process.argv.slice(2);
const vi = args.indexOf("--version");
const version = vi >= 0 ? (args[vi + 1] ?? pkg.version) : pkg.version;

// Node's platform and arch, and Go's.
const targets = [
  { platform: "darwin", arch: "arm64", goos: "darwin", goarch: "arm64" },
  { platform: "darwin", arch: "x64", goos: "darwin", goarch: "amd64" },
  { platform: "linux", arch: "arm64", goos: "linux", goarch: "arm64" },
  { platform: "linux", arch: "x64", goos: "linux", goarch: "amd64" },
  { platform: "win32", arch: "arm64", goos: "windows", goarch: "arm64" },
  { platform: "win32", arch: "x64", goos: "windows", goarch: "amd64" },
].filter((t) => !args.includes("--host") || (t.platform === process.platform && t.arch === process.arch));

if (targets.length === 0) {
  throw new Error(`no mimicry-core target for ${process.platform}-${process.arch}`);
}

rmSync(build, { recursive: true, force: true });
const staged = join(build, "staged");
const out = join(build, "packages");
mkdirSync(out, { recursive: true });

function pack(dir: string): void {
  execFileSync("bun", ["pm", "pack", "--destination", out], { cwd: dir, stdio: "inherit" });
}

const optional: Record<string, string> = {};

for (const t of targets) {
  const name = `${pkg.name}-${t.platform}-${t.arch}`;
  const dir = join(staged, name);
  const exe = t.platform === "win32" ? "mimicry-core.exe" : "mimicry-core";
  mkdirSync(join(dir, "bin"), { recursive: true });

  execFileSync("go", ["build", "-trimpath", "-ldflags", `-s -w -X main.version=v${version}`, "-o", join(dir, "bin", exe), "./cmd/mimicry-core"], {
    cwd: repo,
    env: { ...process.env, CGO_ENABLED: "0", GOOS: t.goos, GOARCH: t.goarch },
    stdio: "inherit",
  });
  chmodSync(join(dir, "bin", exe), 0o755);
  copyFileSync(join(repo, "LICENSE"), join(dir, "LICENSE"));
  writeFileSync(
    join(dir, "README.md"),
    `# ${name}\n\nmimicry-core for ${t.platform}-${t.arch}, installed by [${pkg.name}](https://www.npmjs.com/package/${pkg.name}) as an optional dependency. Not for direct use.\n`,
  );
  writeFileSync(
    join(dir, "package.json"),
    JSON.stringify(
      {
        name,
        version,
        description: `mimicry-core for ${pkg.name} on ${t.platform}-${t.arch}`,
        license: pkg.license,
        repository: pkg.repository,
        os: [t.platform],
        cpu: [t.arch],
        files: ["bin"],
        preferUnplugged: true,
      },
      null,
      2,
    ) + "\n",
  );
  pack(dir);
  optional[name] = version;
}

// The main package: the build, its README and LICENSE, and package.json
// without what only development needs.
execFileSync("bun", ["run", "build"], { cwd: root, stdio: "inherit" });
const main = join(staged, pkg.name);
mkdirSync(main, { recursive: true });
cpSync(join(root, "dist"), join(main, "dist"), { recursive: true });
copyFileSync(join(root, "README.md"), join(main, "README.md"));
copyFileSync(join(repo, "LICENSE"), join(main, "LICENSE"));

const manifest: Manifest = { ...pkg, version, optionalDependencies: optional };
delete manifest.scripts;
delete manifest.devDependencies;
delete manifest.packageManager;
writeFileSync(join(main, "package.json"), JSON.stringify(manifest, null, 2) + "\n");
pack(main);

console.log(`packages in ${out}`);
