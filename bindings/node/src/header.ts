// Header is Go's http.Header: names in canonical form, each with its
// values in order. Values cross to the core as bytes: a value is a
// ByteString (each character U+0000 to U+00FF, one byte each), as
// WHATWG's Headers takes it, so bytes that are not UTF-8 survive both ways.

import { create } from "@bufbuild/protobuf";
import { type HeaderValues, HeaderValuesSchema } from "./gen/mimicry/v1/mimicry_pb.js";

/** What a Header is made from: a Header, WHATWG Headers, a record or name-value pairs. */
export type HeaderInit =
  | Header
  | Headers
  | Readonly<Record<string, string | readonly string[]>>
  | Iterable<readonly [string, string]>;

const token = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;

/**
 * Go's CanonicalMIMEHeaderKey: the first letter and each letter after a
 * hyphen upper case, the rest lower case ("accept-encoding" is
 * "Accept-Encoding"); a name that is not a token is kept as it is.
 */
export function canonicalHeaderName(name: string): string {
  if (!token.test(name)) {
    return name;
  }

  return name.toLowerCase().replace(/(^|-)([a-z])/g, (_m, sep: string, c: string) => sep + c.toUpperCase());
}

/** Go's http.Header: canonical names, each with its values in order. */
export class Header implements Iterable<[string, string[]]> {
  private readonly m = new Map<string, string[]>();

  constructor(init?: HeaderInit) {
    if (init === undefined) {
      return;
    }

    if (init instanceof Header || init instanceof Headers) {
      for (const [name, value] of init instanceof Header ? init.pairs() : init) {
        this.add(name, value);
      }

      return;
    }

    if (Symbol.iterator in init) {
      for (const [name, value] of init as Iterable<readonly [string, string]>) {
        this.add(name, value);
      }

      return;
    }

    for (const [name, v] of Object.entries(init)) {
      if (typeof v === "string") {
        this.add(name, v);
      } else {
        // An empty list is kept: it removes a default where Go's does.
        this.m.set(canonicalHeaderName(name), [...v]);
      }
    }
  }

  /** The first value of name, or undefined. */
  get(name: string): string | undefined {
    return this.m.get(canonicalHeaderName(name))?.[0];
  }

  /** Every value of name. */
  values(name: string): string[] {
    return [...(this.m.get(canonicalHeaderName(name)) ?? [])];
  }

  has(name: string): boolean {
    return this.m.has(canonicalHeaderName(name));
  }

  /** Replaces name's values with value. */
  set(name: string, value: string): void {
    this.m.set(canonicalHeaderName(name), [value]);
  }

  /** Appends value to name's values. */
  add(name: string, value: string): void {
    const key = canonicalHeaderName(name);
    const vs = this.m.get(key);
    if (vs === undefined) {
      this.m.set(key, [value]);
    } else {
      vs.push(value);
    }
  }

  delete(name: string): void {
    this.m.delete(canonicalHeaderName(name));
  }

  /** The names, in the order they were added. */
  keys(): IterableIterator<string> {
    return this.m.keys();
  }

  /** Each name and a pair per value. */
  *pairs(): IterableIterator<[string, string]> {
    for (const [name, vs] of this.m) {
      for (const v of vs) {
        yield [name, v];
      }
    }
  }

  [Symbol.iterator](): IterableIterator<[string, string[]]> {
    return this.m.entries();
  }

  toJSON(): Record<string, string[]> {
    return Object.fromEntries(this.m);
  }
}

/** A ByteString as its bytes; a character above U+00FF is refused. */
export function byteString(s: string, what: string): Uint8Array {
  for (let i = 0; i < s.length; i++) {
    if (s.charCodeAt(i) > 0xff) {
      throw new TypeError(`mimicry: ${what} has a character above U+00FF at ${i}; header values are bytes (latin1)`);
    }
  }

  return Buffer.from(s, "latin1");
}

/** A header as the core takes it. */
export function headerToPb(init: HeaderInit | undefined): HeaderValues[] {
  if (init === undefined) {
    return [];
  }

  const h = init instanceof Header ? init : new Header(init);

  return [...h].map(([name, values]) =>
    create(HeaderValuesSchema, {
      name,
      values: values.map((v) => byteString(v, `header ${name}'s value`)),
    }),
  );
}

/** A header the core sent. */
export function headerFromPb(hv: readonly HeaderValues[]): Header {
  const h = new Header();
  for (const { name, values } of hv) {
    for (const v of values) {
      h.add(name, Buffer.from(v).toString("latin1"));
    }
  }

  return h;
}
