import { describe, expect, it } from "vitest";
import { pageHasTopbar } from "./shell-topbar";

const route = { isHomeRoute: false, isNewChatRoute: false, startingOrchestrator: false };

describe("pageHasTopbar", () => {
	it("is true for pages that draw a bar", () => {
		expect(pageHasTopbar(route)).toBe(true);
	});

	it("is false for home and new chat, whose panel reclaims the band", () => {
		expect(pageHasTopbar({ ...route, isHomeRoute: true })).toBe(false);
		expect(pageHasTopbar({ ...route, isNewChatRoute: true })).toBe(false);
	});

	it("is true again while an orchestrator is starting", () => {
		expect(pageHasTopbar({ ...route, isHomeRoute: true, startingOrchestrator: true })).toBe(true);
		expect(pageHasTopbar({ ...route, isNewChatRoute: true, startingOrchestrator: true })).toBe(true);
	});
});
