// Profiles: the built-in ones by name (Go's profiles.ByName), their data
// as JSON (profile.Encode), and edited ones parsed back (profile.Parse)
// into objects of the core.

import { create } from "@bufbuild/protobuf";

import { type Core, CoreObject, type Method, defaultCore } from "./core.ts";
import { ProtocolError, errorFromPb } from "./errors.ts";
import { type ProfileRef, ProfileRefSchema } from "./gen/mimicry/v1/mimicry_pb.js";

/** A profile parseProfile made: an object of the core. */
export class Profile extends CoreObject {
  readonly name: string;
  readonly userAgent: string;

  constructor(core: Core, id: bigint, name: string, userAgent: string) {
    super(core, id);
    this.name = name;
    this.userAgent = userAgent;
  }
}

/** A profile as the core names it. */
export function profileRef(p: string | Profile): ProfileRef {
  return create(ProfileRefSchema, {
    profile: typeof p === "string" ? { case: "name", value: p } : { case: "id", value: p.id },
  });
}

/** The built-in profiles (Go's profiles.Names and profiles.Supported). */
export interface ProfileList {
  /** Every built-in profile's name, deprecated ones included. */
  names: string[];
  /** The names not deprecated. */
  supported: string[];
}

/** Lists the built-in profiles. */
export async function listProfiles(): Promise<ProfileList> {
  const core = await defaultCore();
  const r = await core.call({ case: "listProfiles", value: {} });
  if (r.result.case !== "profiles") {
    throw new ProtocolError("mimicry: ListProfiles' reply has no profiles");
  }

  return {
    names: r.result.value.names,
    supported: r.result.value.supported,
  };
}

async function readAll(core: Core, method: Method): Promise<[string, string, Uint8Array]> {
  const k = await core.open(method);
  try {
    const r = await k.reply();
    if (r.error !== undefined) {
      throw errorFromPb(r.error);
    }

    const parts: Uint8Array[] = [];
    for (let b = await k.read(); b !== null; b = await k.read()) {
      parts.push(b);
    }

    const info = r.result.case === "profile" ? r.result.value : null;

    return [info?.name ?? "", info?.userAgent ?? "", Buffer.concat(parts)];
  } finally {
    k.close();
  }
}

/**
 * A profile's data as JSON (Go's profile.Encode), the form parseProfile
 * reads: edit it and parse it to use an edited profile.
 */
export async function profileSpec(profile: string | Profile): Promise<string> {
  const core = typeof profile === "string" ? await defaultCore() : profile.core;
  const [, , json] = await readAll(core, { case: "profileSpec", value: { profile: profileRef(profile) } });

  return Buffer.from(json).toString("utf8");
}

/**
 * Reads a profile's JSON (Go's profile.Parse); an invalid one throws a
 * ValidationError listing its problems.
 */
export async function parseProfile(json: string | Uint8Array): Promise<Profile> {
  const core = await defaultCore();
  const k = await core.open({ case: "parseProfile", value: {} }, { body: true });
  try {
    await k.write(typeof json === "string" ? Buffer.from(json, "utf8") : json);
    await k.closeWrite();

    const r = await k.reply();
    if (r.error !== undefined) {
      throw errorFromPb(r.error);
    }

    if (r.result.case !== "profile") {
      throw new ProtocolError("mimicry: ParseProfile's reply has no profile");
    }

    return new Profile(core, r.result.value.id, r.result.value.name, r.result.value.userAgent);
  } finally {
    k.close();
  }
}

/**
 * The captured machines of a platform ("macos", "windows", ...; Go's
 * profiles.Machines), each a profile.Machine as parsed JSON: values for
 * Config.clientHints.
 */
export async function machines(platform: string): Promise<object[]> {
  const core = await defaultCore();
  const r = await core.call({ case: "machines", value: { platform } });
  if (r.result.case !== "machines") {
    throw new ProtocolError("mimicry: Machines' reply has no machines");
  }

  return r.result.value.machines.map((m) => JSON.parse(Buffer.from(m).toString("utf8")) as object);
}
