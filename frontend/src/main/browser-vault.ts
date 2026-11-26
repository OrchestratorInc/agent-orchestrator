// Pure building blocks for the local credential vault: sealing, TOTP, origin
// matching and redaction. Nothing here touches Electron, so tests and a future
// remote-machine filler can share it.
import { createCipheriv, createDecipheriv, createHmac, randomBytes } from "node:crypto";

export const SECRET_MASK = "••••";

type Sealed = { v: 1; iv: string; tag: string; ct: string };

/** Encrypts a JSON value with AES-256-GCM under `key` (32 bytes). */
export function seal(key: Buffer, value: unknown): string {
  const iv = randomBytes(12);
  const cipher = createCipheriv("aes-256-gcm", key, iv);
  const ct = Buffer.concat([cipher.update(JSON.stringify(value), "utf8"), cipher.final()]);
  const sealed: Sealed = {
    v: 1,
    iv: iv.toString("base64"),
    tag: cipher.getAuthTag().toString("base64"),
    ct: ct.toString("base64"),
  };
  return JSON.stringify(sealed);
}

/** Decrypts `seal` output; throws on a wrong key or any tampering. */
export function openSealed<T = unknown>(key: Buffer, sealed: string): T {
  const parsed = JSON.parse(sealed) as Sealed;
  if (parsed.v !== 1) throw new Error("unsupported vault format");
  const decipher = createDecipheriv("aes-256-gcm", key, Buffer.from(parsed.iv, "base64"));
  decipher.setAuthTag(Buffer.from(parsed.tag, "base64"));
  const plain = Buffer.concat([decipher.update(Buffer.from(parsed.ct, "base64")), decipher.final()]);
  return JSON.parse(plain.toString("utf8")) as T;
}

const BASE32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

export function decodeBase32(input: string): Buffer {
  const clean = input.replace(/[\s=-]/g, "").toUpperCase();
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const char of clean) {
    const index = BASE32.indexOf(char);
    if (index < 0) throw new Error("TOTP seed is not valid base32");
    value = (value << 5) | index;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return Buffer.from(out);
}

/** RFC 6238 TOTP (SHA-1, 30 s step, 6 digits), the default every site uses. */
export function totpCode(seed: string, nowMs: number = Date.now()): string {
  const key = decodeBase32(seed);
  if (!key.length) throw new Error("TOTP seed is empty");
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(nowMs / 1000 / 30)));
  const hmac = createHmac("sha1", key).update(counter).digest();
  const offset = hmac[hmac.length - 1] & 0x0f;
  const binary = hmac.readUInt32BE(offset) & 0x7fffffff;
  return String(binary % 1_000_000).padStart(6, "0");
}

/**
 * Normalizes a user-entered site to an exact origin. A bare host means https.
 * Only http(s) origins can hold credentials.
 */
export function normalizeVaultOrigin(input: string): string {
  const trimmed = input.trim();
  const withScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed) || /^(javascript|data|about|blob):/i.test(trimmed) ? trimmed : `https://${trimmed}`;
  const url = new URL(withScheme);
  if (url.protocol !== "https:" && url.protocol !== "http:") {
    throw new Error("Only http and https sites can hold credentials");
  }
  if (!url.hostname) throw new Error("Site has no host");
  return url.origin;
}

/**
 * Whether a secret may go to this origin: HTTPS, or plain HTTP only to this
 * computer (localhost, *.localhost, 127.0.0.0/8, [::1]) for local development.
 */
export function secureCredentialOrigin(origin: string): boolean {
  let url: URL;
  try {
    url = new URL(origin);
  } catch {
    return false;
  }
  if (url.protocol === "https:") return true;
  const host = url.hostname;
  return url.protocol === "http:" && (host === "localhost" || host.endsWith(".localhost") || host === "[::1]" || /^127(\.\d{1,3}){3}$/.test(host));
}

/** Exact origin match: scheme, host and port. No subdomain or suffix matching. */
export function originAllowed(itemOrigins: readonly string[], pageURL: string): boolean {
  let origin: string;
  try {
    const url = new URL(pageURL);
    if (url.protocol !== "https:" && url.protocol !== "http:") return false;
    origin = url.origin;
  } catch {
    return false;
  }
  return itemOrigins.includes(origin);
}

// Values shorter than this are too common to scrub without mangling output.
export const MIN_REDACT_LENGTH = 4;

export type Redactor = {
  add(secret: string): void;
  scrub<T>(value: T): T;
  size(): number;
};

/**
 * Remembers secrets filled into a session and masks them, in the encodings a
 * page or network capture would echo them in, from anything returned to the
 * agent.
 */
export function createRedactor(): Redactor {
  const needles = new Set<string>();
  const variants = (secret: string): string[] => {
    const json = JSON.stringify(secret).slice(1, -1);
    const uri = encodeURIComponent(secret);
    // application/x-www-form-urlencoded, as a browser posts a login form: it
    // also escapes !'()~ and turns spaces into +.
    const form = new URLSearchParams({ v: secret }).toString().slice(2);
    const lower = (encoded: string) => encoded.replace(/%[0-9A-F]{2}/g, (escape) => escape.toLowerCase());
    const html = (apostrophe: string) =>
      secret.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, apostrophe);
    const bytes = Buffer.from(secret, "utf8");
    return [
      secret,
      json,
      uri,
      lower(uri),
      form,
      lower(form),
      uri.replace(/%20/g, "+"),
      html("&#39;"),
      html("&#x27;"),
      html("&apos;"),
      bytes.toString("base64"),
      bytes.toString("base64url"),
    ];
  };
  let ordered: string[] = [];
  const scrubString = (text: string): string => {
    let out = text;
    for (const needle of ordered) {
      if (out.includes(needle)) out = out.split(needle).join(SECRET_MASK);
    }
    return out;
  };
  const walk = (value: unknown): unknown => {
    if (typeof value === "string") return scrubString(value);
    if (Array.isArray(value)) return value.map(walk);
    if (value && typeof value === "object" && !Buffer.isBuffer(value)) {
      const out: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(value)) out[k] = walk(v);
      return out;
    }
    return value;
  };
  return {
    add(secret) {
      if (secret.length < MIN_REDACT_LENGTH) return;
      for (const v of variants(secret)) if (v.length >= MIN_REDACT_LENGTH) needles.add(v);
      // Longest first so an encoding never leaves a partial match behind.
      ordered = [...needles].sort((a, b) => b.length - a.length);
    },
    scrub<T>(value: T): T {
      return ordered.length ? (walk(value) as T) : value;
    },
    size: () => needles.size,
  };
}
