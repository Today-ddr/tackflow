import { api, errorMessage, type LoginResponse } from "./api";

export interface Passkey {
  id: string;
  name: string;
  created_at: string;
  last_used_at: string | null;
}

interface CredentialDescriptorJSON extends Omit<PublicKeyCredentialDescriptor, "id"> {
  id: string;
}

interface CreationOptionsJSON extends Omit<PublicKeyCredentialCreationOptions, "challenge" | "user" | "excludeCredentials"> {
  challenge: string;
  user: Omit<PublicKeyCredentialUserEntity, "id"> & { id: string };
  excludeCredentials?: CredentialDescriptorJSON[];
}

interface RequestOptionsJSON extends Omit<PublicKeyCredentialRequestOptions, "challenge" | "allowCredentials"> {
  challenge: string;
  allowCredentials?: CredentialDescriptorJSON[];
}

type JSONValue = string | number | boolean | null | JSONValue[] | { [key: string]: JSONValue };

export function supportsPasskeys(): boolean {
  return window.isSecureContext === true && typeof PublicKeyCredential !== "undefined" &&
    typeof navigator.credentials?.get === "function" && typeof navigator.credentials?.create === "function";
}

function decode(value: string): ArrayBuffer {
  const base64 = value.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(base64.padEnd(Math.ceil(base64.length / 4) * 4, "="));
  return Uint8Array.from(binary, (character) => character.charCodeAt(0)).buffer;
}

function encode(value: ArrayBuffer): string {
  let binary = "";
  for (const byte of new Uint8Array(value)) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function extensionJSON(value: unknown): JSONValue {
  if (value instanceof ArrayBuffer) return encode(value);
  if (ArrayBuffer.isView(value)) return encode(new Uint8Array(value.buffer, value.byteOffset, value.byteLength).slice().buffer);
  if (Array.isArray(value)) return value.map(extensionJSON);
  if (value !== null && typeof value === "object") return Object.fromEntries(Object.entries(value).filter(([, item]) => item !== undefined).map(([key, item]) => [key, extensionJSON(item)]));
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") return value;
  return null;
}

function serialize(credential: Credential | null): JSONValue {
  if (!credential || credential.type !== "public-key") throw new Error("未取得 Passkey，请重试或使用密码登录");
  const publicKey = credential as PublicKeyCredential;
  const response = publicKey.response;
  let serializedResponse: { [key: string]: JSONValue };
  if ("attestationObject" in response) {
    const attestation = response as AuthenticatorAttestationResponse;
    serializedResponse = {
      clientDataJSON: encode(attestation.clientDataJSON),
      attestationObject: encode(attestation.attestationObject),
      ...(typeof attestation.getTransports === "function" ? { transports: attestation.getTransports() } : {}),
    };
  } else {
    const assertion = response as AuthenticatorAssertionResponse;
    serializedResponse = {
      clientDataJSON: encode(assertion.clientDataJSON),
      authenticatorData: encode(assertion.authenticatorData),
      signature: encode(assertion.signature),
      ...(assertion.userHandle ? { userHandle: encode(assertion.userHandle) } : {}),
    };
  }
  return {
    id: publicKey.id,
    rawId: encode(publicKey.rawId),
    type: publicKey.type,
    ...(publicKey.authenticatorAttachment ? { authenticatorAttachment: publicKey.authenticatorAttachment } : {}),
    clientExtensionResults: extensionJSON(publicKey.getClientExtensionResults()),
    response: serializedResponse,
  };
}

export async function loginWithPasskey(signal: AbortSignal): Promise<LoginResponse> {
  const begin = await api<{ challenge_id: string; publicKey: RequestOptionsJSON }>("/auth/passkey/begin", { method: "POST", body: "{}", signal });
  signal.throwIfAborted();
  const credential = await navigator.credentials.get({
    signal,
    publicKey: {
      ...begin.publicKey,
      challenge: decode(begin.publicKey.challenge),
      allowCredentials: begin.publicKey.allowCredentials?.map((item) => ({ ...item, id: decode(item.id) })),
    },
  });
  signal.throwIfAborted();
  return api<LoginResponse>("/auth/passkey/finish", { method: "POST", body: JSON.stringify({ challenge_id: begin.challenge_id, credential: serialize(credential) }), signal });
}

export async function registerPasskey(name: string, signal: AbortSignal): Promise<{ passkey: Passkey }> {
  const begin = await api<{ challenge_id: string; publicKey: CreationOptionsJSON }>("/passkeys/register/begin", { method: "POST", body: JSON.stringify({ name }), signal });
  signal.throwIfAborted();
  const credential = await navigator.credentials.create({
    signal,
    publicKey: {
      ...begin.publicKey,
      challenge: decode(begin.publicKey.challenge),
      user: { ...begin.publicKey.user, id: decode(begin.publicKey.user.id) },
      excludeCredentials: begin.publicKey.excludeCredentials?.map((item) => ({ ...item, id: decode(item.id) })),
    },
  });
  signal.throwIfAborted();
  return api<{ passkey: Passkey }>("/passkeys/register/finish", { method: "POST", body: JSON.stringify({ challenge_id: begin.challenge_id, credential: serialize(credential) }), signal });
}

export function passkeyError(error: unknown): string {
  if (error !== null && typeof error === "object" && "name" in error && ["AbortError", "NotAllowedError"].includes(String(error.name))) return "Passkey 操作已取消或超时，可以重试或使用密码。";
  return errorMessage(error);
}
