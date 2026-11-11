// Makes a request through the SDK and leaves everything open: Node must
// still exit, the core being unref'd while idle. Prints the core's pid.

import { defaultCore } from "../../src/core.ts";
import { newClient } from "../../src/index.ts";

const c = await newClient({ profile: "chrome-157-macos" });
const r = await c.do({ url: process.argv[2] ?? "" });
await r.text();
const jar = c.cookies;
await jar.setHostString("example.com", "a=1");
console.log((await defaultCore()).child.pid);
