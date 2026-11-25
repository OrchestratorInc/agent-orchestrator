import { describe, expect, it } from "vitest";
import { ACCOUNT_COLORS, accountAbbreviation } from "./account-identity";

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
