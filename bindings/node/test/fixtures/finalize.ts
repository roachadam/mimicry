// Run with --expose-gc: transports nobody holds are freed in the core
// once garbage collected. Prints how many were.

import { Core } from "../../src/core.ts";
import { newTransport } from "../../src/index.ts";

const n = 20;
let freed = 0;
// eslint-disable-next-line @typescript-eslint/unbound-method -- called with its core below
const { free } = Core.prototype;
Core.prototype.free = function (this: Core, id: bigint) {
  freed++;

  return free.call(this, id);
};

for (let i = 0; i < n; i++) {
  await newTransport({ profile: "chrome-157-macos" });
}

const gc = (globalThis as { gc?: () => void }).gc;
if (gc === undefined) {
  throw new Error("run with --expose-gc");
}

// FinalizationRegistry callbacks run in later tasks after a collection.
for (let i = 0; i < 100 && freed < n; i++) {
  gc();
  await new Promise((resolve) => setTimeout(resolve, 10));
}

console.log(freed);
