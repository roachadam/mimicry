// A request and how a page makes it: Go's http.Request plus mimicry's
// As, With and Exact. Go keeps those in the request's context; here they
// are fields of a plain object, and asKind, withOptions and exact return a
// copy with them set, as Go's return a shallow copy.

import type { HeaderInit } from "./header.ts";

/** How a page makes a request (Go's mimicry.Kind); Navigate is the zero value. */
export const Kind = {
  Navigate: 0,
  Frame: 1,
  Script: 2,
  AsyncScript: 3,
  DeferScript: 4,
  InjectedScript: 5,
  PreloadScript: 6,
  Style: 7,
  Image: 8,
  InjectedImage: 9,
  Font: 10,
  PreloadFont: 11,
  Fetch: 12,
  XHR: 13,
  Beacon: 14,
  /** Appears only in plans. */
  Preflight: -1,
} as const;
export type Kind = (typeof Kind)[keyof typeof Kind];

/** Go's mimicry.Priority. */
export const Priority = {
  Auto: 0,
  High: 1,
  Low: 2,
} as const;
export type Priority = (typeof Priority)[keyof typeof Priority];

/** Go's mimicry.CrossOrigin: an element's crossorigin attribute. */
export const CrossOrigin = {
  None: 0,
  Anonymous: 1,
  UseCredentials: 2,
} as const;
export type CrossOrigin = (typeof CrossOrigin)[keyof typeof CrossOrigin];

/** Go's mimicry.Credentials: fetch()'s credentials mode. */
export const Credentials = {
  SameOrigin: 0,
  Include: 1,
  Omit: 2,
} as const;
export type Credentials = (typeof Credentials)[keyof typeof Credentials];

/** Go's mimicry.HintDelegation: whether a cross-origin request gets the page's client hints. */
export const HintDelegation = {
  Default: 0,
  Delegated: 1,
  Withheld: 2,
} as const;
export type HintDelegation = (typeof HintDelegation)[keyof typeof HintDelegation];

/** Go's mimicry.Place: moves one header before or after another. */
export interface Place {
  header: string;
  after?: string;
  before?: string;
}

/**
 * How a page makes the request: Go's mimicry.Request, what With takes
 * (Go's From is page).
 */
export interface Options {
  kind?: Kind;
  /** The page making the request (Go's From). */
  page?: string | URL;
  priority?: Priority;
  crossOrigin?: CrossOrigin;
  credentials?: Credentials;
  noCORS?: boolean;
  /** The stylesheet that asked for a font. */
  stylesheet?: string | URL;
  ignoreCORS?: boolean;
  headerPlacement?: readonly Place[];
  clientHints?: HintDelegation;
}

/**
 * A request body. A string (UTF-8), bytes or a Blob go whole and can be
 * sent again for a redirect that keeps the body (Go's GetBody); a stream
 * goes as it is read, with `contentLength` if known, and cannot.
 */
export type Body = string | Uint8Array | ArrayBuffer | Blob | ReadableStream<Uint8Array> | AsyncIterable<Uint8Array>;

/** Go's http.Request, with mimicry's As, With and Exact as fields. */
export interface Request {
  /** GET when unset. */
  method?: string;
  url: string | URL;
  header?: HeaderInit;
  body?: Body | null;
  /** A streamed body's length; unset is unknown. */
  contentLength?: number;
  /** Aborting it cancels the request (Go's context), its body included. */
  signal?: AbortSignal;
  /** This request's proxy URL instead of the client's; "" connects directly. */
  proxy?: string;
  /** Set by asKind and withOptions; unset is a navigation. */
  options?: Options;
  /** Set by exact. */
  exact?: boolean;
  /** Go's HeaderOrderKey: the order of the request's own headers, lower case. */
  headerOrder?: readonly string[];
}

/** Go's mimicry.As: req made as kind k by the page the transport last navigated to. */
export function asKind<R extends Request>(req: R, kind: Kind): R {
  return withOptions(req, { kind });
}

/** Go's mimicry.With: a copy of req made as options describes. */
export function withOptions<R extends Request>(req: R, options: Options): R {
  return { ...req, options };
}

/**
 * Go's mimicry.Exact: a copy of req that sends exactly its own headers,
 * in its own order (headerOrder) or sorted by name, plus Config.header.
 */
export function exact<R extends Request>(req: R): R {
  return { ...req, exact: true };
}
