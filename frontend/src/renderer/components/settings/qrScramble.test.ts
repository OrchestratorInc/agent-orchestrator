import { describe, expect, it } from "vitest";
import { PAIRING_LINK_BASE_FOR_SCRAMBLE, scramblePairingCodes } from "./qrScramble";
import { buildPairingOffer, pairingCodeUrl } from "../../lib/pairing-payload";

describe("scramble payloads for the pairing placeholder", () => {
	it("produces the requested number of codes", () => {
		expect(scramblePairingCodes(5)).toHaveLength(5);
	});

	// Deterministic: React re-renders must not reshuffle the stack, or the
	// crossfade turns into flicker.
	it("is stable across calls", () => {
		expect(scramblePairingCodes(4)).toEqual(scramblePairingCodes(4));
	});

	it("gives every layer a different pattern", () => {
		expect(new Set(scramblePairingCodes(6)).size).toBe(6);
	});

	// The whole point: the placeholder has to encode a payload of the same
	// shape and length as a real offer, so the encoder picks the same QR
	// version. A different version means a different module count, and the
	// resolve would visibly jump from a coarse grid to a fine one. Lengths
	// are compared at the real offer's field sizes, so one byte of base64
	// drift is the most the decoy can be off by.
	it("matches the length of a real pairing code, field for field", () => {
		const host = "192.168.1.12";
		const real = pairingCodeUrl(
			buildPairingOffer({
				endpoints: [
					{ kind: "lan", host, port: 3011, secure: false },
					{ kind: "tunnel", host: "stem-til-house-doing.trycloudflare.com", port: 443, secure: true },
				],
				password: "ai0OGmFX",
				hostId: "h_70884810-088d-4b10-b3ab-0b19c47bb17c",
				name: host,
				platform: "",
			}),
			PAIRING_LINK_BASE_FOR_SCRAMBLE,
		);
		const shape = { hostIdLength: 38, nameLength: host.length, tokenLength: 8 };
		for (const code of scramblePairingCodes(6, shape)) {
			expect(Math.abs(code.length - real.length)).toBeLessThanOrEqual(8);
		}
	});

	it("grows with the real hostId so a long id cannot leave the decoy a version short", () => {
		const short = scramblePairingCodes(1, { hostIdLength: 6 })[0];
		const long = scramblePairingCodes(1, { hostIdLength: 38 })[0];
		expect(long.length).toBeGreaterThan(short.length);
	});

	it("carries no real credential material", () => {
		for (const code of scramblePairingCodes(4)) {
			expect(code.startsWith(`${PAIRING_LINK_BASE_FOR_SCRAMBLE}#`)).toBe(true);
			expect(code).not.toContain("trycloudflare.com");
		}
	});
});
