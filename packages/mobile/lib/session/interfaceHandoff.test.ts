import { describe, expect, it } from "vitest";
import { interfaceHandoffPending } from "./interfaceTransition";

const transition = (phase: string, targetMode: "chat" | "tui" = "chat") => ({ id: "t-1", phase, targetMode });

describe("interfaceHandoffPending", () => {
	it("holds the card once a switch this screen watched has completed", () => {
		expect(interfaceHandoffPending(transition("completed"), "tui", "t-1")).toBe(true);
	});

	it("does not bring the card back for a switch the screen never watched", () => {
		expect(interfaceHandoffPending(transition("completed"), "tui", undefined)).toBe(false);
		expect(interfaceHandoffPending(transition("completed"), "tui", "t-0")).toBe(false);
	});

	it("is over once the route shows the target mode", () => {
		expect(interfaceHandoffPending(transition("completed", "tui"), "tui", "t-1")).toBe(false);
	});

	it.each(["failed", "cancelled", "recovery_required", "activating"])("is not pending for a %s switch", (phase) => {
		expect(interfaceHandoffPending(transition(phase), "tui", "t-1")).toBe(false);
	});
});
