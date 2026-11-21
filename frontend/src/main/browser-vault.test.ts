import { randomBytes } from "node:crypto";
import { describe, expect, it } from "vitest";
import {
  SECRET_MASK,
  createRedactor,
  decodeBase32,
  normalizeVaultOrigin,
  openSealed,
  originAllowed,
  seal,
  totpCode,
} from "./browser-vault";

describe("vault sealing", () => {
  it("round-trips JSON under the data key", () => {
    const key = randomBytes(32);
    const sealed = seal(key, { password: "hunter2" });
    expect(sealed).not.toContain("hunter2");
    expect(openSealed(key, sealed)).toEqual({ password: "hunter2" });
  });

  it("rejects a wrong key and tampered ciphertext", () => {
    const key = randomBytes(32);
    const sealed = seal(key, { a: 1 });
    expect(() => openSealed(randomBytes(32), sealed)).toThrow();
    const parsed = JSON.parse(sealed);
    const ct = Buffer.from(parsed.ct, "base64");
    ct[0] ^= 1;
    parsed.ct = ct.toString("base64");
    expect(() => openSealed(key, JSON.stringify(parsed))).toThrow();
  });

  it("uses a fresh IV per seal", () => {
    const key = randomBytes(32);
    expect(seal(key, { a: 1 })).not.toEqual(seal(key, { a: 1 }));
  });
});

describe("TOTP", () => {
  // RFC 6238 appendix B, SHA-1 seed "12345678901234567890".
  const seed = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ";
  it.each([
    [59, "287082"],
    [1111111109, "081804"],
    [1234567890, "005924"],
    [2000000000, "279037"],
  ])("matches the RFC vector at t=%i", (seconds, code) => {
    expect(totpCode(seed, seconds * 1000)).toBe(code);
  });

  it("accepts lowercase, spaces and padding in the seed", () => {
    expect(decodeBase32("gezd gnbv=")).toEqual(decodeBase32("GEZDGNBV"));
    expect(() => decodeBase32("not*base32")).toThrow();
  });
});

describe("origin matching", () => {
  it("normalizes to scheme://host[:port]", () => {
    expect(normalizeVaultOrigin("https://Example.com/login?x=1")).toBe("https://example.com");
    expect(normalizeVaultOrigin("example.com")).toBe("https://example.com");
    expect(normalizeVaultOrigin("http://localhost:4173/")).toBe("http://localhost:4173");
    expect(normalizeVaultOrigin("localhost:4173")).toBe("https://localhost:4173");
    expect(normalizeVaultOrigin("https://bücher.de")).toBe("https://xn--bcher-kva.de");
    expect(() => normalizeVaultOrigin("javascript:alert(1)")).toThrow();
    expect(() => normalizeVaultOrigin("file:///etc/passwd")).toThrow();
  });

  const item = ["https://github.com", "http://localhost:4173"];
  it.each([
    ["https://github.com/login", true],
    ["https://github.com:443/session", true],
    ["https://gist.github.com/", false],
    ["https://github.com.evil.io/login", false],
    ["https://githuh.com/login", false],
    ["https://xn--gthub-n4a.com/", false],
    ["http://github.com/login", false],
    ["https://github.com:8443/", false],
    ["http://localhost:4173/login", true],
    ["http://localhost:4174/login", false],
    ["https://localhost:4173/login", false],
    ["about:blank", false],
    ["not a url", false],
  ])("%s -> %s", (url, ok) => {
    expect(originAllowed(item, url)).toBe(ok);
  });
});

describe("redactor", () => {
  it("masks every encoding of a filled secret anywhere in a result", () => {
    const r = createRedactor();
    r.add("p@ss word&1");
    const result = {
      text: 'textbox "Password": p@ss word&1',
      nested: [{ body: "user=a&password=p%40ss+word%261" }, "p%40ss%20word%261"],
      json: '{"password":"p@ss word&1"}',
      n: 3,
    };
    const out = r.scrub(result) as typeof result;
    expect(JSON.stringify(out)).not.toMatch(/p@ss|p%40ss/);
    expect(out.text).toBe(`textbox "Password": ${SECRET_MASK}`);
    expect(out.n).toBe(3);
  });

  it.each([
    ["raw", (v: string) => v],
    ["encodeURIComponent", (v: string) => encodeURIComponent(v)],
    ["form encoding (URLSearchParams)", (v: string) => new URLSearchParams({ v }).toString().slice(2)],
    ["lowercase percent escapes", (v: string) => new URLSearchParams({ v }).toString().slice(2).replace(/%[0-9A-F]{2}/g, (m) => m.toLowerCase())],
    ["JSON string escaping", (v: string) => JSON.stringify(v).slice(1, -1)],
    ["base64", (v: string) => Buffer.from(v).toString("base64")],
    ["base64url", (v: string) => Buffer.from(v).toString("base64url")],
    ["HTML escaping", (v: string) => v.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;")],
  ])("masks the %s form of a secret with !'()*~, spaces and quotes", (_name, encode) => {
    const secret = `Pa ss!'(w)*~"<&>9 ü`;
    const r = createRedactor();
    r.add(secret);
    const echoed = `before ${encode(secret)} after`;
    expect(r.scrub(echoed)).toBe(`before ${SECRET_MASK} after`);
  });

  it("ignores trivially short values and leaves results untouched when empty", () => {
    const r = createRedactor();
    r.add("ab");
    const value = { text: "ab cab" };
    expect(r.scrub(value)).toEqual(value);
  });
});
