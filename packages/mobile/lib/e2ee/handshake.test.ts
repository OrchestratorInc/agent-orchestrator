import { ed25519, x25519 } from "@noble/curves/ed25519.js";
import { describe, expect, it } from "vitest";
import { decodeHostKey, startHandshake } from "./handshake";

const seq = (start: number) => Uint8Array.from({ length: 32 }, (_, i) => start + i);
const hex = (bytes: Uint8Array) => Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
const unhex = (text: string) => Uint8Array.from(text.match(/../g) ?? [], (b) => parseInt(b, 16));

// The same values as TestE2EETestVector in backend/internal/httpd/lan_e2ee_test.go.
const vector = {
	hostKey: "2543b92ff1095511476adc8369db6ddc933665a11978dda1404ee1066ca9559d",
	signature: "81ed928ebe753e3473a964e208f2672176e9589e3ca93b6b336bb63a0d39e0e6b9c9521010e2a48574a44d22fd652328418ec0f6f51d9aa4169c8516c2822605",
	firstToHost: "1c81cef78476ac94861f2e435da04c39209f4090f9",
	secondToHost: "193168b762511db53e75f5e7c5ede3d01c759f22a2",
	firstToPhone: "e5a4f93bdf8d52be21ddaa2d8a42aacef7fbd0c87a",
};

function hostReply(signature = unhex(vector.signature)) {
	const reply = new Uint8Array(97);
	reply[0] = 1;
	reply.set(x25519.getPublicKey(seq(0x20)), 1);
	reply.set(signature, 33);
	return reply;
}

describe("startHandshake", () => {
	it("derives the same records as the host", () => {
		expect(hex(ed25519.getPublicKey(seq(0x40)))).toBe(vector.hostKey);
		const handshake = startHandshake(seq(0x00));
		expect(handshake.hello[0]).toBe(1);
		const { toHost, toPhone } = handshake.finish(hostReply(), unhex(vector.hostKey));
		const hello = new TextEncoder().encode("hello");
		expect(hex(toHost.seal(hello))).toBe(vector.firstToHost);
		expect(hex(toHost.seal(hello))).toBe(vector.secondToHost);
		expect(new TextDecoder().decode(toPhone.open(unhex(vector.firstToPhone)))).toBe("hello");
	});

	it("refuses a reply another key signed", () => {
		const other = ed25519.getPublicKey(seq(0x60));
		expect(() => startHandshake(seq(0x00)).finish(hostReply(), other)).toThrow("did not sign");
	});

	it("refuses a record that was changed on the way", () => {
		const { toPhone } = startHandshake(seq(0x00)).finish(hostReply(), unhex(vector.hostKey));
		const record = unhex(vector.firstToPhone);
		record[0] ^= 1;
		expect(() => toPhone.open(record)).toThrow();
	});
});

describe("decodeHostKey", () => {
	it("reads unpadded base64url and rejects anything else", () => {
		expect(hex(decodeHostKey("JUO5L_EJVRFHatyDadtt3JM2ZaEZeN2hQE7hBmypVZ0") ?? new Uint8Array())).toBe(vector.hostKey);
		expect(decodeHostKey("JUO5L_EJVRFHatyDadtt3JM2ZaEZeN2hQE7hBmypVZ0=")).toBeNull();
		expect(decodeHostKey("")).toBeNull();
	});
});
