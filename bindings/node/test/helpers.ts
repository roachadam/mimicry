// What the tests share: the core they run (MIMICRY_CORE, else the one
// npm test builds into .core/), a local server over HTTP/1.1 and HTTP/2
// with TLS (testdata's certificate, signed by testdata/ca.pem) and over
// plain HTTP, and a WebSocket echo server.

import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import http from "node:http";
import http2 from "node:http2";
import type { AddressInfo } from "node:net";
import type { Duplex } from "node:stream";
import { join } from "node:path";

const root = join(import.meta.dirname, "..");

if (process.env["MIMICRY_CORE"] === undefined) {
  const built = join(root, ".core", process.platform === "win32" ? "mimicry-core.exe" : "mimicry-core");
  if (!existsSync(built)) {
    throw new Error(`no mimicry-core at ${built}: run npm test (or node scripts/build-core.ts), or set MIMICRY_CORE`);
  }

  process.env["MIMICRY_CORE"] = built;
}

const testdata = join(import.meta.dirname, "testdata");
export const caPEM = readFileSync(join(testdata, "ca.pem"), "utf8");
const cert = readFileSync(join(testdata, "cert.pem"));
const key = readFileSync(join(testdata, "key.pem"));

/** The profile the tests use. */
export const profile = "chrome-157-macos";

export type Handler = (req: http.IncomingMessage | http2.Http2ServerRequest, res: http.ServerResponse | http2.Http2ServerResponse) => void;

/** A server over TLS (HTTP/2, else HTTP/1.1) and plain HTTP, both on 127.0.0.1. */
export interface TestServer {
  /** https://127.0.0.1:<port> */
  tls: string;
  /** https://localhost:<port>, the same server under another name. */
  tlsLocalhost: string;
  /** http://127.0.0.1:<port> */
  plain: string;
  /** ws://127.0.0.1:<port>, echoing every message. */
  ws: string;
  close(): Promise<void>;
}

async function listen(s: http.Server | http2.Http2SecureServer): Promise<number> {
  const { promise, resolve } = Promise.withResolvers<void>();
  s.listen(0, "127.0.0.1", resolve);
  await promise;

  return (s.address() as AddressInfo).port;
}

/** Starts the servers with one handler for both. */
export async function startServer(handler: Handler): Promise<TestServer> {
  const secure = http2.createSecureServer({ cert, key, allowHTTP1: true }, handler);
  const plain = http.createServer(handler);
  plain.on("upgrade", (req, socket) => {
    echoWebSocket(req, socket);
  });

  const sockets = new Set<Duplex>();
  for (const s of [secure, plain] as const) {
    s.on("connection", (c: Duplex) => {
      sockets.add(c);
      c.on("close", () => sockets.delete(c));
    });
  }

  const tlsPort = await listen(secure);
  const plainPort = await listen(plain);

  return {
    tls: `https://127.0.0.1:${tlsPort}`,
    tlsLocalhost: `https://localhost:${tlsPort}`,
    plain: `http://127.0.0.1:${plainPort}`,
    ws: `ws://127.0.0.1:${plainPort}`,
    async close() {
      for (const c of sockets) {
        c.destroy();
      }

      await Promise.all(
        [secure, plain].map(async (s) => {
          const { promise, resolve } = Promise.withResolvers<void>();
          s.close(() => {
            resolve();
          });
          await promise;
        }),
      );
    },
  };
}

// echoWebSocket answers an Upgrade and echoes each message (RFC 6455, no
// extensions), answering a close with a close.
function echoWebSocket(req: http.IncomingMessage, socket: Duplex): void {
  const key = req.headers["sec-websocket-key"];
  if (typeof key !== "string") {
    socket.destroy();

    return;
  }

  if (req.url === "/refuse") {
    socket.end("HTTP/1.1 403 Forbidden\r\nContent-Length: 4\r\n\r\nnope");

    return;
  }

  const accept = createHash("sha1").update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest("base64");
  const protocol = req.headers["sec-websocket-protocol"]?.split(",")[0]?.trim();
  socket.write(
    `HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n` +
      (protocol === undefined ? "" : `Sec-WebSocket-Protocol: ${protocol}\r\n`) +
      "\r\n",
  );

  let buf = Buffer.alloc(0);
  let message: Buffer[] = [];
  let opcode = 0;

  const send = (op: number, payload: Buffer): void => {
    let head: Buffer;
    if (payload.length < 126) {
      head = Buffer.from([0x80 | op, payload.length]);
    } else if (payload.length < 65536) {
      head = Buffer.alloc(4);
      head[0] = 0x80 | op;
      head[1] = 126;
      head.writeUInt16BE(payload.length, 2);
    } else {
      head = Buffer.alloc(10);
      head[0] = 0x80 | op;
      head[1] = 127;
      head.writeBigUInt64BE(BigInt(payload.length), 2);
    }

    socket.write(Buffer.concat([head, payload]));
  };

  socket.on("data", (chunk: Buffer) => {
    buf = Buffer.concat([buf, chunk]);
    for (;;) {
      if (buf.length < 2) {
        return;
      }

      const b0 = buf[0] as number;
      const b1 = buf[1] as number;
      let len = b1 & 0x7f;
      let off = 2;
      if (len === 126) {
        if (buf.length < 4) {
          return;
        }

        len = buf.readUInt16BE(2);
        off = 4;
      } else if (len === 127) {
        if (buf.length < 10) {
          return;
        }

        len = Number(buf.readBigUInt64BE(2));
        off = 10;
      }

      const masked = (b1 & 0x80) !== 0;
      const mask = masked ? buf.subarray(off, off + 4) : null;
      off += masked ? 4 : 0;
      if (buf.length < off + len) {
        return;
      }

      const payload = Buffer.from(buf.subarray(off, off + len));
      buf = buf.subarray(off + len);
      if (mask !== null) {
        for (let i = 0; i < payload.length; i++) {
          payload[i] = (payload[i] as number) ^ (mask[i % 4] as number);
        }
      }

      const op = b0 & 0x0f;
      if (op === 8) {
        send(8, payload.subarray(0, 2));
        socket.end();

        return;
      }

      if (op === 9) {
        send(10, payload);

        continue;
      }

      if (op === 1 || op === 2) {
        opcode = op;
        message = [];
      }

      if (op <= 2) {
        message.push(payload);
        if ((b0 & 0x80) !== 0) {
          send(opcode, Buffer.concat(message));
        }
      }
    }
  });
  socket.on("error", () => undefined);
}
