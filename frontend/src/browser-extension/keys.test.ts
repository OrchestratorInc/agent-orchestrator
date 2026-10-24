import { describe, expect, it } from "vitest";
import { keyEvents } from "./keys";

describe("keyEvents", () => {
	it("types printable keys with text", () => {
		expect(keyEvents("a")).toEqual([
			{ type: "keyDown", key: "a", code: "KeyA", windowsVirtualKeyCode: 65, modifiers: 0, text: "a", unmodifiedText: "a" },
			{ type: "keyUp", key: "a", code: "KeyA", windowsVirtualKeyCode: 65, modifiers: 0 },
		]);
		expect(keyEvents("Enter")[0]).toMatchObject({ type: "keyDown", key: "Enter", text: "\r" });
		expect(keyEvents("ArrowDown")[0]).toMatchObject({ type: "rawKeyDown", key: "ArrowDown", windowsVirtualKeyCode: 40 });
	});

	it("wraps chords in modifier presses and adds editing commands", () => {
		const events = keyEvents("Control+A");
		expect(events.map((event) => [event.type, event.key, event.modifiers])).toEqual([
			["rawKeyDown", "Control", 2],
			["rawKeyDown", "a", 2],
			["keyUp", "a", 2],
			["keyUp", "Control", 0],
		]);
		expect(events[1]).toMatchObject({ commands: ["selectAll"] });
		expect(events[1]).not.toHaveProperty("text");
		expect(keyEvents("Shift+a")[1]).toMatchObject({ key: "A", text: "A", modifiers: 8 });
		expect(keyEvents("cmd+z")[1]).toMatchObject({ commands: ["undo"], modifiers: 4 });
	});

	it("rejects unknown keys and modifiers", () => {
		expect(() => keyEvents("NotAKey")).toThrow(expect.objectContaining({ code: "INVALID_ARGUMENT" }));
		expect(() => keyEvents("Hyper+a")).toThrow(expect.objectContaining({ code: "INVALID_ARGUMENT" }));
		expect(() => keyEvents("")).toThrow(expect.objectContaining({ code: "INVALID_ARGUMENT" }));
	});
});
