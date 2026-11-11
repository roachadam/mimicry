// The SDK against a local server, through the real core: what the Python
// SDK's tests check (kinds, options, a 100 MB stream each way, abort,
// WebSockets, VerifyConnection, the core's death, saved state 0600), plus
// the errors' classes, large calls, cookies' notices and redirects.

import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, statSync } from "node:fs";
import type http from "node:http";
import type http2 from "node:http2";
import { tmpdir } from "node:os";
import { Writable } from "node:stream";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { create } from "@bufbuild/protobuf";

import { defaultCore } from "../src/core.ts";
import { errorFromPb } from "../src/errors.ts";
import { Code, ErrorSchema } from "../src/gen/mimicry/v1/mimicry_pb.js";
import {
  CORSError,
  CertRole,
  CloseError,
  CoreExited,
  Credentials,
  DeadlineError,
  Kind,
  MessageType,
  MimicryError,
  NetError,
  NotMeasuredError,
  ProxyError,
  ValidationError,
  VerifyError,
  asKind,
  exact,
  fetchPins,
  newClient,
  newJar,
  newTicketCache,
  newTransport,
  parseProfile,
  profileSpec,
  withOptions,
} from "../src/index.ts";
import { type TestServer, caPEM, profile, startServer } from "./helpers.ts";

type Req = http.IncomingMessage | http2.Http2ServerRequest;
type Res = http.ServerResponse | http2.Http2ServerResponse;

const MiB = 1 << 20;

// Routes is what the tests' server does, by path.
const routes = new Map<string, (req: Req, res: Res) => void>();
let server: TestServer;

before(async () => {
  server = await startServer((req, res) => {
    const path = new URL(req.url ?? "/", "http://x").pathname;
    const route = routes.get(path);
    if (route === undefined) {
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify({ path, headers: req.headers, raw: req.rawHeaders, method: req.method }));

      return;
    }

    route(req, res);
  });
});

after(async () => {
  await server.close();
});

// What a test opened, closed after it (Node 22 has no `await using`).
const opened: { close(): Promise<void> }[] = [];

function closing<T extends { close(): Promise<void> }>(o: T): T {
  opened.push(o);

  return o;
}

afterEach(async () => {
  for (const o of opened.splice(0).reverse()) {
    await o.close().catch(() => undefined);
  }
});

interface Echo {
  path: string;
  headers: Record<string, string>;
  raw: string[];
  method: string;
}

describe("requests", () => {
  test("each kind plans as itself, and goes out with its Sec-Fetch-Dest", async () => {
    const c = closing(await newClient({ profile, rootCAs: caPEM }));
    const page = `${server.tls}/page`;
    await (await c.do({ url: page })).text();

    const dests: Partial<Record<keyof typeof Kind, string>> = {
      Navigate: "document",
      Frame: "iframe",
      Script: "script",
      Style: "style",
      Image: "image",
      Font: "font",
      Fetch: "empty",
      XHR: "empty",
    };

    for (const [name, k] of Object.entries(Kind)) {
      if (k === Kind.Preflight) {
        continue;
      }

      const req = withOptions({ url: `${server.tls}/k/${name}`, method: k === Kind.Beacon ? "POST" : "GET" }, { kind: k, page });
      let plan;
      try {
        plan = await c.plan(req);
      } catch (e) {
        assert.ok(e instanceof NotMeasuredError, `${name}: ${String(e)}`);

        continue;
      }

      assert.equal(plan.kind, k, name);
      assert.ok(plan.text.length > 0);

      const want = dests[name as keyof typeof Kind];
      if (want !== undefined) {
        const echo = (await (await c.do(req)).json()) as Echo;
        assert.equal(echo.headers["sec-fetch-dest"], want, name);
      }
    }
  });

  test("asKind, withOptions, exact and headerOrder", async () => {
    const c = closing(await newClient({ profile }));
    const page = `${server.plain}/page`;
    await (await c.do({ url: page })).text();

    // asKind: from the page the client last navigated to.
    const fetched = (await (await c.do(asKind({ url: `${server.plain}/a` }, Kind.Fetch))).json()) as Echo;
    assert.equal(fetched.headers["sec-fetch-mode"], "cors");
    assert.equal(fetched.headers["referer"], page);

    // withOptions: credentials omitted sends no cookie and keeps the page.
    await c.cookies.setHostString("127.0.0.1", "a=1");
    const omit = (await (
      await c.do(withOptions({ url: `${server.plain}/b` }, { kind: Kind.Fetch, page, credentials: Credentials.Omit }))
    ).json()) as Echo;
    assert.equal(omit.headers["cookie"], undefined);
    const include = (await (await c.do(withOptions({ url: `${server.plain}/b` }, { kind: Kind.Fetch, page }))).json()) as Echo;
    assert.equal(include.headers["cookie"], "a=1");

    // headerOrder places the caller's own headers.
    const ordered = (await (
      await c.do({ url: `${server.plain}/c`, header: { "X-B": "2", "X-A": "1" }, headerOrder: ["x-a", "accept", "user-agent", "x-b"] })
    ).json()) as Echo;
    const names = ordered.raw.filter((_, i) => i % 2 === 0).map((n) => n.toLowerCase());
    assert.ok(names.indexOf("x-a") < names.indexOf("x-b"), names.join(","));

    // exact sends only the caller's headers, in its order.
    const ex = (await (
      await c.do(exact({ url: `${server.plain}/d`, header: { Accept: "*/*", "X-Exact": "1" }, headerOrder: ["x-exact", "accept"] }))
    ).json()) as Echo;
    const exNames = ex.raw.filter((_, i) => i % 2 === 0).map((n) => n.toLowerCase());
    assert.equal(ex.headers["user-agent"], undefined);
    assert.ok(exNames.indexOf("x-exact") < exNames.indexOf("accept"), exNames.join(","));
  });

  test("a header value's bytes cross both ways", async () => {
    routes.set("/latin1", (_req, res) => {
      res.setHeader("x-latin1", Buffer.from([0x63, 0x61, 0x66, 0xe9]).toString("latin1"));
      res.end();
    });
    const c = closing(await newClient({ profile }));
    const r = await c.do({ url: `${server.plain}/latin1` });
    await r.text();
    assert.equal(r.header.get("X-Latin1"), "caf\u00e9");
    await assert.rejects(c.do({ url: `${server.plain}/`, header: { "X-Euro": "\u20ac" } }), TypeError);
  });

  test("redirects: followed, or returned", async () => {
    routes.set("/redirect", (_req, res) => {
      res.statusCode = 302;
      res.setHeader("location", "/landed");
      res.end();
    });
    const c = closing(await newClient({ profile, rootCAs: caPEM }));
    const followed = await c.do({ url: `${server.tls}/redirect` });
    assert.equal(followed.url, `${server.tls}/landed`);
    await followed.text();

    const kept = closing(await newClient({ profile, rootCAs: caPEM, redirects: { follow: false } }));
    const r = await kept.do({ url: `${server.tls}/redirect` });
    assert.equal(r.statusCode, 302);
    await r.text();

    const t = closing(await newTransport({ profile, rootCAs: caPEM }));
    const hop = await t.roundTrip({ url: `${server.tls}/redirect` });
    assert.equal(hop.statusCode, 302);
    await hop.text();
  });

  test("64 requests at once over HTTP/2", async () => {
    const c = closing(await newClient({ profile, rootCAs: caPEM }));
    const rs = await Promise.all(Array.from({ length: 64 }, (_, i) => c.do({ url: `${server.tls}/n/${i}` })));
    for (const [i, r] of rs.entries()) {
      assert.equal(r.proto, "HTTP/2.0");
      assert.equal(((await r.json()) as Echo).path, `/n/${i}`);
    }
  });

  test("calls and bodies larger than a frame", async () => {
    routes.set("/count", (req, res) => {
      let n = 0;
      req.on("data", (b: Buffer) => {
        n += b.length;
      });
      req.on("end", () => {
        res.end(String(n));
      });
    });

    // 2 MB of roots makes NewClient a continued call.
    const roots = caPEM.repeat(Math.ceil((2 * MiB) / caPEM.length));
    const c = closing(await newClient({ profile, rootCAs: roots }));
    const body = Buffer.alloc(3 * MiB, 7);
    const r = await c.do(withOptions({ method: "POST", url: `${server.tls}/count`, body }, { kind: Kind.Fetch, page: `${server.tls}/` }));
    assert.equal(await r.text(), String(body.length));
  });
});

describe("streams", () => {
  test("a 100 MB response streams with bounded memory, held back while unread", async () => {
    const total = 100 * MiB;
    let sent = 0;
    routes.set("/download", (_req, res) => {
      const chunk = Buffer.alloc(64 << 10, 1);
      const w = res as http.ServerResponse;
      const pump = (): void => {
        while (sent < total) {
          sent += chunk.length;
          if (!w.write(chunk)) {
            res.once("drain", pump);

            return;
          }
        }

        res.end();
      };
      pump();
    });

    const c = closing(await newClient({ profile }));
    const r = await c.do({ url: `${server.plain}/download` });
    const reader = r.body.getReader();
    const first = await reader.read();
    assert.ok(first.value !== undefined && first.value.length > 0);

    // Unread, the body holds the server back: the protocol's credit plus
    // the sockets' buffers, nowhere near 100 MB. Real time on purpose: the
    // check is that the server makes no progress while nothing reads.
    await new Promise((resolve) => setTimeout(resolve, 500));
    const heldAt = sent;
    assert.ok(heldAt < 48 * MiB, `the server sent ${heldAt} bytes to an unread body`);

    let got = first.value.length;
    let peak = 0;
    const base = process.memoryUsage().arrayBuffers;
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }

      got += value.length;
      peak = Math.max(peak, process.memoryUsage().arrayBuffers - base);
    }

    assert.equal(got, total);
    assert.ok(peak < 64 * MiB, `${peak} bytes of buffers while reading`);
  });

  test("a 100 MB request body streams", async () => {
    const total = 100 * MiB;
    let produced = 0;
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (produced >= total) {
          controller.close();

          return;
        }

        const n = Math.min(256 << 10, total - produced);
        produced += n;
        controller.enqueue(new Uint8Array(n).fill(3));
      },
    });

    const c = closing(await newClient({ profile }));
    const r = await c.do(withOptions({ method: "POST", url: `${server.plain}/count`, body }, { kind: Kind.Fetch, page: `${server.plain}/` }));
    assert.equal(await r.text(), String(total));
  });

  test("an abort before the response resets the request; the server sees it go", async () => {
    const closed = Promise.withResolvers<void>();
    const arrived = Promise.withResolvers<void>();
    routes.set("/hang", (_req, res) => {
      res.on("close", () => {
        closed.resolve();
      });
      arrived.resolve();
    });

    const c = closing(await newClient({ profile }));
    const ac = new AbortController();
    const p = c.do({ url: `${server.plain}/hang`, signal: ac.signal });
    await arrived.promise;
    ac.abort();
    await assert.rejects(p, { name: "AbortError" });
    await closed.promise;
  });

  test("an abort while the body is read fails the read", async () => {
    const closed = Promise.withResolvers<void>();
    routes.set("/slow", (_req, res) => {
      res.on("close", () => {
        closed.resolve();
      });
      (res as http.ServerResponse).write("first");
    });

    const c = closing(await newClient({ profile }));
    const ac = new AbortController();
    const r = await c.do({ url: `${server.plain}/slow`, signal: ac.signal });
    const reader = r.body.getReader();
    assert.equal(Buffer.from((await reader.read()).value ?? []).toString(), "first");
    const next = reader.read();
    ac.abort();
    await assert.rejects(next, { name: "AbortError" });
    await closed.promise;
  });

  test("AbortSignal.timeout is a timeout", async () => {
    routes.set("/never", () => undefined);
    const c = closing(await newClient({ profile }));
    await assert.rejects(c.do({ url: `${server.plain}/never`, signal: AbortSignal.timeout(200) }), { name: "TimeoutError" });
  });
});

describe("WebSockets", () => {
  test("text and binary echo, then close(1000) and a CloseError", async () => {
    const c = closing(await newClient({ profile }));
    await (await c.do({ url: `${server.plain}/` })).text();
    const ws = await c.dialWebSocket(`${server.ws}/echo`, { page: `${server.plain}/` });

    await ws.writeMessage(MessageType.Text, "hello");
    await ws.writeMessage(MessageType.Binary, new Uint8Array([0, 1, 2, 255]));
    const big = Buffer.alloc(300 << 10, 9);
    await ws.writeMessage(MessageType.Binary, big);

    const m1 = await ws.readMessage();
    assert.equal(m1.type, MessageType.Text);
    assert.equal(Buffer.from(m1.data).toString(), "hello");
    const m2 = await ws.readMessage();
    assert.equal(m2.type, MessageType.Binary);
    assert.deepEqual([...m2.data], [0, 1, 2, 255]);
    const m3 = await ws.readMessage();
    assert.ok(Buffer.from(m3.data).equals(big));

    await ws.close(1000, "");
    await assert.rejects(ws.readMessage(), (e: unknown) => e instanceof CloseError && e.closeCode === 1000);
  });

  test("a refused handshake throws with the server's response", async () => {
    const c = closing(await newClient({ profile }));
    await (await c.do({ url: `${server.plain}/` })).text();
    await assert.rejects(c.dialWebSocket(`${server.ws}/refuse`, { page: `${server.plain}/` }), (e: unknown) => {
      assert.ok(e instanceof MimicryError, String(e));
      assert.equal(e.response?.statusCode, 403);

      return true;
    });
  });
});

describe("callbacks", () => {
  test("verifyConnection refuses one host and accepts another", async () => {
    const seen: string[] = [];
    const c = closing(await newClient({
      profile,
      rootCAs: caPEM,
      disableCoalescing: true,
      verifyConnection: async (state) => {
        // An answer that comes after a turn of the event loop.
        await new Promise((resolve) => setImmediate(resolve));
        seen.push(state.serverName);
        assert.match(state.peerCertificates[0]?.subject ?? "", /CN=localhost/);
        if (state.serverName === "localhost") {
          throw new Error("not this one");
        }
      },
    }));

    await assert.rejects(c.do({ url: `${server.tlsLocalhost}/` }), (e: unknown) => e instanceof VerifyError && e.message.includes("not this one"));
    const r = await c.do({ url: `${server.tls}/` });
    assert.equal(r.statusCode, 200);
    await r.text();
    assert.deepEqual(seen, ["localhost", "127.0.0.1"]);
  });

  test("fetchPins returns the chain and, as Go's, ignores verifyConnection", async () => {
    let called = 0;
    const pins = await fetchPins(
      {
        profile,
        rootCAs: caPEM,
        verifyConnection: () => {
          called++;
          throw new Error("never asked");
        },
      },
      server.tls,
    );
    assert.equal(called, 0);
    assert.deepEqual(
      pins.map((p) => p.role),
      [CertRole.Leaf, CertRole.Root],
    );
    assert.match(pins[0]?.subject ?? "", /localhost/);
  });

  test("a jar's onSetCookie hears each Set-Cookie line", async () => {
    routes.set("/set", (_req, res) => {
      res.setHeader("set-cookie", ["a=1; Path=/", "b=2; Path=/; Max-Age=0"]);
      res.end();
    });
    const events: string[] = [];
    const heard = Promise.withResolvers<void>();
    const jar = closing(await newJar({
      rules: profile,
      onSetCookie: (e) => {
        events.push(e.line);
        if (events.length === 2) {
          heard.resolve();
        }
      },
    }));
    const c = closing(await newClient({ profile, cookies: jar }));
    assert.equal(c.cookies, jar);
    await (await c.do({ url: `${server.plain}/set` })).text();
    await heard.promise;
    assert.deepEqual(events, ["a=1; Path=/", "b=2; Path=/; Max-Age=0"]);
    assert.deepEqual(
      (await jar.all()).map((k) => `${k.name}=${k.value}`),
      ["a=1"],
    );
  });
});

describe("errors", () => {
  test("instanceof follows the error's matches", () => {
    const e = errorFromPb(
      create(ErrorSchema, {
        code: Code.PROXY,
        matches: [Code.PROXY, Code.DEADLINE, Code.NET],
        message: "proxy: timeout",
        proxy: { proxy: "http://p:1", statusCode: 0 },
      }),
    );
    assert.ok(e instanceof ProxyError && e instanceof DeadlineError && e instanceof NetError && e instanceof MimicryError);
    assert.ok(!(e instanceof CORSError));
    assert.equal(e.code, "PROXY");
    assert.ok(e instanceof ProxyError && e.proxy === "http://p:1");
  });

  test("a refused connection is a NetError, a blocked cross-origin fetch a CORSError", async () => {
    const c = closing(await newClient({ profile, rootCAs: caPEM }));
    await assert.rejects(c.do({ url: "http://127.0.0.1:1/" }), (e: unknown) => e instanceof NetError);

    // A preflight the server does not allow.
    const page = `${server.tlsLocalhost}/page`;
    await (await c.do({ url: page })).text();
    await assert.rejects(
      c.do(withOptions({ method: "PUT", url: `${server.tls}/api`, header: { "X-Custom": "1" } }, { kind: Kind.Fetch, page })),
      (e: unknown) => e instanceof CORSError && e.url !== "",
    );
  });

  test("profiles round-trip as JSON, and an invalid one is a ValidationError", async () => {
    const json = await profileSpec(profile);
    const p = await parseProfile(json);
    assert.equal(p.name, profile);
    const c = closing(await newClient({ profile: p }));
    await (await c.do({ url: `${server.plain}/` })).text();

    const spec = JSON.parse(json) as Record<string, unknown>;
    spec["name"] = "Not A Name";
    await assert.rejects(parseProfile(JSON.stringify(spec)), (e: unknown) => e instanceof ValidationError && e.problems.length > 0);
  });
});

describe("saved state", () => {
  const dir = mkdtempSync(join(tmpdir(), "mimicry-node-"));
  after(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  test("writeJSON and writeNetscape write files 0600 that read back", async () => {
    const jar = closing(await newJar({ rules: profile }));
    await jar.setHostString("example.com", "a=1; b=2");
    const json = join(dir, "cookies.json");
    const txt = join(dir, "cookies.txt");
    await jar.writeJSON(json);
    await jar.writeNetscape(txt);

    const tickets = closing(await newTicketCache());
    const tj = join(dir, "tickets.json");
    await tickets.writeJSON(tj);

    if (process.platform !== "win32") {
      for (const f of [json, txt, tj]) {
        assert.equal(statSync(f).mode & 0o777, 0o600, f);
      }
    }

    const back = closing(await newJar({ rules: profile }));
    await back.readJSON(json);
    assert.deepEqual(
      (await back.all()).map((k) => `${k.name}=${k.value}`).sort(),
      ["a=1", "b=2"],
    );

    const fromTxt = closing(await newJar({ rules: profile }));
    await fromTxt.readNetscape(readFileSync(txt));
    assert.equal((await fromTxt.all()).length, 2);

    await tickets.readJSON(tj);
  });
});

describe("saved state to a stream", () => {
  test("a destination that fails while full rejects writeJSON instead of hanging", { timeout: 10_000 }, async () => {
    const jar = closing(await newJar({ rules: profile }));
    await jar.setHostString("example.com", "a=1");
    // Full after any write, and failing it: 'error', never 'drain'.
    const dest = new Writable({
      highWaterMark: 1,
      write(_chunk, _encoding, callback) {
        callback(new Error("disk full"));
      },
    });
    dest.on("error", () => undefined);
    await assert.rejects(jar.writeJSON(dest), /disk full|closed/);
  });
});

describe("the core", () => {
  test("a core killed mid-request is CoreExited, and the next call starts another", async () => {
    const arrived = Promise.withResolvers<void>();
    routes.set("/doomed", () => {
      arrived.resolve();
    });

    const c = await newClient({ profile });
    const core = await defaultCore();
    const p = c.do({ url: `${server.plain}/doomed` });
    await arrived.promise;
    core.child.kill("SIGKILL");
    await assert.rejects(p, (e: unknown) => {
      assert.ok(e instanceof CoreExited, String(e));
      if (process.platform !== "win32") {
        assert.equal(e.signal, "SIGKILL");
      }

      return true;
    });
    await assert.rejects(c.do({ url: `${server.plain}/` }), CoreExited);

    const again = closing(await newClient({ profile }));
    assert.notEqual(await defaultCore(), core);
    await (await again.do({ url: `${server.plain}/` })).text();
  });
});
