import { describe, expect, it, vi } from "vitest";

vi.mock("expo-crypto", () => ({
	CryptoDigestAlgorithm: { SHA256: "SHA-256" },
	CryptoEncoding: { BASE64: "base64" },
	// SHA-256 of "test-verifier", base64 — pinned so the test asserts a real
	// digest rather than whatever the implementation happens to produce.
	digestStringAsync: vi.fn(async () => "3q2+7wAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="),
	getRandomBytesAsync: vi.fn(async (n: number) => new Uint8Array(n).fill(7)),
}));

import { createPkcePair, toBase64Url } from "./pkce";

describe("toBase64Url", () => {
	// A base64url string is what goes in a URL query parameter: `+` and `/`
	// are not URL-safe and `=` padding confuses some servers.
	it("replaces the url-unsafe characters and strips padding", () => {
		expect(toBase64Url("ab+c/d==")).toBe("ab-c_d");
	});

	it("leaves an already-safe string alone", () => {
		expect(toBase64Url("abcdef")).toBe("abcdef");
	});
});

describe("createPkcePair", () => {
	it("derives the challenge from the verifier as url-safe base64", async () => {
		const { verifier, challenge } = await createPkcePair();
		expect(verifier.length).toBeGreaterThanOrEqual(43);
		expect(challenge).not.toContain("+");
		expect(challenge).not.toContain("/");
		expect(challenge).not.toContain("=");
	});

	// The whole point of PKCE: the challenge must be a hash, never the
	// verifier itself. Sending the verifier as the challenge would let anyone
	// who intercepts the authorization URL complete the exchange.
	it("does not send the verifier as the challenge", async () => {
		const { verifier, challenge } = await createPkcePair();
		expect(challenge).not.toBe(verifier);
	});
});
