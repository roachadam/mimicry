// The errors calls throw: one class per code of the core's Error (Go's
// sentinels and error types), under MimicryError; CoreExited when the core
// dies; ProtocolError when the connection to it breaks.

import type { Response } from "./client.ts";
import type { Error as PbError } from "./gen/mimicry/v1/mimicry_pb.js";
import { type Header, headerFromPb } from "./header.ts";

/** An error's kind, one per sentinel and error type of Go's API. */
export type ErrorCode =
  | "INTERNAL"
  | "NOT_MEASURED"
  | "CORS"
  | "PROXY"
  | "PIN"
  | "BLOCKED"
  | "PLACEMENT"
  | "CLOSE"
  | "VALIDATION"
  | "CANCELED"
  | "DEADLINE"
  | "NET"
  | "INVALID"
  | "VERIFY";

// The proto's Code values, in order.
const codes: readonly ErrorCode[] = [
  "INTERNAL",
  "NOT_MEASURED",
  "CORS",
  "PROXY",
  "PIN",
  "BLOCKED",
  "PLACEMENT",
  "CLOSE",
  "VALIDATION",
  "CANCELED",
  "DEADLINE",
  "NET",
  "INVALID",
  "VERIFY",
];

/**
 * An error the core returned: Go's error, its `code` the most specific of
 * the codes its chain matches, `matches` every one. Each code has a class
 * below, and `instanceof` any of them is true for an error that matches
 * its code: a proxy failure at its deadline is a ProxyError and a
 * DeadlineError.
 */
export class MimicryError extends Error {
  override name = "MimicryError";
  readonly code: ErrorCode;
  readonly matches: ReadonlySet<ErrorCode>;
  /** The response that came with the error: a failed WebSocket handshake's, say. */
  response: Response | null = null;

  constructor(message: string, code: ErrorCode, matches: Iterable<ErrorCode> = []) {
    super(message);
    this.code = code;
    this.matches = new Set([code, ...matches]);
  }
}

function matching(v: unknown, code: ErrorCode): boolean {
  return v instanceof MimicryError && v.matches.has(code);
}

/** Go's ErrNotMeasured: the profile never measured what the request needs. */
export class NotMeasuredError extends MimicryError {
  override name = "NotMeasuredError";

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "NOT_MEASURED");
  }
}

/** Go's *CORSError: the browser would have blocked the response. */
export class CORSError extends MimicryError {
  override name = "CORSError";
  url = "";
  redirect = false;
  reason = "";
  status = 0;
  header: Header | null = null;

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "CORS");
  }
}

/** Go's *ProxyError: the proxy refused or failed; its cause is in matches and the message. */
export class ProxyError extends MimicryError {
  override name = "ProxyError";
  /** The proxy's URL, its password removed. */
  proxy = "";
  statusCode = 0;
  header: Header | null = null;

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "PROXY");
  }
}

/** Go's *PinError: the server's chain matched none of Config.pins. */
export class PinError extends MimicryError {
  override name = "PinError";
  host = "";
  presented: string[] = [];

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "PIN");
  }
}

/** Go's ErrBlocked: the browser would not send the request at all. */
export class BlockedError extends MimicryError {
  override name = "BlockedError";

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "BLOCKED");
  }
}

/** Go's ErrPlacement: a header placement the request's headers cannot take. */
export class PlacementError extends MimicryError {
  override name = "PlacementError";

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "PLACEMENT");
  }
}

/** Go's *CloseError: the WebSocket closed; Go's Code is closeCode. */
export class CloseError extends MimicryError {
  override name = "CloseError";
  closeCode = 0;
  reason = "";

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "CLOSE");
  }
}

/** One problem of a profile that failed validation. */
export interface Problem {
  path: string;
  message: string;
}

/** Go's *profile.ValidationError: an edited profile is not valid. */
export class ValidationError extends MimicryError {
  override name = "ValidationError";
  problems: Problem[] = [];

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "VALIDATION");
  }
}

/** Go's context.Canceled: the core gave the call up. */
export class CanceledError extends MimicryError {
  override name = "CanceledError";

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "CANCELED");
  }
}

/** A deadline passed: Go's context.DeadlineExceeded, a timeout. */
export class DeadlineError extends MimicryError {
  override name = "DeadlineError";

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "DEADLINE");
  }
}

/** A network failure: a connection refused or reset, a failed lookup, a TLS failure. */
export class NetError extends MimicryError {
  override name = "NetError";

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "NET");
  }
}

/** A call the core could not carry out as given (a bad argument, a freed object). */
export class InvalidError extends MimicryError {
  override name = "InvalidError";

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "INVALID");
  }
}

/** Config.verifyConnection refused the connection; the message is its error's. */
export class VerifyError extends MimicryError {
  override name = "VerifyError";

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "VERIFY");
  }
}

/** Any other error of the core. */
export class InternalError extends MimicryError {
  override name = "InternalError";

  static override [Symbol.hasInstance](v: unknown): boolean {
    return matching(v, "INTERNAL");
  }
}

const classes: Record<ErrorCode, new (message: string, code: ErrorCode, matches: Iterable<ErrorCode>) => MimicryError> = {
  INTERNAL: InternalError,
  NOT_MEASURED: NotMeasuredError,
  CORS: CORSError,
  PROXY: ProxyError,
  PIN: PinError,
  BLOCKED: BlockedError,
  PLACEMENT: PlacementError,
  CLOSE: CloseError,
  VALIDATION: ValidationError,
  CANCELED: CanceledError,
  DEADLINE: DeadlineError,
  NET: NetError,
  INVALID: InvalidError,
  VERIFY: VerifyError,
};

function codeOf(c: number): ErrorCode {
  return codes[c] ?? "INTERNAL";
}

/** The error the core sent, as its class. */
export function errorFromPb(e: PbError): MimicryError {
  const code = codeOf(e.code);
  const err = new classes[code](e.message, code, e.matches.map(codeOf));

  // The typed fields go on the class of the error's own code.
  if (err instanceof CORSError && code === "CORS" && e.cors !== undefined) {
    err.url = e.cors.url;
    err.redirect = e.cors.redirect;
    err.reason = e.cors.reason;
    err.status = e.cors.status;
    err.header = headerFromPb(e.cors.header);
  } else if (err instanceof ProxyError && code === "PROXY" && e.proxy !== undefined) {
    err.proxy = e.proxy.proxy;
    err.statusCode = e.proxy.statusCode;
    err.header = headerFromPb(e.proxy.header);
  } else if (err instanceof PinError && code === "PIN" && e.pin !== undefined) {
    err.host = e.pin.host;
    err.presented = e.pin.presented;
  } else if (err instanceof CloseError && code === "CLOSE" && e.close !== undefined) {
    err.closeCode = e.close.code;
    err.reason = e.close.reason;
  } else if (err instanceof ValidationError && code === "VALIDATION" && e.validation !== undefined) {
    err.problems = e.validation.problems.map((p) => ({
      path: p.path,
      message: p.message,
    }));
  }

  return err;
}

/** The core exited: its exit code or the signal that ended it, and the end of its stderr. */
export class CoreExited extends Error {
  override name = "CoreExited";
  readonly exitCode: number | null;
  readonly signal: NodeJS.Signals | null;
  readonly stderr: string;

  constructor(exitCode: number | null, signal: NodeJS.Signals | null, stderr: string) {
    const status = signal !== null ? `signal ${signal}` : `exit code ${exitCode}`;
    const tail = stderr.trim();
    super(`mimicry: mimicry-core exited (${status})${tail === "" ? "" : `: ${tail}`}`);
    this.exitCode = exitCode;
    this.signal = signal;
    this.stderr = stderr;
  }
}

/** The connection to the core broke a rule of the protocol; the core was stopped. */
export class ProtocolError extends Error {
  override name = "ProtocolError";
}
