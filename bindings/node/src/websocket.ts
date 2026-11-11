// WebSockets: Go's Transport.DialWebSocket and *mimicry.WebSocket. The
// socket is one stream of the core's (proto/README.md, WebSockets): its
// DATA carries records both ways, type u8 | length u32 | payload, and each
// message written is confirmed by a WebSocketWritten notice.

import { CallStream, type Core, CoreObject, type SocketSink } from "./core.ts";
import { MimicryError, ProtocolError, errorFromPb } from "./errors.ts";
import { type Error as PbError, ErrorSchema, type WebSocket as PbWebSocket } from "./gen/mimicry/v1/mimicry_pb.js";
import { type HeaderInit, headerToPb } from "./header.ts";
import { Response } from "./client.ts";

import { fromBinary } from "@bufbuild/protobuf";

/** Go's mimicry.MessageType. */
export const MessageType = {
  Text: 1,
  Binary: 2,
} as const;
export type MessageType = (typeof MessageType)[keyof typeof MessageType];

/** Go's mimicry.WebSocketOptions (Go's From is page), plus the handshake's signal. */
export interface WebSocketOptions {
  /** The page opening the socket. */
  page?: string | URL;
  protocols?: readonly string[];
  header?: HeaderInit;
  /** Aborting it before the handshake is done cancels the dial. */
  signal?: AbortSignal;
}

/** A message read: its type and its bytes (text is UTF-8). */
export interface Message {
  type: MessageType;
  data: Uint8Array;
}

/** mimicry's bound on a message (64 MiB), both ways. */
const maxRecord = 64 << 20;
const recordEnd = 0;

// Pending is the socket's state the core's notices reach; it never refers
// to the WebSocket, so the WebSocket can be garbage collected.
class Pending implements SocketSink {
  readonly writes = new Map<bigint, { resolve: () => void; reject: (e: unknown) => void }>();

  written(seq: bigint, error: PbError | undefined): void {
    const w = this.writes.get(seq);
    if (w === undefined) {
      return;
    }

    this.writes.delete(seq);
    if (error === undefined) {
      w.resolve();
    } else {
      w.reject(errorFromPb(error));
    }
  }

  failAll(e: unknown): void {
    for (const w of this.writes.values()) {
      w.reject(e);
    }

    this.writes.clear();
  }
}

/** Go's *mimicry.WebSocket. */
export class WebSocket extends CoreObject {
  /** The subprotocol the server chose. */
  readonly protocol: string;
  readonly extensions: string;
  /** The server's handshake response (its headers; no body). */
  readonly response: Response | null;

  private readonly k: CallStream;
  private readonly pending: Pending;
  private seq = 0n;
  private writeLock: Promise<unknown> = Promise.resolve();
  private readLock: Promise<unknown> = Promise.resolve();
  private chunks: Uint8Array[] = [];
  private avail = 0;
  private closedErr: Error | null = null;

  constructor(core: Core, k: CallStream, ws: PbWebSocket) {
    const pending = new Pending();
    super(core, ws.id, () => {
      k.close();
      core.sockets.delete(ws.id);
    });
    this.k = k;
    this.pending = pending;
    this.protocol = ws.protocol;
    this.extensions = ws.extensions;
    this.response = ws.response === undefined ? null : new Response(ws.response, null);
    core.sockets.set(ws.id, pending);

    void k.stream.ended.then(async () => {
      core.sockets.delete(ws.id);
      if (pending.writes.size > 0) {
        pending.failAll(k.stream.err === null ? new Error("mimicry: the WebSocket closed") : await core.mapError(k.stream.err, k.stream.id));
      }
    });
  }

  // readExact takes n bytes of the stream, or null at its end before any.
  private async readExact(n: number): Promise<Uint8Array | null> {
    while (this.avail < n) {
      const b = await this.k.read();
      if (b === null) {
        if (this.avail === 0) {
          return null;
        }

        throw new ProtocolError("mimicry: a WebSocket record cut short");
      }

      this.chunks.push(b);
      this.avail += b.length;
    }

    const first = this.chunks[0];
    if (first !== undefined && first.length >= n) {
      if (first.length === n) {
        this.chunks.shift();
      } else {
        this.chunks[0] = first.subarray(n);
      }

      this.avail -= n;

      return first.subarray(0, n);
    }

    const out = Buffer.allocUnsafe(n);
    let off = 0;
    while (off < n) {
      const c = this.chunks[0] as Uint8Array;
      const k = Math.min(c.length, n - off);
      out.set(c.subarray(0, k), off);
      off += k;
      if (k === c.length) {
        this.chunks.shift();
      } else {
        this.chunks[0] = c.subarray(k);
      }
    }

    this.avail -= n;

    return out;
  }

  /**
   * The next message (Go's ReadMessage). Once the socket has closed it
   * throws why, every time: a CloseError after a close handshake.
   */
  readMessage(): Promise<Message> {
    const next = this.readLock.then(() => this.read());
    this.readLock = next.catch(() => undefined);

    return next;
  }

  private async read(): Promise<Message> {
    if (this.closedErr !== null) {
      throw this.closedErr;
    }

    try {
      const head = await this.readExact(5);
      if (head === null) {
        throw new ProtocolError("mimicry: the WebSocket's stream ended without its end record");
      }

      const typ = head[0] ?? 0;
      const n = Buffer.from(head.buffer, head.byteOffset, 5).readUInt32BE(1);
      if (n > maxRecord) {
        throw new ProtocolError(`mimicry: a ${n}-byte WebSocket record`);
      }

      const data = n === 0 ? new Uint8Array(0) : await this.readExact(n);
      if (data === null) {
        throw new ProtocolError("mimicry: a WebSocket record cut short");
      }

      if (typ === recordEnd) {
        throw errorFromPb(fromBinary(ErrorSchema, data));
      }

      return {
        type: typ as MessageType,
        data,
      };
    } catch (e) {
      this.closedErr = e instanceof Error ? e : new Error(String(e));

      throw this.closedErr;
    }
  }

  /**
   * Sends a message (Go's WriteMessage) and waits until the core has
   * written it, throwing its error. A string is sent as UTF-8.
   */
  writeMessage(type: MessageType, data: Uint8Array | string): Promise<void> {
    const payload = typeof data === "string" ? Buffer.from(data, "utf8") : data;
    const next = this.writeLock.then(() => this.write(type, payload));
    this.writeLock = next.catch(() => undefined);

    return next;
  }

  private async write(type: MessageType, payload: Uint8Array): Promise<void> {
    if (payload.length > maxRecord) {
      throw new RangeError(`mimicry: a ${payload.length}-byte WebSocket message, over ${maxRecord}`);
    }

    const head = Buffer.allocUnsafe(5);
    head[0] = type;
    head.writeUInt32BE(payload.length, 1);

    this.seq++;
    const { promise, resolve, reject } = Promise.withResolvers<void>();
    this.pending.writes.set(this.seq, { resolve, reject });

    try {
      await this.k.write(payload.length < 16 << 10 ? Buffer.concat([head, payload]) : head);
      if (payload.length >= 16 << 10) {
        await this.k.write(payload);
      }
    } catch (e) {
      this.pending.writes.delete(this.seq);

      throw e;
    }

    await this.core.track(promise);
  }

  /**
   * Closes the socket with code and reason (Go's Close), returning once
   * the close handshake is done, and frees it.
   */
  override async close(code = 1000, reason = ""): Promise<void> {
    try {
      await this.core.call({ case: "closeWebsocket", value: { websocket: this.id, code, reason } });
      await this.writeLock;
      await this.k.closeWrite().catch(() => undefined);
      // The core's end record and END follow the handshake: wait for
      // them, so a read after close returns why it closed.
      await this.core.track(this.k.stream.ended);
    } catch (e) {
      // A socket that had already closed has nothing to close.
      if (!(e instanceof MimicryError && e.code === "INVALID")) {
        throw e;
      }
    } finally {
      await super.close();
    }
  }
}

/** Dials a WebSocket through the client or transport id. */
export async function openWebSocket(core: Core, transport: bigint, url: string, options: WebSocketOptions): Promise<WebSocket> {
  const k = await core.open(
    {
      case: "dialWebsocket",
      value: {
        transport,
        url,
        options: {
          page: options.page === undefined ? "" : String(options.page),
          protocols: [...(options.protocols ?? [])],
          header: headerToPb(options.header),
        },
      },
    },
    { body: true },
  );

  // The signal bounds the handshake only, not the socket.
  const signal = options.signal;
  const onAbort = (): void => {
    k.close(signal?.reason);
  };
  signal?.addEventListener("abort", onAbort, { once: true });

  try {
    signal?.throwIfAborted();

    let r;
    try {
      r = await k.reply();
    } catch (e) {
      signal?.throwIfAborted();

      throw e;
    }

    if (r.error !== undefined) {
      const err = errorFromPb(r.error);
      if (r.result.case === "response") {
        err.response = new Response(r.result.value, k);
      } else {
        k.close();
      }

      throw err;
    }

    if (r.result.case !== "websocket") {
      k.close();

      throw new ProtocolError("mimicry: DialWebSocket's reply has no socket");
    }

    return new WebSocket(core, k, r.result.value);
  } catch (e) {
    if (!(e instanceof MimicryError && e.response !== null)) {
      k.close(e);
    }

    throw e;
  } finally {
    signal?.removeEventListener("abort", onAbort);
  }
}
