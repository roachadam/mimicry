// Run with --expose-gc: a request on a client nothing else references,
// collected (or not) while the server holds the request, which then
// redirects to another host, a new connection verifyConnection must
// accept. Prints the request's outcome and the hosts verified as JSON.

import { newClient } from "../../src/index.ts";
import { caPEM, startServer } from "../helpers.ts";

const gc = (globalThis as { gc?: () => void }).gc;
if (gc === undefined) {
  throw new Error("run with --expose-gc");
}

const release = Promise.withResolvers<void>();
const arrived = Promise.withResolvers<void>();
const server = await startServer((req, res) => {
  if (req.url === "/slow") {
    arrived.resolve();
    void release.promise.then(() => {
      res.statusCode = 302;
      res.setHeader("location", `${server.tlsLocalhost}/landed`);
      res.end();
    });

    return;
  }

  res.end("landed");
});

const verified: string[] = [];
const seen = { collected: false };
const watch = new FinalizationRegistry(() => {
  seen.collected = true;
});

// Returns the request in a box, so this function's frame (and the client
// in it) is gone once it has: nothing holds the client while it waits.
async function begin(): Promise<{ p: Promise<{ text(): Promise<string> }> }> {
  const c = await newClient({
    profile: "chrome-157-macos",
    rootCAs: caPEM,
    disableCoalescing: true,
    verifyConnection: (s) => {
      verified.push(s.serverName);
    },
  });
  watch.register(c, null);

  return { p: c.do({ url: `${server.tls}/slow` }) };
}

async function start(): Promise<string> {
  const { p } = await begin();

  return (await p).text();
}

const result = start().then(
  (body) => `ok ${body}`,
  (e: unknown) => (e instanceof Error ? `${e.name}: ${e.message}` : String(e)),
);
await arrived.promise;

// Collect while the request waits, giving finalizers turns to run.
for (let i = 0; i < 20 && !seen.collected; i++) {
  gc();
  await new Promise((resolve) => setImmediate(resolve));
}

release.resolve();
console.log(JSON.stringify({ result: await result, verified, collected: seen.collected }));
await server.close();
