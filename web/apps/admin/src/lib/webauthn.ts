/**
 * Passkeys in the browser. The server sends WebAuthn options as JSON with
 * base64url strings; the browser API wants ArrayBuffers, and the answer goes
 * back as JSON again.
 */

export const passkeysSupported = () => typeof window !== "undefined" && !!window.PublicKeyCredential && !!navigator.credentials;

const fromB64url = (s: string) => {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(s.length / 4) * 4, "=");
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
};

const toB64url = (buf: ArrayBuffer | null) => {
  if (!buf) return undefined;
  const bytes = new Uint8Array(buf);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};

type Json = Record<string, unknown>;
type Descriptor = { id: string; type: string; transports?: string[] };

function creationOptions(o: { publicKey: Json }): CredentialCreationOptions {
  const pk = o.publicKey as Json & { challenge: string; user: Json & { id: string }; excludeCredentials?: Descriptor[] };
  return {
    publicKey: {
      ...(pk as object),
      challenge: fromB64url(pk.challenge),
      user: { ...(pk.user as object), id: fromB64url(pk.user.id) } as PublicKeyCredentialUserEntity,
      excludeCredentials: (pk.excludeCredentials ?? []).map((c) => ({ ...c, id: fromB64url(c.id) }) as PublicKeyCredentialDescriptor),
    } as PublicKeyCredentialCreationOptions,
  };
}

function requestOptions(o: { publicKey: Json; mediation?: string }): CredentialRequestOptions {
  const pk = o.publicKey as Json & { challenge: string; allowCredentials?: Descriptor[] };
  return {
    publicKey: {
      ...(pk as object),
      challenge: fromB64url(pk.challenge),
      allowCredentials: (pk.allowCredentials ?? []).map((c) => ({ ...c, id: fromB64url(c.id) }) as PublicKeyCredentialDescriptor),
    } as PublicKeyCredentialRequestOptions,
  };
}

/** Creates a passkey; returns the JSON the server's finish step expects. */
export async function createPasskey(options: { publicKey: Json }) {
  const cred = (await navigator.credentials.create(creationOptions(options))) as PublicKeyCredential | null;
  if (!cred) throw new Error("No passkey was created");
  const res = cred.response as AuthenticatorAttestationResponse;
  return {
    id: cred.id,
    rawId: toB64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults(),
    response: {
      clientDataJSON: toB64url(res.clientDataJSON),
      attestationObject: toB64url(res.attestationObject),
      transports: typeof res.getTransports === "function" ? res.getTransports() : undefined,
    },
  };
}

/** Signs in with a passkey; returns the JSON the server's finish step expects. */
export async function getPasskey(options: { publicKey: Json }) {
  const cred = (await navigator.credentials.get(requestOptions(options))) as PublicKeyCredential | null;
  if (!cred) throw new Error("No passkey was chosen");
  const res = cred.response as AuthenticatorAssertionResponse;
  return {
    id: cred.id,
    rawId: toB64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults(),
    response: {
      clientDataJSON: toB64url(res.clientDataJSON),
      authenticatorData: toB64url(res.authenticatorData),
      signature: toB64url(res.signature),
      userHandle: toB64url(res.userHandle),
    },
  };
}

/** A friendly message for the errors browsers throw when a passkey prompt ends. */
export function passkeyError(e: unknown): string | null {
  const name = e instanceof DOMException ? e.name : "";
  if (name === "NotAllowedError" || name === "AbortError") return null; // cancelled or timed out
  if (name === "InvalidStateError") return "That passkey is already added to your account.";
  if (name === "SecurityError") return "Passkeys need the admin's own address (https, or localhost).";
  return e instanceof Error ? e.message : "The passkey didn't work";
}

/** A guess at a name for a new passkey, e.g. "iPhone" or "Mac". */
export function deviceName() {
  const ua = navigator.userAgent;
  if (/iPhone/.test(ua)) return "iPhone";
  if (/iPad/.test(ua)) return "iPad";
  if (/Android/.test(ua)) return "Android phone";
  if (/Macintosh/.test(ua)) return "Mac";
  if (/Windows/.test(ua)) return "Windows PC";
  return "Passkey";
}
