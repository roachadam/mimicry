// State the core holds and clients share: the cookie jar, the ticket
// cache, the Alt-Svc cache and the client hint store, as in Go, with
// Go's serializations to save and load them. A file this SDK writes is
// created 0600 (cookies and tickets are credentials) and replaced whole.

import { randomBytes } from "node:crypto";
import { createReadStream } from "node:fs";
import { chmod, open, rename, rm } from "node:fs/promises";
import { fileURLToPath } from "node:url";

import { create } from "@bufbuild/protobuf";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";

import { type Core, CoreObject, type Method, defaultCore } from "./core.ts";
import { ProtocolError, errorFromPb } from "./errors.ts";
import {
  type Cookie as PbCookie,
  type CookieRequest as PbCookieRequest,
  CookieRequestSchema,
  CookieSchema,
  type SameSite as PbSameSite,
  type SetCookie,
} from "./gen/mimicry/v1/mimicry_pb.js";
import { byteString } from "./header.ts";
import { type Profile, profileRef } from "./profiles.ts";

/** Where saved state goes: a file path (created 0600), or a writable stream. */
export type StateDestination = string | URL | NodeJS.WritableStream;
/** Where saved state comes from: a file path, its bytes, or a readable stream. */
export type StateSource = string | URL | Uint8Array | AsyncIterable<Uint8Array | string>;

function path(p: string | URL): string {
  return p instanceof URL ? fileURLToPath(p) : p;
}

// writeState makes a call whose reply's DATA is the state and sends it to
// dest; a file is written beside dest, 0600, then renamed over it.
async function writeState(core: Core, method: Method, dest: StateDestination): Promise<void> {
  const k = await core.open(method);
  try {
    const r = await k.reply();
    if (r.error !== undefined) {
      throw errorFromPb(r.error);
    }

    if (typeof dest === "string" || dest instanceof URL) {
      const final = path(dest);
      const tmp = `${final}.${randomBytes(6).toString("hex")}.tmp`;
      const fh = await open(tmp, "wx", 0o600);
      try {
        for (let b = await k.read(); b !== null; b = await k.read()) {
          await fh.write(b);
        }

        await fh.close();
        // The mode open sets is masked by the umask; this one is not.
        await chmod(tmp, 0o600);
        await rename(tmp, final);
      } catch (e) {
        await fh.close().catch(() => undefined);
        await rm(tmp, { force: true });

        throw e;
      }

      return;
    }

    for (let b = await k.read(); b !== null; b = await k.read()) {
      if (!dest.write(b)) {
        const { promise, resolve } = Promise.withResolvers<void>();
        dest.once("drain", resolve);
        await promise;
      }
    }
  } finally {
    k.close();
  }
}

// readState makes a call that takes the state as DATA after it.
async function readState(core: Core, method: Method, src: StateSource): Promise<void> {
  const k = await core.open(method, { body: true });
  try {
    if (src instanceof Uint8Array) {
      await k.write(src);
    } else {
      const chunks = typeof src === "string" || src instanceof URL ? createReadStream(path(src)) : src;
      for await (const c of chunks as AsyncIterable<Uint8Array | string>) {
        await k.write(typeof c === "string" ? Buffer.from(c, "utf8") : c);
      }
    }

    await k.closeWrite();

    const r = await k.reply();
    if (r.error !== undefined) {
      throw errorFromPb(r.error);
    }
  } catch (e) {
    k.close(e);

    throw e;
  } finally {
    k.close();
  }
}

/** State saved and loaded with Go's JSON (WriteJSON, ReadJSON). */
class Saved extends CoreObject {
  /** Saves the state as Go's WriteJSON writes it: to a file (0600) or a stream. */
  writeJSON(dest: StateDestination): Promise<void> {
    return writeState(this.core, { case: "writeJson", value: { id: this.id } }, dest);
  }

  /** Loads state Go's WriteJSON (or writeJSON) wrote. */
  readJSON(src: StateSource): Promise<void> {
    return readState(this.core, { case: "readJson", value: { id: this.id } }, src);
  }
}

/** cookies.SameSite. */
export const SameSite = {
  Unset: 0,
  Lax: 1,
  Strict: 2,
  None: 3,
} as const;
export type SameSite = (typeof SameSite)[keyof typeof SameSite];

/** cookies.Cookie. Names, values and raw lines are ByteStrings (one byte per character). */
export interface Cookie {
  name: string;
  value: string;
  domain: string;
  hostOnly: boolean;
  path: string;
  expires: Date | null;
  secure: boolean;
  httpOnly: boolean;
  sameSite: SameSite;
  partitioned: boolean;
  partition: string;
  created: Date | null;
  lastAccess: Date | null;
  raw: string;
}

/** cookies.Request: a request as the jar sees it. */
export interface CookieRequest {
  url: string | URL;
  method?: string;
  /** A top-level navigation: the request becomes the page. */
  navigation?: boolean;
  /** The page the request is made from. */
  page?: string | URL;
  redirects?: readonly (string | URL)[];
  websocket?: boolean;
}

/** cookies.Event: one Set-Cookie line the jar received. */
export interface SetCookieEvent {
  url: string;
  line: string;
  /** The cookie stored; null when rejected says why not, or the line deleted one. */
  cookie: Cookie | null;
  rejected: string;
  deleted: boolean;
}

function latin1(b: Uint8Array): string {
  return Buffer.from(b).toString("latin1");
}

function cookieFromPb(c: PbCookie): Cookie {
  return {
    name: latin1(c.name),
    value: latin1(c.value),
    domain: c.domain,
    hostOnly: c.hostOnly,
    path: c.path,
    expires: c.expires === undefined ? null : timestampDate(c.expires),
    secure: c.secure,
    httpOnly: c.httpOnly,
    sameSite: c.sameSite,
    partitioned: c.partitioned,
    partition: c.partition,
    created: c.created === undefined ? null : timestampDate(c.created),
    lastAccess: c.lastAccess === undefined ? null : timestampDate(c.lastAccess),
    raw: latin1(c.raw),
  };
}

function cookieToPb(c: Partial<Cookie> & Pick<Cookie, "name" | "value">): PbCookie {
  return create(CookieSchema, {
    name: byteString(c.name, "a cookie's name"),
    value: byteString(c.value, "a cookie's value"),
    domain: c.domain ?? "",
    hostOnly: c.hostOnly ?? false,
    path: c.path ?? "",
    expires: c.expires == null ? undefined : timestampFromDate(c.expires),
    secure: c.secure ?? false,
    httpOnly: c.httpOnly ?? false,
    sameSite: (c.sameSite ?? 0) as PbSameSite,
    partitioned: c.partitioned ?? false,
    partition: c.partition ?? "",
    created: c.created == null ? undefined : timestampFromDate(c.created),
    lastAccess: c.lastAccess == null ? undefined : timestampFromDate(c.lastAccess),
    raw: byteString(c.raw ?? "", "a cookie's raw line"),
  });
}

function cookieRequestToPb(r: CookieRequest): PbCookieRequest {
  return create(CookieRequestSchema, {
    url: String(r.url),
    method: r.method ?? "",
    navigation: r.navigation ?? false,
    page: r.page === undefined ? "" : String(r.page),
    redirects: (r.redirects ?? []).map(String),
    websocket: r.websocket ?? false,
  });
}

/** The SetCookie notice as the jar's onSetCookie gets it. */
export function setCookieEventFromPb(e: SetCookie): SetCookieEvent {
  return {
    url: e.url,
    line: latin1(e.line),
    cookie: e.cookie === undefined ? null : cookieFromPb(e.cookie),
    rejected: e.rejected,
    deleted: e.deleted,
  };
}

/** Go's *cookies.Jar: a browser's cookie store, with its rules. */
export class Jar extends Saved {
  constructor(core: Core, id: bigint, onSetCookie?: (e: SetCookieEvent) => void) {
    super(core, id, () => {
      core.jars.delete(id);
    });
    if (onSetCookie !== undefined) {
      core.jars.set(id, (e) => {
        onSetCookie(setCookieEventFromPb(e));
      });
    }
  }

  private async cookieList(method: Method): Promise<Cookie[]> {
    const r = await this.core.call(method);
    if (r.result.case !== "cookies") {
      return [];
    }

    return r.result.value.cookies.map(cookieFromPb);
  }

  /** Every cookie, expired ones included until a request clears them. */
  all(): Promise<Cookie[]> {
    return this.cookieList({ case: "jarAll", value: { jar: this.id } });
  }

  /** The cookies the browser would send with req, in its order; updates their last access. */
  cookies(req: CookieRequest): Promise<Cookie[]> {
    return this.cookieList({ case: "jarCookies", value: { jar: this.id, request: cookieRequestToPb(req) } });
  }

  /** What cookies would return, changing nothing. */
  peek(req: CookieRequest): Promise<Cookie[]> {
    return this.cookieList({ case: "jarPeek", value: { jar: this.id, request: cookieRequestToPb(req) } });
  }

  /** Stores a cookie as given. */
  async set(cookie: Partial<Cookie> & Pick<Cookie, "name" | "value">): Promise<void> {
    await this.core.call({ case: "jarSet", value: { jar: this.id, cookie: cookieToPb(cookie) } });
  }

  /** Stores "a=1; b=2" as Domain cookies of domain (Go's SetString). */
  async setString(domain: string, cookies: string): Promise<void> {
    await this.core.call({ case: "jarSetString", value: { jar: this.id, domain, cookies: byteString(cookies, "the cookies") } });
  }

  /** Stores "a=1; b=2" as host-only cookies of host (Go's SetHostString). */
  async setHostString(host: string, cookies: string): Promise<void> {
    await this.core.call({ case: "jarSetHostString", value: { jar: this.id, host, cookies: byteString(cookies, "the cookies") } });
  }

  /** Takes a response's raw Set-Cookie lines as the browser would (Go's SetCookies). */
  async setCookies(req: CookieRequest, lines: readonly string[]): Promise<void> {
    await this.core.call({
      case: "jarSetCookies",
      value: { jar: this.id, request: cookieRequestToPb(req), lines: lines.map((l) => byteString(l, "a Set-Cookie line")) },
    });
  }

  private async count(method: Method): Promise<number> {
    const r = await this.core.call(method);

    return r.result.case === "count" ? Number(r.result.value) : 0;
  }

  /** Removes expired cookies and returns how many. */
  clearExpired(): Promise<number> {
    return this.count({ case: "jarClearExpired", value: { jar: this.id } });
  }

  /** Removes session cookies, as a browser restart does, and returns how many. */
  clearSession(): Promise<number> {
    return this.count({ case: "jarClearSession", value: { jar: this.id } });
  }

  /** Go's Jar.String(name). */
  async string(name: string): Promise<string> {
    const r = await this.core.call({ case: "jarString", value: { jar: this.id, name } });

    return r.result.case === "value" ? latin1(r.result.value) : "";
  }

  /** An independent jar with the same rules and cookies. */
  async clone(): Promise<Jar> {
    const r = await this.core.call({ case: "jarClone", value: { jar: this.id } });
    if (r.result.case !== "object") {
      throw new ProtocolError("mimicry: JarClone's reply has no jar");
    }

    return new Jar(this.core, r.result.value.id);
  }

  /** Removes every cookie. */
  async clear(): Promise<void> {
    await this.core.call({ case: "clear", value: { id: this.id } });
  }

  /** Saves the cookies in Netscape's cookies.txt format: to a file (0600) or a stream. */
  writeNetscape(dest: StateDestination): Promise<void> {
    return writeState(this.core, { case: "writeNetscape", value: { jar: this.id } }, dest);
  }

  /** Loads cookies in Netscape's cookies.txt format. */
  readNetscape(src: StateSource): Promise<void> {
    return readState(this.core, { case: "readNetscape", value: { jar: this.id } }, src);
  }
}

/** Go's *mimicry.TicketCache: TLS session tickets (secrets), shared by clients. */
export class TicketCache extends Saved {
  async clear(): Promise<void> {
    await this.core.call({ case: "clear", value: { id: this.id } });
  }
}

/** Go's *mimicry.AltSvcCache: Alt-Svc advertisements, shared by clients. */
export class AltSvcCache extends Saved {
  async clear(): Promise<void> {
    await this.core.call({ case: "clear", value: { id: this.id } });
  }
}

/** Go's *mimicry.MemoryClientHintStore: each origin's client hints opt-in. */
export class ClientHintStore extends Saved {
  /** The hints origin asked for. */
  async clientHints(origin: string): Promise<string[]> {
    const r = await this.core.call({ case: "clientHints", value: { store: this.id, origin } });

    return r.result.case === "strings" ? r.result.value.values : [];
  }

  async setClientHints(origin: string, hints: readonly string[]): Promise<void> {
    await this.core.call({ case: "setClientHints", value: { store: this.id, origin, hints: [...hints] } });
  }
}

/** newJar's options (Go's cookies.Config, as data). */
export interface JarOptions {
  /** The profile whose cookie rules the jar keeps; unset is RFC 6265bis. */
  rules?: string | Profile;
  /** Called with each Set-Cookie line the jar receives. */
  onSetCookie?: (e: SetCookieEvent) => void;
}

/** Go's cookies.New. */
export async function newJar(options: JarOptions = {}): Promise<Jar> {
  const core = typeof options.rules === "object" ? options.rules.core : await defaultCore();
  const r = await core.call({
    case: "newJar",
    value: {
      rules: options.rules === undefined ? undefined : profileRef(options.rules),
      onSetCookie: options.onSetCookie !== undefined,
    },
  });
  if (r.result.case !== "object") {
    throw new ProtocolError("mimicry: NewJar's reply has no jar");
  }

  return new Jar(core, r.result.value.id, options.onSetCookie);
}

async function newObject(method: Method): Promise<[Core, bigint]> {
  const core = await defaultCore();
  const r = await core.call(method);
  if (r.result.case !== "object") {
    throw new ProtocolError(`mimicry: ${method.case}'s reply has no object`);
  }

  return [core, r.result.value.id];
}

/** Go's mimicry.NewTicketCache. */
export async function newTicketCache(): Promise<TicketCache> {
  return new TicketCache(...(await newObject({ case: "newTicketCache", value: {} })));
}

/** Go's mimicry.NewAltSvcCache. */
export async function newAltSvcCache(): Promise<AltSvcCache> {
  return new AltSvcCache(...(await newObject({ case: "newAltSvcCache", value: {} })));
}

/** Go's mimicry.NewClientHintStore. */
export async function newClientHintStore(): Promise<ClientHintStore> {
  return new ClientHintStore(...(await newObject({ case: "newClientHintStore", value: {} })));
}
