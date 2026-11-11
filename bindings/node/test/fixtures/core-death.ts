// Awaits one request and nothing else, so only the SDK keeps Node alive;
// prints the core's pid, then how the request ended once the test has
// killed the core.

import { defaultCore } from "../../src/core.ts";
import { newClient } from "../../src/index.ts";

const c = await newClient({ profile: "chrome-157-macos" });
console.log((await defaultCore()).child.pid);

try {
  await c.do({ url: process.argv[2] ?? "" });
  console.log("answered");
} catch (e) {
  console.log(e instanceof Error ? e.name : String(e));
}
