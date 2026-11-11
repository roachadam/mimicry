// The core: mimicry-core started as this process's child (decision 0069),
// spoken to over its stdio with the wire protocol. Node's 'pipe' stdio is
// a socketpair each way on Linux and macOS and a named pipe each way on
// Windows; the core reads stdin and writes stdout. One core serves the
// whole process, started at first use and again after one dies. It is
// unref'd while no call waits on it, so it never keeps Node alive, and it
// exits when this process's end of its stdin closes.

import { type ChildProcess, spawn } from "node:child_process";
import { createRequire } from "node:module";
import { dirname, isAbsolute, join } from "node:path";

import { type MessageInitShape, create, fromBinary, toBinary } from "@bufbuild/protobuf";

import { CanceledError, CoreExited, ProtocolError, errorFromPb } from "./errors.ts";
import {
  CallSchema,
  Code as PbCode,
  type ConnectionState as PbConnectionState,
  type Error as PbError,
  ErrorSchema,
  EventReplySchema,
  EventSchema,
  type Reply,
  ReplySchema,
  type SetCookie,
} from "./gen/mimicry/v1/mimicry_pb.js";
import { Code, Conn, type Stream, WireError, codeName, maxPayload } from "./wire.ts";

const require = createRequire(import.meta.url);
const pkg = require("../package.json") as { name: string; version: string };

/** The protocol version this SDK speaks (proto/README.md, Hello). */
const protocolMajor = 1;
const protocolMinor = 0;

/** How much of the core's stderr a CoreExited keeps. */
const stderrKeep = 4096;
/** How long a broken connection waits for the core's exit status. */
const exitGrace = 2000;

/**
 * The core binary: MIMICRY_CORE when set (an absolute path, for
 * development: `go build ./cmd/mimicry-core`), else the one in this
 * platform's package (`<name>-<platform>-<arch>`, an optional dependency).
 * Never looked up in PATH.
 */
export function corePath(): string {
  const env = process.env["MIMICRY_CORE"];
  if (env !== undefined && env !== "") {
    if (!isAbsolute(env)) {
      throw new Error(`mimicry: MIMICRY_CORE must be an absolute path, not ${JSON.stringify(env)}`);
    }

    return env;
  }

  const platformPkg = `${pkg.name}-${process.platform}-${process.arch}`;
  let dir: string;
  try {
    dir = dirname(require.resolve(`${platformPkg}/package.json`));
  } catch {
    throw new Error(
      `mimicry: no mimicry-core for ${process.platform}-${process.arch}: ${platformPkg}, an optional dependency of ${pkg.name}, is not installed`,
    );
  }

  return join(dir, "bin", process.platform === "win32" ? "mimicry-core.exe" : "mimicry-core");
}

/** A call's method and its arguments, as Call's oneof takes them. */
export type Method = NonNullable<MessageInitShape<typeof CallSchema>["method"]>;

/** A WebSocket's side of the core's notices. */
export interface SocketSink {
  written(seq: bigint, error: PbError | undefined): void;
}

type Handle = { ref(): void; unref(): void };

/** One core process and the connection to it. */
export class Core {
  readonly child: ChildProcess;
  readonly conn: Conn;
  coreVersion = "";

  /** StreamError notices by stream, until their call reads them. */
  readonly streamErrors = new Map<number, PbError>();
  /** Open WebSockets by id, for their WebSocketWritten notices. */
  readonly sockets = new Map<bigint, SocketSink>();
  /** Jars made with onSetCookie, by id. */
  readonly jars = new Map<bigint, (e: SetCookie) => void>();
  /** Clients and transports made with verifyConnection, by id. */
  readonly verifiers = new Map<bigint, (state: PbConnectionState) => unknown>();

  private stderrTail = "";
  private active = 0;
  private dead: Promise<Error> | null = null;
  private readonly exited: Promise<CoreExited>;

  private constructor(child: ChildProcess) {
    this.child = child;

    const exited = Promise.withResolvers<CoreExited>();
    this.exited = exited.promise;
    child.on("close", (code, signal) => {
      exited.resolve(new CoreExited(code, signal, this.stderrTail));
    });
    child.stderr?.setEncoding("utf8");
    child.stderr?.on("data", (s: string) => {
      this.stderrTail = (this.stderrTail + s).slice(-stderrKeep);
    });

    if (child.stdout === null || child.stdin === null) {
      throw new Error("mimicry: mimicry-core started without its stdio");
    }

    this.conn = new Conn(child.stdout, child.stdin, {
      role: "sdk",
      onEvent: (p) => {
        this.onEvent(p);
      },
    });
    void this.conn.done.then(() => this.failure());
    void this.acceptLoop();
    this.refs(false);
  }

  /** Starts a core and says Hello. */
  static async start(path = corePath()): Promise<Core> {
    const child = spawn(path, [], {
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    });

    const spawned = Promise.withResolvers<void>();
    child.once("spawn", spawned.resolve);
    child.once("error", spawned.reject);
    try {
      await spawned.promise;
    } catch (e) {
      throw new Error(`mimicry: starting mimicry-core (${path}): ${(e as Error).message}`, { cause: e });
    } finally {
      child.off("error", spawned.reject);
    }

    // A failure later (a kill that failed) is the connection's to report.
    child.on("error", () => undefined);

    const core = new Core(child);
    try {
      const r = await core.call({
        case: "hello",
        value: {
          protocolMajor,
          protocolMinor,
          sdkVersion: `node ${pkg.version}`,
        },
      });
      if (r.result.case !== "hello" || r.result.value.protocolMajor !== protocolMajor) {
        throw new ProtocolError(`mimicry: mimicry-core speaks another protocol than ${protocolMajor}.${protocolMinor}`);
      }

      core.coreVersion = r.result.value.coreVersion;
    } catch (e) {
      core.stop();

      throw e;
    }

    return core;
  }

  /** Whether calls still reach the core. */
  isAlive(): boolean {
    return this.dead === null && this.conn.err === null;
  }

  /** Stops the core: its connection closes and it exits. */
  stop(): void {
    this.conn.close();
  }

  private refs(on: boolean): void {
    const handles = [this.child, this.child.stdin, this.child.stdout, this.child.stderr] as unknown as (Handle | null)[];
    for (const h of handles) {
      if (on) {
        h?.ref();
      } else {
        h?.unref();
      }
    }
  }

  /** Keeps the process alive while p waits on the core. */
  async track<T>(p: Promise<T>): Promise<T> {
    if (this.active++ === 0) {
      this.refs(true);
    }

    try {
      return await p;
    } finally {
      if (--this.active === 0) {
        this.refs(false);
      }
    }
  }

  /**
   * The error every call gets once the connection is gone: CoreExited
   * when the core ended it (with its status and stderr), ProtocolError
   * when either side broke the protocol (the core is then stopped).
   */
  failure(): Promise<Error> {
    this.dead ??= this.diagnose();

    return this.dead;
  }

  private async diagnose(): Promise<Error> {
    await this.conn.done;
    const err = this.conn.err;

    if (err !== null && err.code !== undefined) {
      this.child.kill();

      return new ProtocolError(`mimicry: the connection to mimicry-core failed: ${err.message}`, { cause: err });
    }

    const timer = Promise.withResolvers<null>();
    const t = setTimeout(timer.resolve, exitGrace, null);
    t.unref();
    const exited = await Promise.race([this.exited, timer.promise]);
    clearTimeout(t);
    if (exited !== null) {
      return exited;
    }

    this.child.kill();

    return new ProtocolError(`mimicry: the connection to mimicry-core ended and it did not exit: ${err?.message ?? "closed"}`, {
      cause: err,
    });
  }

  /** The error a failed wire operation on stream id means to the caller. */
  async mapError(e: unknown, id: number | null, signal?: AbortSignal): Promise<unknown> {
    if (!(e instanceof WireError)) {
      return e;
    }

    if (e.conn) {
      return this.failure();
    }

    if (!e.remote && e.code === Code.Cancel) {
      // This side reset it: the caller's abort, or a close with a reason.
      if (signal?.aborted === true) {
        return signal.reason;
      }

      return e.cause ?? new CanceledError("mimicry: the call was closed", "CANCELED");
    }

    if (id !== null) {
      const se = this.streamErrors.get(id);
      if (se !== undefined) {
        this.streamErrors.delete(id);

        return errorFromPb(se);
      }
    }

    if (e.remote) {
      return new CanceledError(`mimicry: mimicry-core reset the call (${codeName(e.code ?? 0)})`, "CANCELED");
    }

    return new ProtocolError(`mimicry: ${e.message}`, { cause: e });
  }

  /** Opens a call; `body` says DATA follows it (ended with closeWrite). */
  async open(method: Method, options: { signal?: AbortSignal | undefined; body?: boolean } = {}): Promise<CallStream> {
    if (this.dead !== null) {
      throw await this.dead;
    }

    const body = options.body ?? false;
    const signal = options.signal;
    const b = toBinary(CallSchema, create(CallSchema, { method }));

    let st: Stream | null = null;
    try {
      if (b.length <= maxPayload) {
        st = await this.track(this.conn.open(b, !body, signal));
      } else {
        // Too large for a frame: the call goes as DATA (continued).
        const head = toBinary(CallSchema, create(CallSchema, { continued: b.length }));
        st = await this.track(this.conn.open(head, false, signal));
        await this.track(st.write(b));
        if (!body) {
          await this.track(st.closeWrite());
        }
      }
    } catch (e) {
      st?.close();

      throw await this.mapError(e, st?.id ?? null, signal);
    }

    return new CallStream(this, st, signal);
  }

  /** Makes a call with no body either way and returns its reply, throwing its error. */
  async call(method: Method, signal?: AbortSignal): Promise<Reply> {
    const k = await this.open(method, { signal });
    try {
      const r = await k.reply();
      while ((await k.read()) !== null) {
        // A call with no body has none; drain anything regardless.
      }

      if (r.error !== undefined) {
        throw errorFromPb(r.error);
      }

      return r;
    } finally {
      k.close();
    }
  }

  /** Frees an object, waiting for the core. */
  async free(id: bigint): Promise<void> {
    if (!this.isAlive()) {
      // The core freed everything the connection owned.
      return;
    }

    try {
      await this.call({ case: "close", value: { id } });
    } catch (e) {
      if (this.isAlive()) {
        throw e;
      }
    }
  }

  private onEvent(p: Uint8Array): void {
    let ev;
    try {
      ev = fromBinary(EventSchema, p);
    } catch {
      return;
    }

    switch (ev.event.case) {
      case "streamError":
        if (ev.event.value.error !== undefined) {
          this.streamErrors.set(ev.event.value.stream, ev.event.value.error);
        }

        break;
      case "websocketWritten": {
        const w = ev.event.value;
        this.sockets.get(w.websocket)?.written(w.seq, w.error);

        break;
      }
      case "setCookie": {
        const sc = ev.event.value;
        const fn = this.jars.get(sc.jar);
        if (fn !== undefined) {
          // The caller's callback runs off the reader.
          queueMicrotask(() => {
            fn(sc);
          });
        }

        break;
      }
      default:
        break;
    }
  }

  // acceptLoop answers the core's reverse calls until the connection ends.
  private async acceptLoop(): Promise<void> {
    for (;;) {
      let st: Stream;
      try {
        st = await this.conn.accept();
      } catch {
        return;
      }

      void this.answer(st);
    }
  }

  private async answer(st: Stream): Promise<void> {
    try {
      let ev = fromBinary(EventSchema, st.payload);
      if (ev.continued > 0) {
        ev = fromBinary(EventSchema, await readFull(st, ev.continued));
      }

      // Fail closed: a connection nothing here verified is refused.
      let refusal: string | null = `mimicry: no answer to the reverse call ${ev.event.case ?? "(none)"}`;
      if (ev.event.case === "verifyConnection") {
        const v = ev.event.value;
        const fn = this.verifiers.get(v.transport);
        if (fn === undefined || v.state === undefined) {
          refusal = "mimicry: no verifyConnection for this client or transport";
        } else {
          try {
            await fn(v.state);
            refusal = null;
          } catch (e) {
            refusal = e instanceof Error ? e.message : String(e);
          }
        }
      }

      const reply = create(EventReplySchema, {});
      if (refusal !== null) {
        reply.error = create(ErrorSchema, {
          code: PbCode.VERIFY,
          message: refusal,
        });
      }

      await st.writeReply(toBinary(EventReplySchema, reply), true);
    } catch {
      // The core gave the call up, or the connection ended.
    } finally {
      st.close();
    }
  }
}

async function readFull(st: Stream, n: number): Promise<Uint8Array> {
  const out = new Uint8Array(n);
  let got = 0;
  while (got < n) {
    const b = await st.read(n - got);
    if (b === null) {
      throw new ProtocolError(`mimicry: a continued message ended after ${got} of ${n} bytes`);
    }

    out.set(b, got);
    got += b.length;
  }

  return out;
}

/** An open call: its reply, then DATA either way. */
export class CallStream {
  readonly core: Core;
  readonly stream: Stream;
  private readonly signal: AbortSignal | undefined;

  constructor(core: Core, stream: Stream, signal: AbortSignal | undefined) {
    this.core = core;
    this.stream = stream;
    this.signal = signal;
    void stream.ended.then(() => {
      // Its StreamError has been read, or nothing will read it.
      setImmediate(() => this.core.streamErrors.delete(stream.id));
    });
  }

  private async wrap<T>(p: Promise<T>): Promise<T> {
    try {
      return await this.core.track(p);
    } catch (e) {
      throw await this.core.mapError(e, this.stream.id, this.signal);
    }
  }

  /** Waits for the reply, reading it from DATA when continued. */
  reply(): Promise<Reply> {
    return this.wrap(
      (async () => {
        const r = fromBinary(ReplySchema, await this.stream.reply());
        if (r.continued === 0) {
          return r;
        }

        return fromBinary(ReplySchema, await readFull(this.stream, r.continued));
      })(),
    );
  }

  /** The next chunk of the reply's body; null at its end. */
  read(max?: number): Promise<Uint8Array | null> {
    return this.wrap(this.stream.read(max));
  }

  write(b: Uint8Array): Promise<void> {
    return this.wrap(this.stream.write(b));
  }

  closeWrite(): Promise<void> {
    return this.wrap(this.stream.closeWrite());
  }

  /** Ends the call, resetting it if it has not ended. */
  close(reason?: unknown): void {
    this.stream.close(reason);
  }
}

let current: Promise<Core> | null = null;

/** The process's core, started at first use and again after one dies. */
export async function defaultCore(): Promise<Core> {
  const p = current;
  if (p !== null) {
    const c = await p.catch(() => null);
    if (c?.isAlive() === true) {
      return c;
    }

    if (current !== p) {
      return defaultCore();
    }
  }

  const next = Core.start();
  current = next;
  next.catch(() => {
    if (current === next) {
      current = null;
    }
  });

  return next;
}

/** Stops the process's core, if one runs; the next call starts another. */
export function stopDefaultCore(): void {
  const p = current;
  current = null;
  void p?.then((c) => {
    c.stop();
  }, () => undefined);
}

interface Held {
  core: Core;
  id: bigint;
  cleanup: (() => void) | undefined;
}

const registry = new FinalizationRegistry<Held>(({ core, id, cleanup }) => {
  cleanup?.();
  core.free(id).catch(() => undefined);
});

/**
 * An object the core holds for this process: freed by close(), by
 * `await using`, or when it is garbage collected. cleanup, which must not
 * refer to the object, runs when it is freed.
 */
export class CoreObject {
  /** @internal */
  readonly core: Core;
  /** @internal The object's id in the core. */
  readonly id: bigint;
  private readonly cleanup: (() => void) | undefined;
  private closed = false;

  constructor(core: Core, id: bigint, cleanup?: () => void) {
    this.core = core;
    this.id = id;
    this.cleanup = cleanup;
    registry.register(this, { core, id, cleanup }, this);
  }

  /** Frees the object in the core. Closing it again does nothing. */
  async close(): Promise<void> {
    if (this.closed) {
      return;
    }

    this.closed = true;
    registry.unregister(this);
    this.cleanup?.();
    await this.core.free(this.id);
  }

  async [Symbol.asyncDispose](): Promise<void> {
    await this.close();
  }
}
