import { describe, expect, it } from "vitest";
import { isSimplifiedTopbar } from "./simplified-topbar";

describe("isSimplifiedTopbar", () => {
	it("is a Developer Mode preview", () => {
		expect(isSimplifiedTopbar({ developerMode: false })).toBe(false);
		expect(isSimplifiedTopbar({})).toBe(false);
		expect(isSimplifiedTopbar({ developerMode: true })).toBe(true);
	});

	it("can be switched off inside Developer Mode", () => {
		expect(isSimplifiedTopbar({ developerMode: true, simplifiedTopbar: false })).toBe(false);
		expect(isSimplifiedTopbar({ developerMode: true, simplifiedTopbar: true })).toBe(true);
	});

	it("ignores the switch outside Developer Mode", () => {
		expect(isSimplifiedTopbar({ developerMode: false, simplifiedTopbar: true })).toBe(false);
	});
});
