// The framed, multiplexed protocol between mimicry-core and this SDK
// (decision 0069), a port of the Go implementation (internal/rpc/wire):
// frames, streams, per-stream credit, RESET and EVENT over a readable and
// a writable Node stream. CALL, REPLY and EVENT payloads are opaque bytes
// here; the messages in them belong to the layer above. The rules, and the
// vectors this implementation replays as Go's does, are in
// proto/testdata/wire/README.md.
//
// Nothing grows without bound: a stream buffers at most `window` bytes of
// the peer's DATA (the credit it granted); each side has at most
// `maxStreams` streams open; the write queue holds at most one frame per
// stream from its caller plus one WINDOW and one RESET per stream. No
// timer runs here: deadlines are the callers' AbortSignals.

import type { Readable, Writable } from "node:stream";

/** The most a frame carries. */
export const maxPayload = 64 * 1024;
/** The credit each direction of a stream starts with, on both ends. */
export const defaultWindow = 256 * 1024;
/** How many streams each side may have open, on both ends. */
export const defaultMaxStreams = 256;

const headerSize = 10;
const maxCredit = 2 ** 31 - 1;

const typeCall = 1;
const typeReply = 2;
const typeData = 3;
const typeWindow = 4;
const typeReset = 5;
const typeEvent = 6;
const flagEnd = 0x01;

const typeNames = ["type 0", "CALL", "REPLY", "DATA", "WINDOW", "RESET", "EVENT"];

function typeName(t: number): string {
  return typeNames[t] ?? `type ${t}`;
}

/** Why a stream or the connection was reset: a RESET frame's u32. */
export const Code = {
  Cancel: 0,
  StreamLimit: 1,
  CreditOverrun: 2,
  FrameTooLarge: 3,
  UnknownType: 4,
  UnknownFlags: 5,
  StreamClosed: 6,
  StreamNotOpen: 7,
  WrongParity: 8,
  BadFrame: 9,
} as const;

const codeNames = [
  "CANCEL",
  "STREAM_LIMIT",
  "CREDIT_OVERRUN",
  "FRAME_TOO_LARGE",
  "UNKNOWN_TYPE",
  "UNKNOWN_FLAGS",
  "STREAM_CLOSED",
  "STREAM_NOT_OPEN",
  "WRONG_PARITY",
  "BAD_FRAME",
];

/** The protocol's name of a code ("CANCEL"), or CODE_<n> for one unknown. */
export function codeName(code: number): string {
  return codeNames[code] ?? `CODE_${code}`;
}

interface WireErrorOptions {
  code?: number | undefined;
  remote?: boolean;
  conn?: boolean;
  truncated?: boolean;
  cause?: unknown;
}

/**
 * A stream's or the connection's failure. `conn` says the connection
 * failed (every stream with it); otherwise one stream did. `code` is the
 * protocol's code when there is one, `remote` that the peer sent it in a
 * RESET, `truncated` that the connection ended inside a frame.
 */
export class WireError extends Error {
  override name = "WireError";
  readonly code: number | undefined;
  readonly remote: boolean;
  readonly conn: boolean;
  readonly truncated: boolean;

  constructor(message: string, options: WireErrorOptions = {}) {
    super(message, options.cause === undefined ? undefined : { cause: options.cause });
    this.code = options.code;
    this.remote = options.remote ?? false;
    this.conn = options.conn ?? false;
    this.truncated = options.truncated ?? false;
  }
}

/** A protocol error this side found: it fails the connection, except a credit overrun. */
function violation(code: number, message: string): WireError {
  return new WireError(`wire: ${message}`, {
    code,
    conn: code !== Code.CreditOverrun,
  });
}

function connClosed(cause?: unknown): WireError {
  return new WireError("wire: connection closed", {
    conn: true,
    cause,
  });
}

function canceled(id: number, code: number, reason?: unknown): WireError {
  return new WireError(`wire: stream ${id} reset with ${codeName(code)}`, {
    code: Code.Cancel,
    cause: reason,
  });
}

/** One wake-up for whoever waits; a later wait gets a fresh promise. */
class Signal {
  private promise: Promise<void> | null = null;
  private resolve: (() => void) | null = null;

  wait(): Promise<void> {
    if (this.promise === null) {
      const { promise, resolve } = Promise.withResolvers<void>();
      this.promise = promise;
      this.resolve = resolve;
    }

    return this.promise;
  }

  notify(): void {
    const resolve = this.resolve;
    if (resolve !== null) {
      this.promise = null;
      this.resolve = null;
      resolve();
    }
  }
}

interface OutFrame {
  s: Stream | null;
  typ: number;
  // buf is the frame; null for WINDOW, built from its stream's pending
  // credit when written.
  buf: Uint8Array | null;
  // open marks a stream's opening frame, written even if the stream was
  // reset meanwhile (the RESET follows it).
  open: boolean;
  done: ((err: WireError | null) => void) | null;
}

function encodeFrame(stream: number, typ: number, flags: number, payload: Uint8Array): Uint8Array {
  const b = Buffer.allocUnsafe(headerSize + payload.length);
  b.writeUInt32BE(stream, 0);
  b[4] = typ;
  b[5] = flags;
  b.writeUInt32BE(payload.length, 6);
  b.set(payload, headerSize);

  return b;
}

function u32(v: number): Uint8Array {
  const b = Buffer.allocUnsafe(4);
  b.writeUInt32BE(v, 0);

  return b;
}

function resetFrame(id: number, code: number): Uint8Array {
  return encodeFrame(id, typeReset, 0, u32(code));
}

interface Header {
  stream: number;
  typ: number;
  flags: number;
  length: number;
}

/** The side of the connection a Conn speaks for. */
export type Role = "sdk" | "core";

export interface ConnConfig {
  role: Role;
  /** The credit each direction of a stream starts with (both ends equal). */
  window?: number;
  /** How many streams each side may have open (both ends equal). */
  maxStreams?: number;
  /**
   * Receives the core's notices (EVENT on stream 0) on the SDK's side. It
   * runs while the connection is being read: hand the payload off.
   */
  onEvent?: (payload: Uint8Array) => void;
}

/**
 * One end of a connection: streams multiplexed over `input` (read) and
 * `output` (written). The writer takes frames first come, first served,
 * and each stream has at most one of its caller's frames queued, so a long
 * body takes turns with every other stream frame by frame.
 */
export class Conn {
  readonly role: Role;
  readonly window: number;
  readonly maxStreams: number;
  private readonly onEvent: ((payload: Uint8Array) => void) | undefined;
  private readonly output: Writable;

  private error: WireError | null = null;
  private readonly streams = new Map<number, Stream>();
  private nextID: number;
  private lastPeer = 0;
  private peerOpen = 0;
  private localOpen = 0;
  private slotWaiters: { resolve: () => void; reject: (err: unknown) => void }[] = [];
  private acceptQ: Stream[] = [];
  private readonly accepted = new Signal();

  private wq: OutFrame[] = [];
  private pumpQueued = false;
  private blocked = false;
  private abortFrame: Uint8Array | null = null;
  private outputEnded = false;

  // The reader's state: bytes received but not yet parsed, and the header
  // of a frame whose payload has yet to arrive.
  private chunks: Buffer[] = [];
  private avail = 0;
  private header: Header | null = null;

  private resolveDone!: () => void;
  /** Resolves once the connection has failed and its output is ended. */
  readonly done: Promise<void>;

  constructor(input: Readable, output: Writable, cfg: ConnConfig) {
    const window = cfg.window ?? defaultWindow;
    const maxStreams = cfg.maxStreams ?? defaultMaxStreams;
    if (!Number.isInteger(window) || window < 1 || window > maxCredit) {
      throw new RangeError(`wire: window ${window} out of range`);
    }

    if (!Number.isInteger(maxStreams) || maxStreams < 1) {
      throw new RangeError(`wire: maxStreams ${maxStreams} out of range`);
    }

    this.role = cfg.role;
    this.window = window;
    this.maxStreams = maxStreams;
    this.onEvent = cfg.onEvent;
    this.output = output;
    this.nextID = cfg.role === "core" ? 2 : 1;
    const done = Promise.withResolvers<void>();
    this.done = done.promise;
    this.resolveDone = done.resolve;

    input.on("data", (chunk: Buffer) => {
      this.receive(chunk);
    });
    input.on("end", () => {
      this.receiveEnd();
    });
    input.on("error", (err) => {
      this.fail(connClosed(err), null);
    });
    output.on("error", (err) => {
      this.fail(connClosed(err), null);
    });
  }

  /** Why the connection failed, or null while it runs. */
  get err(): WireError | null {
    return this.error;
  }

  /**
   * Opens a stream with payload: a CALL on the SDK's side, an EVENT (a
   * reverse call) on the core's. `end` says nothing follows in this
   * direction. It waits for a free stream (maxStreams) and for the frame
   * to be written. If `signal` aborts first, the stream is reset (CANCEL).
   */
  async open(payload: Uint8Array, end: boolean, signal?: AbortSignal): Promise<Stream> {
    if (payload.length > maxPayload) {
      throw violation(Code.FrameTooLarge, `a ${payload.length}-byte payload`);
    }

    await this.acquireSlot(signal);

    if (this.error !== null || this.nextID > 0xffffffff) {
      const err = this.error ?? new WireError("wire: stream ids exhausted");
      this.releaseSlot();

      throw err;
    }

    const id = this.nextID;
    this.nextID += 2;

    const s = new Stream(this, id, true, this.window);
    this.streams.set(id, s);

    if (end) {
      s.sendEnded = true;
    }

    const f = this.newFrame(s, id, this.role === "sdk" ? typeCall : typeEvent, end ? flagEnd : 0, payload);
    f.open = true;
    this.enqueue(f);

    if (signal !== undefined) {
      const onAbort = (): void => {
        this.resetStream(s, Code.Cancel, canceled(id, Code.Cancel, signal.reason));
      };
      signal.addEventListener("abort", onAbort, { once: true });
      s.stopAbort = () => {
        signal.removeEventListener("abort", onAbort);
      };
    }

    const err = await s.awaitFrame(f);
    if (err !== null) {
      throw err;
    }

    return s;
  }

  private acquireSlot(signal?: AbortSignal): Promise<void> {
    if (signal?.aborted === true) {
      return Promise.reject(canceled(this.nextID, Code.Cancel, signal.reason));
    }

    if (this.error !== null) {
      return Promise.reject(this.error);
    }

    if (this.localOpen < this.maxStreams) {
      this.localOpen++;

      return Promise.resolve();
    }

    const { promise, resolve, reject } = Promise.withResolvers<void>();
    const onAbort = (): void => {
      this.slotWaiters = this.slotWaiters.filter((w) => w !== waiter);
      reject(canceled(this.nextID, Code.Cancel, signal?.reason));
    };
    const waiter = {
      resolve: () => {
        signal?.removeEventListener("abort", onAbort);
        resolve();
      },
      reject: (err: unknown) => {
        signal?.removeEventListener("abort", onAbort);
        reject(err);
      },
    };
    signal?.addEventListener("abort", onAbort, { once: true });
    this.slotWaiters.push(waiter);

    return promise;
  }

  private releaseSlot(): void {
    const w = this.slotWaiters.shift();
    if (w !== undefined) {
      // The slot passes to the waiter.
      w.resolve();

      return;
    }

    this.localOpen--;
  }

  /**
   * The next stream the peer opened: CALLs on the core's side, EVENT
   * streams (reverse calls) on the SDK's. A stream the peer reset before
   * it was accepted is never returned.
   */
  async accept(): Promise<Stream> {
    for (;;) {
      const s = this.acceptQ.shift();
      if (s !== undefined) {
        return s;
      }

      if (this.error !== null) {
        throw this.error;
      }

      await this.accepted.wait();
    }
  }

  /** Sends a notice (EVENT on stream 0); only the core sends them. */
  async notify(payload: Uint8Array): Promise<void> {
    if (this.role !== "core") {
      throw new Error("wire: only the core sends notices");
    }

    if (payload.length > maxPayload) {
      throw violation(Code.FrameTooLarge, `a ${payload.length}-byte payload`);
    }

    if (this.error !== null) {
      throw this.error;
    }

    const f = this.newFrame(null, 0, typeEvent, 0, payload);
    const { promise, resolve } = Promise.withResolvers<WireError | null>();
    f.done = resolve;
    this.enqueue(f);

    const err = await promise;
    if (err !== null) {
      throw err;
    }
  }

  /** Closes the connection: every stream fails and the output ends. */
  close(): void {
    this.fail(connClosed(), null);
  }

  /**
   * Ends the connection with err: every stream fails, queued frames are
   * dropped, and the writer sends abort (if any) and ends the output.
   * Only the first failure counts.
   */
  private fail(err: WireError, abort: Uint8Array | null): void {
    if (this.error !== null) {
      return;
    }

    this.error = err;
    this.abortFrame = abort;

    for (const s of this.streams.values()) {
      s.err ??= err;
      s.freed = true;
      s.released = true;
      s.end();
    }

    this.streams.clear();
    this.acceptQ = [];

    const wq = this.wq;
    this.wq = [];
    for (const f of wq) {
      f.done?.(err);
    }

    const waiters = this.slotWaiters;
    this.slotWaiters = [];
    for (const w of waiters) {
      w.reject(err);
    }

    this.accepted.notify();
    this.finishOutput();
  }

  private finishOutput(): void {
    if (this.outputEnded) {
      return;
    }

    this.outputEnded = true;

    const abort = this.abortFrame;
    this.abortFrame = null;

    try {
      if (abort !== null) {
        this.output.write(abort);
      }

      this.output.end();
    } catch {
      // The output already failed; nothing more to send.
    }

    this.resolveDone();
  }

  newFrame(s: Stream | null, id: number, typ: number, flags: number, payload: Uint8Array): OutFrame {
    return {
      s,
      typ,
      buf: encodeFrame(id, typ, flags, payload),
      open: false,
      done: null,
    };
  }

  enqueue(f: OutFrame): void {
    this.wq.push(f);

    if (!this.pumpQueued && !this.blocked) {
      this.pumpQueued = true;
      queueMicrotask(() => {
        this.pumpQueued = false;
        this.pump();
      });
    }
  }

  private pump(): void {
    while (!this.blocked && this.error === null) {
      const f = this.wq.shift();
      if (f === undefined) {
        return;
      }

      const [buf, skip] = this.prepare(f);
      if (buf === null) {
        f.done?.(skip);

        continue;
      }

      let ok: boolean;
      try {
        ok = this.output.write(buf);
      } catch (e) {
        const err = connClosed(e);
        this.fail(err, null);
        f.done?.(err);

        return;
      }

      f.done?.(null);

      if (!ok) {
        this.blocked = true;
        this.output.once("drain", () => {
          this.blocked = false;
          this.pump();
        });
      }
    }
  }

  // The bytes to write for f, or null and the error its caller gets when
  // it is not to be written: DATA and REPLY of a stream this side reset
  // (its RESET follows), WINDOW once its direction ended.
  private prepare(f: OutFrame): [Uint8Array | null, WireError | null] {
    const s = f.s;
    if (s === null || f.open) {
      return [f.buf, null];
    }

    if (f.typ === typeReset) {
      // A stream holds its place in maxStreams until its RESET leaves
      // the queue, so a peer that stops reading cannot make RESETs pile
      // up: at most one per stream it may have open.
      s.resetQueued = false;
      if (s.freed) {
        this.release(s);
      }

      return [f.buf, null];
    }

    if (f.typ === typeWindow) {
      s.windowQueued = false;

      const inc = s.pendingWindow;
      s.pendingWindow = 0;

      if (inc === 0 || s.resetSent || s.recvEnded || s.freed) {
        return [null, null];
      }

      return [encodeFrame(s.id, typeWindow, 0, u32(inc)), null];
    }

    if (s.resetSent) {
      return [null, s.err];
    }

    return [f.buf, null];
  }

  private receive(chunk: Buffer): void {
    if (this.error !== null) {
      return;
    }

    this.chunks.push(chunk);
    this.avail += chunk.length;

    // Each failure below returns: the connection reads nothing more.
    for (;;) {
      if (this.header === null) {
        if (this.avail < headerSize) {
          return;
        }

        const hb = this.take(headerSize);
        const h: Header = {
          stream: hb.readUInt32BE(0),
          typ: hb[4] ?? 0,
          flags: hb[5] ?? 0,
          length: hb.readUInt32BE(6),
        };
        const err = checkHeader(h);
        if (err !== null) {
          this.readFailed(err);

          return;
        }

        this.header = h;
      }

      if (this.avail < this.header.length) {
        return;
      }

      const h = this.header;
      this.header = null;

      const err = this.handle(h, this.take(h.length));
      if (err !== null) {
        this.readFailed(err);

        return;
      }
    }
  }

  private receiveEnd(): void {
    if (this.error !== null) {
      return;
    }

    if (this.header !== null) {
      const h = this.header;
      this.fail(
        new WireError(`wire: connection ended inside a frame: ${typeName(h.typ)} ${h.stream}'s ${h.length}-byte payload cut short`, {
          conn: true,
          truncated: true,
        }),
        null,
      );

      return;
    }

    if (this.avail > 0) {
      this.fail(
        new WireError(`wire: connection ended inside a frame: ${headerSize}-byte header cut short`, {
          conn: true,
          truncated: true,
        }),
        null,
      );

      return;
    }

    this.fail(connClosed(), null);
  }

  // take removes n bytes from the received chunks, without copying when
  // they lie in one.
  private take(n: number): Buffer {
    this.avail -= n;

    const first = this.chunks[0];
    if (first !== undefined && first.length >= n) {
      if (first.length === n) {
        this.chunks.shift();
      } else {
        this.chunks[0] = first.subarray(n);
      }

      return first.subarray(0, n);
    }

    const out = Buffer.allocUnsafe(n);
    let off = 0;
    while (off < n) {
      const c = this.chunks[0];
      if (c === undefined) {
        break;
      }

      const k = Math.min(c.length, n - off);
      c.copy(out, off, 0, k);
      off += k;
      if (k === c.length) {
        this.chunks.shift();
      } else {
        this.chunks[0] = c.subarray(k);
      }
    }

    return out;
  }

  // readFailed fails the connection with what reading found: the peer's
  // RESET of the connection (sent nothing back), or a protocol error
  // (answered with a RESET on stream 0 naming it).
  private readFailed(err: WireError): void {
    if (err.remote || err.code === undefined) {
      this.fail(err, null);

      return;
    }

    this.fail(err, resetFrame(0, err.code));
  }

  // The frame that opens the peer's streams, and their ids' parity.
  private peerOpenType(): [number, number] {
    return this.role === "core" ? [typeCall, 1] : [typeEvent, 0];
  }

  private handle(h: Header, p: Buffer): WireError | null {
    const shape = this.check(h, p);
    if (shape !== null) {
      return shape;
    }

    if (h.stream === 0) {
      if (h.typ === typeReset) {
        const code = p.readUInt32BE(0);

        return new WireError(`wire: the peer reset the connection: ${codeName(code)}`, {
          code,
          remote: true,
          conn: true,
        });
      }

      // A notice: check let EVENT through on the SDK's side only.
      this.onEvent?.(p);

      return null;
    }

    const s = this.streams.get(h.stream);
    if (s !== undefined && !s.freed) {
      return this.handleStream(s, h, p);
    }

    return this.handleNew(h, p);
  }

  // check is what a frame must be wherever it comes.
  private check(h: Header, p: Buffer): WireError | null {
    if (h.typ === typeCall && this.role === "sdk") {
      return violation(Code.BadFrame, `CALL ${h.stream} from the core`);
    }

    if (h.typ === typeEvent && this.role === "core") {
      return violation(Code.BadFrame, `EVENT ${h.stream} from the SDK`);
    }

    if (h.stream === 0 && h.typ !== typeEvent && h.typ !== typeReset) {
      return violation(Code.BadFrame, `${typeName(h.typ)} on stream 0`);
    }

    if (h.stream === 0 && h.flags !== 0) {
      return violation(Code.BadFrame, `END on ${typeName(h.typ)} 0`);
    }

    if (h.typ !== typeWindow && h.typ !== typeReset) {
      return null;
    }

    if (h.flags !== 0) {
      return violation(Code.BadFrame, `END on ${typeName(h.typ)} ${h.stream}`);
    }

    if (p.length !== 4) {
      return violation(Code.BadFrame, `${typeName(h.typ)} ${h.stream} with a ${p.length}-byte payload`);
    }

    const inc = p.readUInt32BE(0);
    if (h.typ === typeWindow && (inc === 0 || inc > maxCredit)) {
      return violation(Code.BadFrame, `WINDOW ${h.stream} of ${inc}`);
    }

    return null;
  }

  // handleNew applies a frame on a stream not open: it opens one of the
  // peer's, or it is a WINDOW or RESET in flight when the stream ended.
  private handleNew(h: Header, p: Buffer): WireError | null {
    const [open, parity] = this.peerOpenType();
    const peers = h.stream % 2 === parity;

    if (h.typ === open) {
      if (!peers) {
        return violation(Code.WrongParity, `${typeName(h.typ)} ${h.stream}`);
      }

      if (h.stream <= this.lastPeer) {
        return violation(Code.StreamClosed, `${typeName(h.typ)} ${h.stream} reuses an id`);
      }

      if (this.peerOpen >= this.maxStreams) {
        return violation(Code.StreamLimit, `${typeName(h.typ)} ${h.stream} with ${this.peerOpen} open`);
      }

      const s = new Stream(this, h.stream, false, this.window);
      s.payload = p;
      s.recvEnded = (h.flags & flagEnd) !== 0;
      this.lastPeer = h.stream;
      this.peerOpen++;
      this.streams.set(h.stream, s);
      this.acceptQ.push(s);
      this.accepted.notify();

      return null;
    }

    const opened = peers ? h.stream <= this.lastPeer : h.stream < this.nextID;
    if (!opened) {
      return violation(Code.StreamNotOpen, `${typeName(h.typ)} ${h.stream}`);
    }

    if (h.typ === typeWindow || h.typ === typeReset) {
      return null;
    }

    return violation(Code.StreamClosed, `${typeName(h.typ)} ${h.stream}`);
  }

  // handleStream applies a frame on an open stream, its errors in the
  // precedence the README gives: the stream's state, then the order of its
  // frames, then credit.
  private handleStream(s: Stream, h: Header, p: Buffer): WireError | null {
    const end = (h.flags & flagEnd) !== 0;
    const opening = h.typ === typeCall || h.typ === typeEvent;

    if (s.resetSent && !opening) {
      // This side reset the stream: the peer's frames are in flight until
      // its END or RESET says it stopped.
      if (h.typ === typeReset || end) {
        s.recvEnded = true;
        this.free(s);
      }

      return null;
    }

    switch (h.typ) {
      case typeCall:
      case typeEvent: {
        // check let only the peer's opening type through: it opens an id
        // already open, the peer's or this side's.
        const [, parity] = this.peerOpenType();
        if (h.stream % 2 !== parity) {
          return violation(Code.WrongParity, `${typeName(h.typ)} ${h.stream}`);
        }

        return violation(Code.StreamClosed, `${typeName(h.typ)} ${h.stream} reuses an id`);
      }

      case typeReply:
        if (s.recvEnded) {
          return violation(Code.StreamClosed, `REPLY ${h.stream} after END`);
        }

        if (!s.local) {
          return violation(Code.BadFrame, `REPLY ${h.stream} from the stream's opener`);
        }

        if (s.gotReply) {
          return violation(Code.BadFrame, `a second REPLY on ${h.stream}`);
        }

        s.gotReply = true;
        s.replyPayload = p;
        s.recvEnded = end;
        s.rcv.notify();
        this.maybeFree(s);

        return null;

      case typeData:
        if (s.recvEnded) {
          return violation(Code.StreamClosed, `DATA ${h.stream} after END`);
        }

        if (s.local && !s.gotReply) {
          return violation(Code.BadFrame, `DATA ${h.stream} before REPLY`);
        }

        s.recvEnded = end;

        if (p.length > s.recvCredit) {
          this.resetStream(
            s,
            Code.CreditOverrun,
            violation(Code.CreditOverrun, `${p.length} bytes on stream ${h.stream} with ${s.recvCredit} of credit`),
          );

          return null;
        }

        s.recvCredit -= p.length;

        if (p.length > 0) {
          s.recv.push(p);
          s.buffered += p.length;
        }

        s.rcv.notify();
        this.maybeFree(s);

        return null;

      case typeWindow: {
        if (s.sendEnded) {
          return null;
        }

        const inc = p.readUInt32BE(0);
        if (s.sendCredit > maxCredit - inc) {
          return violation(Code.BadFrame, `WINDOW ${h.stream} takes the credit past 2^31-1`);
        }

        s.sendCredit += inc;
        s.snd.notify();

        return null;
      }

      case typeReset: {
        const code = p.readUInt32BE(0);
        s.err = new WireError(`wire: the peer reset stream ${s.id}: ${codeName(code)}`, {
          code,
          remote: true,
        });
        s.recvEnded = true;

        // Answer, so the peer knows this side stopped too, unless an END
        // already said so.
        if (!s.sendEnded) {
          s.sendEnded = true;
          s.resetQueued = true;
          this.enqueue({
            s,
            typ: typeReset,
            buf: resetFrame(s.id, Code.Cancel),
            open: false,
            done: null,
          });
        }

        this.removeAccept(s);
        this.free(s);

        return null;
      }
    }

    return null;
  }

  /**
   * Resets s from this side with code, failing it with err. The stream
   * stays until the peer's END or RESET shows it stopped sending.
   */
  resetStream(s: Stream, code: number, err: WireError): void {
    if (s.freed || s.resetSent) {
      return;
    }

    s.err = err;
    s.resetSent = true;
    s.sendEnded = true;
    s.resetQueued = true;
    this.enqueue({
      s,
      typ: typeReset,
      buf: resetFrame(s.id, code),
      open: false,
      done: null,
    });
    this.removeAccept(s);
    s.end();

    if (s.recvEnded) {
      this.free(s);
    }
  }

  maybeFree(s: Stream): void {
    if (s.sendEnded && s.recvEnded && !s.resetSent) {
      this.free(s);
    }
  }

  // free ends a stream: both sides stopped sending on it. Its caller may
  // still read what it buffered, unless it failed. Its place in
  // maxStreams is released now, or once its RESET leaves the queue.
  private free(s: Stream): void {
    if (s.freed) {
      return;
    }

    s.freed = true;

    if (s.err !== null) {
      s.recv = [];
      s.buffered = 0;
    }

    s.end();

    if (!s.resetQueued) {
      this.release(s);
    }
  }

  // release forgets an ended stream and frees its place in maxStreams.
  private release(s: Stream): void {
    if (s.released) {
      return;
    }

    s.released = true;
    this.streams.delete(s.id);

    if (s.local) {
      this.releaseSlot();
    } else {
      this.peerOpen--;
    }
  }

  // removeAccept drops a stream the peer opened and this side never
  // accepted: nothing will read its payload.
  private removeAccept(s: Stream): void {
    const i = this.acceptQ.indexOf(s);
    if (i >= 0) {
      this.acceptQ.splice(i, 1);
      s.payload = new Uint8Array(0);
    }
  }
}

function checkHeader(h: Header): WireError | null {
  if (h.typ < typeCall || h.typ > typeEvent) {
    return violation(Code.UnknownType, `unknown frame type: ${h.typ} on stream ${h.stream}`);
  }

  if ((h.flags & ~flagEnd) !== 0) {
    return violation(Code.UnknownFlags, `unknown frame flags: 0x${h.flags.toString(16).padStart(2, "0")} on ${typeName(h.typ)} ${h.stream}`);
  }

  if (h.length > maxPayload) {
    return violation(Code.FrameTooLarge, `frame too large: ${typeName(h.typ)} ${h.stream} is ${h.length} bytes`);
  }

  return null;
}

/**
 * One call: its opening frame (CALL or EVENT), one REPLY, then DATA both
 * ways, each direction ended by END. One reader and one writer at a time.
 * Close every stream: one left open holds its maxStreams slot until both
 * directions end.
 */
export class Stream {
  readonly id: number;
  readonly local: boolean;
  /** The opening frame's payload of a stream the peer opened. */
  payload: Uint8Array = new Uint8Array(0);

  private readonly c: Conn;
  private readonly halfWindow: number;

  // The state below is Go's, driven by the Conn.
  err: WireError | null = null;
  replyPayload: Uint8Array | null = null;
  gotReply = false;
  replySent = false;
  recv: Uint8Array[] = [];
  buffered = 0;
  recvCredit: number;
  unreturned = 0;
  pendingWindow = 0;
  windowQueued = false;
  recvEnded = false;
  sendCredit: number;
  sendEnded = false;
  resetSent = false;
  resetQueued = false;
  freed = false;
  released = false;
  stopAbort: (() => void) | null = null;

  readonly rcv = new Signal();
  readonly snd = new Signal();

  private isEnded = false;
  private resolveEnded!: () => void;
  /** Resolves when the stream ends: reset, failed, or both directions ended. */
  readonly ended: Promise<void>;

  private writeLock: Promise<unknown> = Promise.resolve();
  private readLock: Promise<unknown> = Promise.resolve();

  constructor(c: Conn, id: number, local: boolean, window: number) {
    this.c = c;
    this.id = id;
    this.local = local;
    this.recvCredit = window;
    this.sendCredit = window;
    this.halfWindow = Math.ceil(window / 2);
    const ended = Promise.withResolvers<void>();
    this.ended = ended.promise;
    this.resolveEnded = ended.resolve;
  }

  /** Whether the stream has ended (see `ended`). */
  get done(): boolean {
    return this.isEnded;
  }

  /** Marks the stream ended and wakes whoever waits on it. */
  end(): void {
    if (!this.isEnded) {
      this.isEnded = true;
      this.stopAbort?.();
      this.stopAbort = null;
      this.resolveEnded();
    }

    this.rcv.notify();
    this.snd.notify();
  }

  /** Waits for the REPLY to a stream this side opened and returns its payload. */
  async reply(): Promise<Uint8Array> {
    if (!this.local) {
      throw new Error("wire: reply on a stream the peer opened");
    }

    for (;;) {
      if (this.replyPayload !== null) {
        return this.replyPayload;
      }

      if (this.err !== null) {
        throw this.err;
      }

      await this.rcv.wait();
    }
  }

  private serial<T>(lock: "read" | "write", fn: () => Promise<T>): Promise<T> {
    const prev = lock === "read" ? this.readLock : this.writeLock;
    const next = prev.then(fn, fn);
    const settled = next.catch(() => undefined);
    if (lock === "read") {
      this.readLock = settled;
    } else {
      this.writeLock = settled;
    }

    return next;
  }

  /** Answers a stream the peer opened; `end` says no DATA follows. */
  writeReply(payload: Uint8Array, end: boolean): Promise<void> {
    if (this.local) {
      return Promise.reject(new Error("wire: writeReply on a stream this side opened"));
    }

    if (payload.length > maxPayload) {
      return Promise.reject(violation(Code.FrameTooLarge, `a ${payload.length}-byte payload`));
    }

    return this.serial("write", async () => {
      if (this.err !== null) {
        throw this.err;
      }

      if (this.replySent) {
        throw new Error("wire: a second REPLY");
      }

      this.replySent = true;
      if (end) {
        this.sendEnded = true;
      }

      const f = this.c.newFrame(this, this.id, typeReply, end ? flagEnd : 0, payload);
      this.c.enqueue(f);
      this.c.maybeFree(this);

      const err = await this.awaitFrame(f);
      if (err !== null) {
        throw err;
      }
    });
  }

  /**
   * Reads the peer's DATA: at most `max` bytes, the next buffered chunk or
   * part of it; null after its END. Bytes read are granted back to the
   * peer as credit once they reach half the window.
   */
  read(max = Infinity): Promise<Uint8Array | null> {
    return this.serial("read", async () => {
      for (;;) {
        if (this.err !== null) {
          throw this.err;
        }

        if (this.buffered > 0) {
          return this.take(max);
        }

        if (this.recvEnded) {
          return null;
        }

        await this.rcv.wait();
      }
    });
  }

  private take(max: number): Uint8Array {
    const first = this.recv[0] as Uint8Array;
    let out: Uint8Array;
    if (first.length <= max) {
      out = first;
      this.recv.shift();
    } else {
      out = first.subarray(0, max);
      this.recv[0] = first.subarray(max);
    }

    const n = out.length;
    this.buffered -= n;
    this.unreturned += n;

    if (this.recvEnded || this.unreturned < this.halfWindow) {
      return out;
    }

    this.recvCredit += this.unreturned;
    this.pendingWindow += this.unreturned;
    this.unreturned = 0;

    if (!this.windowQueued) {
      this.windowQueued = true;
      this.c.enqueue({
        s: this,
        typ: typeWindow,
        buf: null,
        open: false,
        done: null,
      });
    }

    return out;
  }

  private sendErr(): Error | null {
    if (this.err !== null) {
      return this.err;
    }

    if (this.sendEnded) {
      return new Error("wire: write after END");
    }

    if (!this.local && !this.replySent) {
      return new Error("wire: DATA before the REPLY");
    }

    return null;
  }

  /** Sends p as DATA, in frames of at most maxPayload, each waiting for credit and its turn. */
  write(p: Uint8Array): Promise<void> {
    return this.serial("write", async () => {
      let off = 0;
      while (off < p.length) {
        const err = this.sendErr();
        if (err !== null) {
          throw err;
        }

        if (this.sendCredit === 0) {
          await this.snd.wait();

          continue;
        }

        const k = Math.min(p.length - off, this.sendCredit, maxPayload);
        this.sendCredit -= k;

        const f = this.c.newFrame(this, this.id, typeData, 0, p.subarray(off, off + k));
        this.c.enqueue(f);

        const ferr = await this.awaitFrame(f);
        if (ferr !== null) {
          throw ferr;
        }

        off += k;
      }
    });
  }

  /** Ends this side's DATA (END). */
  closeWrite(): Promise<void> {
    return this.serial("write", async () => {
      const err = this.sendErr();
      if (err !== null) {
        throw err;
      }

      const f = this.c.newFrame(this, this.id, typeData, flagEnd, new Uint8Array(0));
      this.sendEnded = true;
      this.c.enqueue(f);
      this.c.maybeFree(this);

      const ferr = await this.awaitFrame(f);
      if (ferr !== null) {
        throw ferr;
      }
    });
  }

  /** Ends the stream, resetting it (CANCEL) if either direction is still open. */
  close(reason?: unknown): void {
    this.reset(Code.Cancel, reason);
  }

  /** Resets the stream with code if either direction is still open. */
  reset(code: number, reason?: unknown): void {
    this.c.resetStream(this, code, canceled(this.id, code, reason));
  }

  /**
   * Waits until f is written or dropped, or the stream ends first. A
   * stream that ended normally still writes f, so its result is awaited.
   */
  async awaitFrame(f: OutFrame): Promise<WireError | null> {
    const { promise: done, resolve } = Promise.withResolvers<WireError | null>();
    f.done = resolve;

    const first = await Promise.race([done, this.ended.then(() => undefined)]);
    if (first !== undefined) {
      return first;
    }

    this.c.resetStream(this, Code.Cancel, canceled(this.id, Code.Cancel));
    if (this.err !== null) {
      return this.err;
    }

    return done;
  }
}
