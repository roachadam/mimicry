// The wire protocol's vectors (proto/testdata/wire/), replayed against
// src/wire.ts as Go's TestVectors replays them against internal/rpc/wire:
// each vector's endpoint, of its role, sends exactly the vector's frames
// and fails with the named error, never a hang.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { PassThrough } from "node:stream";
import { test } from "node:test";

import { Conn, type Stream, WireError, codeName } from "../src/wire.ts";

interface VectorFrame {
  dir: "in" | "out";
  size: number;
  stream?: number;
  type?: string;
  end?: boolean;
  payload?: string;
  auto?: boolean;
  note?: string;
  expect?: "ignore" | "stream-error" | "conn-error";
  error?: string;
  remote?: boolean;
}

interface Vector {
  name: string;
  role: "sdk" | "core";
  file: string;
  frames: VectorFrame[];
}

interface Manifest {
  version: number;
  window: number;
  maxStreams: number;
  vectors: Vector[];
}

const dir = new URL("../../../proto/testdata/wire/", import.meta.url);
const manifest = JSON.parse(readFileSync(new URL("manifest.json", dir), "utf8")) as Manifest;

// errName is the manifest's name for err, and whether the peer sent it.
function errName(err: unknown): [string, boolean] {
  if (!(err instanceof WireError)) {
    return ["", false];
  }

  if (err.code !== undefined) {
    return [codeName(err.code), err.remote];
  }

  if (err.truncated) {
    return ["TRUNCATED", false];
  }

  return [err.conn ? "CONN_CLOSED" : "", false];
}

// deadline rejects once the vector's time is up.
function within<T>(p: Promise<T>, signal: AbortSignal, what: string): Promise<T> {
  const { promise, reject } = Promise.withResolvers<T>();
  const onAbort = (): void => {
    reject(new Error(`timed out ${what}`));
  };
  signal.addEventListener("abort", onAbort, { once: true });

  return Promise.race([p, promise]).finally(() => {
    signal.removeEventListener("abort", onAbort);
  });
}

// OutBuf collects what the endpoint writes.
class OutBuf {
  private buf = Buffer.alloc(0);
  private off = 0;
  private eof = false;
  private wake: (() => void) | null = null;

  constructor(out: PassThrough) {
    out.on("data", (chunk: Buffer) => {
      this.buf = Buffer.concat([this.buf, chunk]);
      this.wake?.();
    });
    out.on("end", () => {
      this.eof = true;
      this.wake?.();
    });
  }

  private async more(signal: AbortSignal, what: string): Promise<void> {
    const { promise, resolve } = Promise.withResolvers<void>();
    this.wake = resolve;
    await within(promise, signal, what);
    this.wake = null;
  }

  // next is the next n bytes written.
  async next(n: number, signal: AbortSignal): Promise<Buffer> {
    for (;;) {
      if (this.buf.length >= this.off + n) {
        const b = this.buf.subarray(this.off, this.off + n);
        this.off += n;

        return b;
      }

      if (this.eof) {
        throw new Error(`the endpoint closed the connection before sending ${n} more bytes`);
      }

      await this.more(signal, `waiting for ${n} bytes from the endpoint`);
    }
  }

  // rest waits for the end and returns what came after the last next.
  async rest(signal: AbortSignal): Promise<Buffer> {
    while (!this.eof) {
      await this.more(signal, "waiting for the endpoint to close the connection");
    }

    return this.buf.subarray(this.off);
  }
}

async function readFull(s: Stream, n: number): Promise<Buffer> {
  const parts: Uint8Array[] = [];
  let got = 0;
  while (got < n) {
    const b = await s.read(n - got);
    if (b === null) {
      throw new Error(`EOF after ${got} of ${n} bytes`);
    }

    parts.push(b);
    got += b.length;
  }

  return Buffer.concat(parts);
}

async function replay(v: Vector, data: Buffer, signal: AbortSignal): Promise<void> {
  const input = new PassThrough();
  const output = new PassThrough();
  const notices: Uint8Array[] = [];
  let noticeWake: (() => void) | null = null;
  const c = new Conn(input, output, {
    role: v.role,
    window: manifest.window,
    maxStreams: manifest.maxStreams,
    onEvent: (p) => {
      notices.push(p);
      noticeWake?.();
    },
  });
  const o = new OutBuf(output);
  const streams = new Map<number, Stream>();

  const stream = (f: VectorFrame): Stream => {
    const s = streams.get(f.stream ?? -1);
    if (s === undefined) {
      throw new Error(`no stream ${f.stream}`);
    }

    return s;
  };

  signal.addEventListener("abort", () => {
    c.close();
    input.end();
  });

  let wantErr = "CONN_CLOSED";
  let wantRemote = false;
  let off = 0;

  for (const [i, f] of v.frames.entries()) {
    const b = data.subarray(off, off + f.size);
    off += f.size;
    const payload = Buffer.from(f.payload ?? "", "hex");
    const where = `frame ${i} (${f.dir} ${f.type ?? f.note})`;

    if (f.dir === "out") {
      if (f.auto !== true) {
        if ((f.type === "CALL" || f.type === "EVENT") && f.stream !== 0) {
          const s = await within(c.open(payload, f.end === true), signal, where);
          assert.equal(s.id, f.stream, `${where}: opened stream ${s.id}`);
          streams.set(s.id, s);
        } else if (f.type === "EVENT") {
          await within(c.notify(payload), signal, where);
        } else {
          const s = stream(f);
          switch (f.type) {
            case "REPLY":
              await within(s.writeReply(payload, f.end === true), signal, where);
              break;
            case "DATA":
              if (payload.length > 0) {
                await within(s.write(payload), signal, where);
              }

              if (f.end === true) {
                await within(s.closeWrite(), signal, where);
              }

              break;
            case "RESET":
              s.reset(payload.readUInt32BE(0));
              break;
            default:
              throw new Error(`${where}: no action sends ${f.type}`);
          }
        }
      }

      const got = await o.next(b.length, signal);
      assert.equal(got.toString("hex"), b.toString("hex"), `${where}: the endpoint sent another frame`);

      continue;
    }

    input.write(b);

    if (f.expect === "conn-error") {
      wantErr = f.error ?? "";
      wantRemote = f.remote === true;
      input.end();

      continue;
    }

    // Let the endpoint take the frame in.
    await new Promise<void>((resolve) => {
      setImmediate(resolve);
    });

    switch (f.expect) {
      case "ignore":
        continue;
      case "stream-error": {
        const err = await within(stream(f).read(1).then(() => null, (e: unknown) => e), signal, where);
        const [name, remote] = errName(err);
        assert.ok(name === f.error && !remote, `${where}: the stream failed with ${String(err)}, want ${f.error}`);

        continue;
      }
      case undefined:
        break;
    }

    switch (f.type) {
      case "CALL":
      case "EVENT":
        if (f.stream === 0) {
          while (notices.length === 0) {
            const { promise, resolve } = Promise.withResolvers<void>();
            noticeWake = resolve;
            await within(promise, signal, `${where}: no notice`);
          }

          assert.equal(Buffer.from(notices.shift() as Uint8Array).toString("hex"), payload.toString("hex"));
        } else {
          const s = await within(c.accept(), signal, where);
          assert.equal(s.id, f.stream);
          assert.equal(Buffer.from(s.payload).toString("hex"), payload.toString("hex"));
          streams.set(s.id, s);
        }

        break;

      case "REPLY": {
        const got = await within(stream(f).reply(), signal, where);
        assert.equal(Buffer.from(got).toString("hex"), payload.toString("hex"));

        break;
      }

      case "DATA": {
        const got = await within(readFull(stream(f), payload.length), signal, where);
        assert.equal(got.toString("hex"), payload.toString("hex"));

        break;
      }

      case "RESET": {
        const err = await within(stream(f).read(1).then(() => null, (e: unknown) => e), signal, where);
        assert.ok(err instanceof WireError && err.remote && err.code === payload.readUInt32BE(0), `${where}: the stream failed with ${String(err)}, want the peer's RESET`);

        continue;
      }

      case "WINDOW":
        continue;
    }

    if (f.end === true) {
      const got = await within(stream(f).read(1), signal, where);
      assert.equal(got, null, `${where}: after END, read returned data`);
    }
  }

  input.end();

  await within(c.done, signal, "the connection did not end after the input did");

  const [name, remote] = errName(c.err);
  assert.ok(name === wantErr && remote === wantRemote, `the connection failed with ${String(c.err)} (${name}, remote ${remote}), want ${wantErr} (remote ${wantRemote})`);

  const rest = await o.rest(signal);
  assert.equal(rest.toString("hex"), "", "the endpoint sent bytes after the last frame");
}

for (const v of manifest.vectors) {
  test(`vector ${v.name}`, async () => {
    const data = readFileSync(new URL(v.file, dir));
    await replay(v, data, AbortSignal.timeout(10_000));
  });
}

