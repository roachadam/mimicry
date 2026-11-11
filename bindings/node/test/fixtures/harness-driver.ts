// The harness's Node driver (internal/harness/e2e, TestNodeMatchesGo*):
// sends a page's requests and WebSocket messages through this SDK as the
// Go test asks, so the server's records can be compared with the Go
// client's. One JSON command per line on stdin, one JSON answer per line
// on stdout, matched by id; commands run concurrently (a socket's reads
// wait while its writes go).
//
//   {"id":1,"op":"start","profile":<JSON>,"rootCAs":"<PEM>","dnsServer":"","proxy":"","browserProxyAuth":false}
//   {"id":2,"op":"send","method":"GET","url":"…","options":{…}|null,"header":[{"name":"…","values":["…"]}],
//    "headerOrder":[…],"body":"<base64>","stream":false,"exact":false}
//   {"id":3,"op":"dial","url":"ws://…","page":"…","protocols":[…]}      → {"ws":n}
//   {"id":4,"op":"write","ws":n,"binary":true,"data":"<base64>"}
//   {"id":5,"op":"read","ws":n}                                         → {"data":"<base64>"}
//   {"id":6,"op":"close","ws":n}
//
// Every answer is {"id":n} plus the result, or {"id":n,"error":"…"}.

import { createInterface } from "node:readline";

import {
  type Client,
  MessageType,
  type Options,
  type Request,
  type WebSocket,
  exact,
  newClient,
  parseProfile,
  withOptions,
} from "../../src/index.ts";

interface Command {
  id: number;
  op: string;
  [k: string]: unknown;
}

let client: Client | null = null;
const sockets = new Map<number, WebSocket>();

function reply(m: object): void {
  process.stdout.write(JSON.stringify(m) + "\n");
}

async function* chunks(b: Buffer): AsyncGenerator<Uint8Array> {
  for (let i = 0; i < b.length; i += 1000) {
    await Promise.resolve();
    yield b.subarray(i, i + 1000);
  }
}

function need(): Client {
  if (client === null) {
    throw new Error("no start");
  }

  return client;
}

async function run(c: Command): Promise<object> {
  switch (c.op) {
    case "start": {
      const profile = await parseProfile(JSON.stringify(c["profile"]));
      client = await newClient({
        profile,
        rootCAs: c["rootCAs"] as string,
        dnsServer: c["dnsServer"] as string,
        proxy: c["proxy"] as string,
        browserProxyAuth: c["browserProxyAuth"] as boolean,
      });

      return {};
    }

    case "send": {
      const header = (c["header"] as { name: string; values: string[] }[]).map((h) => [h.name, h.values] as const);
      const body = Buffer.from(c["body"] as string, "base64");
      let req: Request = {
        method: c["method"] as string,
        url: c["url"] as string,
        header: Object.fromEntries(header),
        headerOrder: c["headerOrder"] as string[],
      };
      if (c["stream"] === true) {
        req.body = chunks(body);
      } else if (body.length > 0) {
        req.body = body;
      }

      const options = c["options"] as Options | null;
      if (options !== null) {
        req = withOptions(req, options);
      }

      if (c["exact"] === true) {
        req = exact(req);
      }

      const r = await need().do(req);
      await r.bytes();

      return {};
    }

    case "dial": {
      const ws = await need().dialWebSocket(c["url"] as string, {
        page: c["page"] as string,
        protocols: c["protocols"] as string[],
      });
      sockets.set(c.id, ws);

      return { ws: c.id };
    }

    case "write": {
      const ws = sockets.get(c["ws"] as number);
      await ws?.writeMessage(c["binary"] === true ? MessageType.Binary : MessageType.Text, Buffer.from(c["data"] as string, "base64"));

      return {};
    }

    case "read": {
      const m = await sockets.get(c["ws"] as number)?.readMessage();

      return { data: Buffer.from(m?.data ?? []).toString("base64") };
    }

    case "close":
      await sockets.get(c["ws"] as number)?.close(1000, "");

      return {};
  }

  throw new Error(`unknown op ${c.op}`);
}

for await (const line of createInterface({ input: process.stdin })) {
  const c = JSON.parse(line) as Command;
  run(c).then(
    (r) => {
      reply({ id: c.id, ...r });
    },
    (e: unknown) => {
      reply({ id: c.id, error: e instanceof Error ? `${e.name}: ${e.message}` : String(e) });
    },
  );
}

await client?.close();
