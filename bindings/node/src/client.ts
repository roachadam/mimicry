// Clients and transports: Go's mimicry.NewClient and NewTransport, their
// Config as data (callbacks as data, VerifyConnection a reverse call),
// requests and their responses with bodies as WHATWG ReadableStreams.

import { X509Certificate } from "node:crypto";

import { create } from "@bufbuild/protobuf";
import { type Duration, durationFromMs, timestampDate } from "@bufbuild/protobuf/wkt";

import { CallStream, Core, CoreObject, defaultCore } from "./core.ts";
import { MimicryError, ProtocolError, errorFromPb } from "./errors.ts";
import {
  type CertPin as PbCertPin,
  type Config as PbConfig,
  ConfigSchema,
  type Credentials as PbCredentials,
  type CrossOrigin as PbCrossOrigin,
  type HintDelegation as PbHintDelegation,
  type Kind as PbKind,
  type Priority as PbPriority,
  type ConnectionState as PbConnectionState,
  type Lines as PbLines,
  type Options as PbOptions,
  OptionsSchema,
  type PlanReply,
  type Request as PbRequest,
  RequestSchema,
  type Response as PbResponse,
} from "./gen/mimicry/v1/mimicry_pb.js";
import { type Header, type HeaderInit, headerFromPb, headerToPb } from "./header.ts";
import { type Profile, profileRef } from "./profiles.ts";
import type { Body, Kind, Options, Place, Request } from "./request.ts";
import { AltSvcCache, ClientHintStore, Jar, TicketCache } from "./state.ts";
import { WebSocket, type WebSocketOptions, openWebSocket } from "./websocket.ts";

/** Go's mimicry.Pin: keys a host's chain must hold. */
export interface Pin {
  host: string;
  includeSubdomains?: boolean;
  /** Accepted keys: "sha256/<base64>" of a SubjectPublicKeyInfo. */
  spki: readonly string[];
}

/** A client certificate chain and its key, both PEM, for host ("" or unset: every other host). */
export interface ClientCertificate {
  host?: string;
  certificate: string | Uint8Array;
  key: string | Uint8Array;
}

/** Go's tls.ConnectionState. */
export interface ConnectionState {
  version: number;
  handshakeComplete: boolean;
  didResume: boolean;
  cipherSuite: number;
  negotiatedProtocol: string;
  serverName: string;
  /** Leaf first. */
  peerCertificates: X509Certificate[];
  verifiedChains: X509Certificate[][];
  signedCertificateTimestamps: Uint8Array[];
  ocspResponse: Uint8Array;
  echAccepted: boolean;
}

/**
 * Go's mimicry.Config, its callbacks as data (decision 0069). Durations
 * are milliseconds. Objects (cookies, caches, the store, a parsed
 * profile) must come from the same core as the client, which they do
 * unless one core died in between.
 */
export interface Config {
  /** A built-in profile's name ("chrome-156-macos") or a parsed one. */
  profile: string | Profile;
  /** PEM certificates that verify servers instead of the system's roots. */
  rootCAs?: string | Uint8Array;
  dialTimeout?: number;
  /** The client's timeout (newClient only), response body included. */
  timeout?: number;
  localAddrs?: readonly string[];
  /** The DNS server ("host:port") that looks up host names (Go's Resolver, as data). */
  dnsServer?: string;
  /** The proxy URL for every request (Go's Proxy, as data). */
  proxy?: string;
  header?: HeaderInit;
  headerPlacement?: readonly Place[];
  cookies?: Jar;
  clientHintStore?: ClientHintStore;
  /** A machine (Go's profile.Machine) as JSON or its object, from machines(). */
  clientHints?: string | object;
  disableDecompression?: boolean;
  disableHTTP3?: boolean;
  altSvc?: AltSvcCache;
  tickets?: TicketCache;
  browserQUICFallback?: boolean;
  browserDialDelay?: boolean;
  browserProxyAuth?: boolean;
  disableCoalescing?: boolean;
  maxConnsPerHost?: number;
  idleConnTimeout?: number;
  maxIdleProxies?: number;
  http3OverSOCKS5?: boolean;
  /** Maps a proxy URL to its CONNECT-UDP proxy URL (Go's ConnectUDPProxy, as data). */
  connectUDPProxy?: Readonly<Record<string, string>>;
  http3ReadBufferSize?: number;
  http3ACKEvery?: number;
  pins?: readonly Pin[];
  /**
   * Go's VerifyConnection: called with each verified connection's state;
   * throwing (or rejecting) fails the connection with a VerifyError
   * carrying its message.
   */
  verifyConnection?: (state: ConnectionState) => void | Promise<void>;
  /** Go's GetClientCertificate, as data: the entry for the host, else the one without a host. */
  clientCertificates?: readonly ClientCertificate[];
  /** A file the core appends TLS secrets to, created 0600 (Go's KeyLogWriter). */
  keyLogFile?: string;
}

/** How a client follows redirects (http.Client's CheckRedirect, as data). */
export interface Redirects {
  /** Unset or true follows them; false returns the redirect itself. */
  follow?: boolean;
  /** Stops a chain after this many; 0 or unset is the browser's limit. */
  max?: number;
}

/** newClient's Config, with how it follows redirects. */
export interface ClientConfig extends Config {
  redirects?: Redirects;
}

function pem(v: string | Uint8Array | undefined): Uint8Array {
  if (v === undefined) {
    return new Uint8Array(0);
  }

  return typeof v === "string" ? Buffer.from(v, "utf8") : v;
}

function ms(v: number | undefined): Duration | undefined {
  return v === undefined ? undefined : durationFromMs(v);
}

// The core a config's objects belong to; the process's when none.
async function coreFor(cfg: Config): Promise<Core> {
  const objects: CoreObject[] = [];
  for (const o of [cfg.profile, cfg.cookies, cfg.clientHintStore, cfg.altSvc, cfg.tickets]) {
    if (o instanceof CoreObject) {
      objects.push(o);
    }
  }

  const core = objects[0]?.core ?? (await defaultCore());
  if (objects.some((o) => o.core !== core)) {
    throw new TypeError("mimicry: the config's objects come from different cores (one died in between)");
  }

  return core;
}

function configToPb(cfg: Config): PbConfig {
  return create(ConfigSchema, {
    profile: profileRef(cfg.profile),
    rootCas: pem(cfg.rootCAs),
    dialTimeout: ms(cfg.dialTimeout),
    timeout: ms(cfg.timeout),
    localAddrs: [...(cfg.localAddrs ?? [])],
    dnsServer: cfg.dnsServer ?? "",
    proxy: cfg.proxy ?? "",
    header: headerToPb(cfg.header),
    headerPlacement: (cfg.headerPlacement ?? []).map(placeToPb),
    cookies: cfg.cookies?.id ?? 0n,
    clientHintStore: cfg.clientHintStore?.id ?? 0n,
    clientHints:
      cfg.clientHints === undefined
        ? new Uint8Array(0)
        : Buffer.from(typeof cfg.clientHints === "string" ? cfg.clientHints : JSON.stringify(cfg.clientHints), "utf8"),
    disableDecompression: cfg.disableDecompression ?? false,
    disableHttp3: cfg.disableHTTP3 ?? false,
    altSvc: cfg.altSvc?.id ?? 0n,
    tickets: cfg.tickets?.id ?? 0n,
    browserQuicFallback: cfg.browserQUICFallback ?? false,
    browserDialDelay: cfg.browserDialDelay ?? false,
    browserProxyAuth: cfg.browserProxyAuth ?? false,
    disableCoalescing: cfg.disableCoalescing ?? false,
    maxConnsPerHost: cfg.maxConnsPerHost ?? 0,
    idleConnTimeout: ms(cfg.idleConnTimeout),
    maxIdleProxies: cfg.maxIdleProxies ?? 0,
    http3OverSocks5: cfg.http3OverSOCKS5 ?? false,
    connectUdpProxy: { ...cfg.connectUDPProxy },
    http3ReadBufferSize: cfg.http3ReadBufferSize ?? 0,
    http3AckEvery: cfg.http3ACKEvery ?? 0,
    pins: (cfg.pins ?? []).map((p) => ({
      host: p.host,
      includeSubdomains: p.includeSubdomains ?? false,
      spki: [...p.spki],
    })),
    verifyConnection: cfg.verifyConnection !== undefined,
    clientCertificates: (cfg.clientCertificates ?? []).map((c) => ({
      host: c.host ?? "",
      certificate: pem(c.certificate),
      key: pem(c.key),
    })),
    keyLogFile: cfg.keyLogFile ?? "",
  });
}

function placeToPb(p: Place): { header: string; after: string; before: string } {
  return {
    header: p.header,
    after: p.after ?? "",
    before: p.before ?? "",
  };
}

function optionsToPb(o: Options | undefined): PbOptions | undefined {
  if (o === undefined) {
    return undefined;
  }

  return create(OptionsSchema, {
    kind: (o.kind ?? 0) as PbKind,
    page: o.page === undefined ? "" : String(o.page),
    priority: (o.priority ?? 0) as PbPriority,
    crossOrigin: (o.crossOrigin ?? 0) as PbCrossOrigin,
    credentials: (o.credentials ?? 0) as PbCredentials,
    noCors: o.noCORS ?? false,
    stylesheet: o.stylesheet === undefined ? "" : String(o.stylesheet),
    ignoreCors: o.ignoreCORS ?? false,
    headerPlacement: (o.headerPlacement ?? []).map(placeToPb),
    clientHints: (o.clientHints ?? 0) as PbHintDelegation,
  });
}

type Streamed = ReadableStream<Uint8Array> | AsyncIterable<Uint8Array>;

// The request as the core takes it, and its body when it is streamed.
async function requestToPb(req: Request): Promise<[PbRequest, Streamed | null]> {
  const r = create(RequestSchema, {
    method: req.method ?? "GET",
    url: String(req.url),
    header: headerToPb(req.header),
    options: optionsToPb(req.options),
    exact: req.exact ?? false,
    headerOrder: [...(req.headerOrder ?? [])],
  });
  if (req.proxy !== undefined) {
    r.proxy = req.proxy;
  }

  const body: Body | null | undefined = req.body;
  if (body === undefined || body === null) {
    return [r, null];
  }

  if (typeof body === "string") {
    r.body = Buffer.from(body, "utf8");
  } else if (body instanceof Uint8Array) {
    r.body = body;
  } else if (body instanceof ArrayBuffer) {
    r.body = new Uint8Array(body);
  } else if (body instanceof Blob) {
    r.body = new Uint8Array(await body.arrayBuffer());
  } else {
    r.streamBody = true;
    if (req.contentLength !== undefined) {
      r.contentLength = BigInt(req.contentLength);
    }

    return [r, body];
  }

  return [r, null];
}

// pumpBody sends a streamed body as the call's DATA, then END; a failure
// resets the call with it.
async function pumpBody(k: CallStream, body: Streamed): Promise<void> {
  try {
    for await (const chunk of body as AsyncIterable<Uint8Array | string>) {
      const b = typeof chunk === "string" ? Buffer.from(chunk, "utf8") : chunk;
      if (b.length > 0) {
        await k.write(b);
      }
    }

    await k.closeWrite();
  } catch (e) {
    k.close(e);
  }
}

/** The connection state the core sent, with its certificates parsed. */
export function connectionStateFromPb(s: PbConnectionState): ConnectionState {
  return {
    version: s.version,
    handshakeComplete: s.handshakeComplete,
    didResume: s.didResume,
    cipherSuite: s.cipherSuite,
    negotiatedProtocol: s.negotiatedProtocol,
    serverName: s.serverName,
    peerCertificates: s.peerCertificates.map((d) => new X509Certificate(d)),
    verifiedChains: s.verifiedChains.map((c) => c.certificates.map((d) => new X509Certificate(d))),
    signedCertificateTimestamps: s.signedCertificateTimestamps,
    ocspResponse: s.ocspResponse,
    echAccepted: s.echAccepted,
  };
}

// Bodies nobody can read any more reset their call.
const bodies = new FinalizationRegistry<CallStream>((k) => {
  k.close();
});

function bodyStream(k: CallStream): ReadableStream<Uint8Array> {
  const body = new ReadableStream<Uint8Array>(
    {
      async pull(controller) {
        try {
          const b = await k.read();
          if (b === null) {
            bodies.unregister(body);
            k.close();
            controller.close();
          } else {
            controller.enqueue(b);
          }
        } catch (e) {
          bodies.unregister(body);
          k.close();
          controller.error(e);
        }
      },
      cancel(reason) {
        bodies.unregister(body);
        k.close(reason);
      },
    },
    { highWaterMark: 0 },
  );
  bodies.register(body, k, body);

  return body;
}

/**
 * Go's http.Response. The body is read as it is consumed: the core sends
 * at most 256 KiB ahead of the reader (the protocol's credit), so a body
 * nobody reads holds the request back rather than filling memory. Read it
 * to its end or cancel it; a body that is garbage collected is cancelled.
 */
export class Response {
  readonly statusCode: number;
  readonly status: string;
  readonly proto: string;
  readonly header: Header;
  /** -1 when unknown. */
  readonly contentLength: number;
  readonly uncompressed: boolean;
  /** The final request's URL, after redirects. */
  readonly url: string;
  /** The connection's state; null over plain HTTP. */
  readonly tls: ConnectionState | null;
  readonly body: ReadableStream<Uint8Array>;

  constructor(r: PbResponse, k: CallStream | null) {
    this.statusCode = r.statusCode;
    this.status = r.status;
    this.proto = r.proto;
    this.header = headerFromPb(r.header);
    this.contentLength = Number(r.contentLength);
    this.uncompressed = r.uncompressed;
    this.url = r.url;
    this.tls = r.tls === undefined ? null : connectionStateFromPb(r.tls);
    this.body =
      k === null
        ? new ReadableStream({
            start(controller) {
              controller.close();
            },
          })
        : bodyStream(k);
  }

  /** The whole body. */
  async bytes(): Promise<Uint8Array> {
    const parts: Uint8Array[] = [];
    for await (const chunk of this.body) {
      parts.push(chunk);
    }

    return Buffer.concat(parts);
  }

  async arrayBuffer(): Promise<ArrayBuffer> {
    const b = await this.bytes();

    return b.buffer.slice(b.byteOffset, b.byteOffset + b.byteLength) as ArrayBuffer;
  }

  /** The whole body as UTF-8. */
  async text(): Promise<string> {
    return new TextDecoder().decode(await this.bytes());
  }

  async json(): Promise<unknown> {
    return JSON.parse(await this.text());
  }

  /** Cancels the body if it is still unread. */
  async [Symbol.asyncDispose](): Promise<void> {
    if (!this.body.locked) {
      await this.body.cancel();
    }
  }
}

/** Go's mimicry.Lines: a request as one protocol would send it. */
export interface Lines {
  requestLine: string;
  headers: { name: string; value: string }[];
  /** Why this protocol cannot send it. */
  error: MimicryError | null;
}

/** Go's mimicry.Override: a header the caller set over mimicry's. */
export interface Override {
  name: string;
  mimicry: string;
  caller: string[];
}

/** Go's mimicry.Plan: what a request would send, without sending it. */
export interface Plan {
  method: string;
  url: string;
  kind: Kind;
  variant: string;
  condition: string[];
  protocol: string;
  http2: Lines | null;
  http1: Lines | null;
  /** Null when the transport speaks no HTTP/3. */
  http3: Lines | null;
  preflight: Plan | null;
  overrides: Override[];
  dropped: string[];
  /** Go's Plan.String(). */
  text: string;
}

function latin1(b: Uint8Array): string {
  return Buffer.from(b).toString("latin1");
}

function linesFromPb(l: PbLines | undefined): Lines | null {
  if (l === undefined) {
    return null;
  }

  return {
    requestLine: l.requestLine,
    headers: l.headers.map((h) => ({ name: h.name, value: latin1(h.value) })),
    error: l.error === undefined ? null : errorFromPb(l.error),
  };
}

function planFromPb(p: PlanReply): Plan {
  return {
    method: p.method,
    url: p.url,
    kind: p.kind,
    variant: p.variant,
    condition: p.condition,
    protocol: p.protocol,
    http2: linesFromPb(p.http2),
    http1: linesFromPb(p.http1),
    http3: linesFromPb(p.http3),
    preflight: p.preflight === undefined ? null : planFromPb(p.preflight),
    overrides: p.overrides.map((o) => ({ name: o.name, mimicry: latin1(o.mimicry), caller: o.caller.map(latin1) })),
    dropped: p.dropped,
    text: p.text,
  };
}

/** Sends req through the client or transport id; the response's body follows. */
async function send(core: Core, id: bigint, req: Request): Promise<Response> {
  const [r, streamed] = await requestToPb(req);
  const k = await core.open({ case: "do", value: { client: id, request: r } }, { signal: req.signal, body: streamed !== null });
  if (streamed !== null) {
    void pumpBody(k, streamed);
  }

  try {
    const reply = await k.reply();
    if (reply.error !== undefined) {
      const err = errorFromPb(reply.error);
      if (reply.result.case === "response") {
        err.response = new Response(reply.result.value, k);
      } else {
        k.close();
      }

      throw err;
    }

    if (reply.result.case !== "response") {
      throw new ProtocolError("mimicry: Do's reply has no response");
    }

    return new Response(reply.result.value, k);
  } catch (e) {
    if (!(e instanceof MimicryError && e.response !== null)) {
      k.close(e);
    }

    throw e;
  }
}

// The clients and transports with a request or a WebSocket dial in
// flight, by count: the call's promise does not refer to its round
// tripper, which garbage collection would otherwise free mid-call (its
// verifier dropped, the core's transport closed) when nothing else holds
// it. Held until the reply (the handshake's answer) has arrived.
const inFlight = new Map<RoundTripper, number>();

async function holding<T>(rt: RoundTripper, p: () => Promise<T>): Promise<T> {
  inFlight.set(rt, (inFlight.get(rt) ?? 0) + 1);
  try {
    return await p();
  } finally {
    const n = (inFlight.get(rt) ?? 1) - 1;
    if (n === 0) {
      inFlight.delete(rt);
    } else {
      inFlight.set(rt, n);
    }
  }
}

/** What a client and a transport share: plans, WebSockets, idle connections. */
class RoundTripper extends CoreObject {
  constructor(core: Core, id: bigint) {
    super(core, id, () => {
      core.verifiers.delete(id);
    });
  }

  /** Plans req as RoundTrip would send it (Go's Transport.Plan). */
  async plan(req: Request): Promise<Plan> {
    const [r] = await requestToPb({ ...req, body: null });
    const reply = await this.core.call({ case: "plan", value: { transport: this.id, request: r } }, req.signal);
    if (reply.result.case !== "plan") {
      throw new ProtocolError("mimicry: Plan's reply has no plan");
    }

    return planFromPb(reply.result.value);
  }

  /**
   * Opens a WebSocket (Go's Transport.DialWebSocket). A failed handshake
   * throws its error with the server's response (`error.response`).
   */
  dialWebSocket(url: string | URL, options: WebSocketOptions = {}): Promise<WebSocket> {
    return holding(this, () => openWebSocket(this.core, this.id, String(url), options));
  }

  /** Closes connections with no requests in flight. */
  async closeIdleConnections(): Promise<void> {
    await this.core.call({ case: "closeIdleConnections", value: { transport: this.id } });
  }

}

/** Go's http.Client from mimicry.NewClient: it follows redirects and keeps cookies. */
export class Client extends RoundTripper {
  /** The client's jar: Config.cookies, or the one it made. */
  readonly cookies: Jar;

  constructor(core: Core, id: bigint, cookies: Jar) {
    super(core, id);
    this.cookies = cookies;
  }

  /** Go's Client.Do. */
  do(req: Request): Promise<Response> {
    return holding(this, () => send(this.core, this.id, req));
  }
}

/** Go's mimicry.Transport: one hop per request, redirects returned. */
export class Transport extends RoundTripper {
  /** Go's Transport.RoundTrip. */
  roundTrip(req: Request): Promise<Response> {
    return holding(this, () => send(this.core, this.id, req));
  }
}

function verifier(cfg: Config): ((s: PbConnectionState) => unknown) | null {
  const fn = cfg.verifyConnection;
  if (fn === undefined) {
    return null;
  }

  return (s) => fn(connectionStateFromPb(s));
}

/** Go's mimicry.NewClient. */
export async function newClient(config: ClientConfig): Promise<Client> {
  const core = await coreFor(config);
  const redirects =
    config.redirects === undefined
      ? undefined
      : {
          ...(config.redirects.follow === undefined ? {} : { follow: config.redirects.follow }),
          max: config.redirects.max ?? 0,
        };
  const r = await core.call({
    case: "newClient",
    value: {
      config: configToPb(config),
      redirects,
    },
  });
  if (r.result.case !== "newClient") {
    throw new ProtocolError("mimicry: NewClient's reply has no client");
  }

  const { client, cookies } = r.result.value;
  const jar = config.cookies !== undefined && config.cookies.id === cookies ? config.cookies : new Jar(core, cookies);
  const c = new Client(core, client, jar);
  const v = verifier(config);
  if (v !== null) {
    core.verifiers.set(client, v);
  }

  return c;
}

/** Go's mimicry.NewTransport. */
export async function newTransport(config: Config): Promise<Transport> {
  const core = await coreFor(config);
  const r = await core.call({ case: "newTransport", value: { config: configToPb(config) } });
  if (r.result.case !== "object") {
    throw new ProtocolError("mimicry: NewTransport's reply has no transport");
  }

  const t = new Transport(core, r.result.value.id);
  const v = verifier(config);
  if (v !== null) {
    core.verifiers.set(t.id, v);
  }

  return t;
}

/** Go's mimicry.CertRole. */
export const CertRole = {
  Leaf: 0,
  Intermediate: 1,
  Root: 2,
} as const;
export type CertRole = (typeof CertRole)[keyof typeof CertRole];

/** Go's mimicry.CertPin: a certificate of a chain and its pin. */
export interface CertPin {
  role: CertRole;
  subject: string;
  issuer: string;
  notAfter: Date | null;
  spki: string;
  spkiHex: string;
  fingerprint: string;
}

function certPinFromPb(p: PbCertPin): CertPin {
  return {
    role: p.role,
    subject: p.subject,
    issuer: p.issuer,
    notAfter: p.notAfter === undefined ? null : timestampDate(p.notAfter),
    spki: p.spki,
    spkiHex: p.spkiHex,
    fingerprint: p.fingerprint,
  };
}

/**
 * Go's mimicry.FetchPins: connects to url's host as a request would and
 * returns the verified chain, leaf first, with its pins. As in Go, it
 * ignores config.pins and config.verifyConnection.
 */
export async function fetchPins(config: Config, url: string | URL, options: { signal?: AbortSignal } = {}): Promise<CertPin[]> {
  const core = await coreFor(config);
  const pb = configToPb(config);
  pb.verifyConnection = false;
  const r = await core.call({ case: "fetchPins", value: { config: pb, url: String(url) } }, options.signal);
  if (r.result.case !== "pins") {
    throw new ProtocolError("mimicry: FetchPins' reply has no pins");
  }

  return r.result.value.pins.map(certPinFromPb);
}
