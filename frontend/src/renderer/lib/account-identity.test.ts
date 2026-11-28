import { describe, expect, it } from "vitest";
import { ACCOUNT_COLORS, accountAbbreviation, accountLetters } from "./account-identity";

describe("accountAbbreviation", () => {
	it("takes the first letter of the first two words", () => {
		expect(accountAbbreviation({ name: "Cristian Account", provider: "claude" })).toBe("CA");
		expect(accountAbbreviation({ name: "one two three", provider: "claude" })).toBe("OT");
	});
	it("takes the first two characters of a single word", () => {
		expect(accountAbbreviation({ name: "Work", provider: "claude" })).toBe("WO");
		expect(accountAbbreviation({ name: "x", provider: "claude" })).toBe("X");
	});
	it("falls back to the email local part, then the provider label", () => {
		expect(accountAbbreviation({ name: "", email: "dana.k@example.com", provider: "codex" })).toBe("DA");
		expect(accountAbbreviation({ email: "@example.com", provider: "codex" })).toBe("CO");
		expect(accountAbbreviation({ provider: "claude" })).toBe("CL");
	});
	it("trims and splits on any whitespace", () => {
		expect(accountAbbreviation({ name: "  \t Ana \n  Bea  ", provider: "claude" })).toBe("AB");
		expect(accountAbbreviation({ name: "   ", email: "zed@x.io", provider: "claude" })).toBe("ZE");
	});
	it("counts code points, not UTF-16 units", () => {
		expect(accountAbbreviation({ name: "😀😎 team", provider: "claude" })).toBe("😀T");
		expect(accountAbbreviation({ name: "😀😎x", provider: "claude" })).toBe("😀😎");
		expect(accountAbbreviation({ name: "ñandú", provider: "claude" })).toBe("ÑA");
	});
});

describe("ACCOUNT_COLORS", () => {
	it("lists the eight palette ids", () => {
		expect(ACCOUNT_COLORS).toEqual(["red", "orange", "amber", "green", "teal", "blue", "violet", "pink"]);
	});
});

describe("accountLetters", () => {
	const claude = (id: string, fields: { name?: string; email?: string } = {}) => ({ id, provider: "claude", ...fields });

	it("keeps each account's own letters when no other account has them", () => {
		expect(accountLetters([claude("a", { email: "dana@example.com" }), claude("b", { name: "Work laptop" })])).toEqual(new Map([["a", "DA"], ["b", "WL"]]));
	});

	it("gives a later account with the same letters the next candidate from its email", () => {
		const letters = accountLetters([claude("a", { email: "ronish.rohan@gmail.com" }), claude("b", { email: "ronish@untrivial.ai" })]);
		expect(letters).toEqual(new Map([["a", "RO"], ["b", "RU"]]));
		// Words in the email come before its domain.
		expect(accountLetters([claude("a", { email: "ronish@untrivial.ai" }), claude("b", { email: "ronish.rohan@gmail.com" })]).get("b")).toBe("RR");
	});

	it("numbers accounts once every candidate is taken", () => {
		const letters = accountLetters([claude("a", { name: "Work" }), claude("b", { name: "Work" }), claude("c", { name: "Work" })]);
		expect([...letters.values()]).toEqual(["WO", "W2", "W3"]);
	});

	it("gives API keys without an email distinct letters", () => {
		const letters = accountLetters([claude("a"), claude("b"), claude("c", { name: "Key" })]);
		expect(new Set(letters.values()).size).toBe(3);
	});

	it("lets the computer's own login pick last, so its sign-in never moves another account's letters", () => {
		const saved = claude("b", { email: "ronish@untrivial.ai" });
		const signedIn = accountLetters([{ ...claude("device-claude", { email: "ronish.rohan@gmail.com" }), device: true }, saved]);
		const signedOut = accountLetters([{ ...claude("device-claude"), device: true }, saved]);
		expect(signedIn).toEqual(new Map([["b", "RO"], ["device-claude", "RR"]]));
		expect(signedOut.get("b")).toBe("RO");
	});

	it("lets accounts of different providers share letters", () => {
		const letters = accountLetters([claude("a", { email: "dana@example.com" }), { id: "b", provider: "codex", email: "dana@example.com" }]);
		expect(letters.get("a")).toBe("DA");
		expect(letters.get("b")).toBe("DA");
	});
});
