import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

const read = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

it("hides the update screen's invisible half from VoiceOver as well as TalkBack", () => {
	const screen = read("./UpdateScreen.tsx");
	// Both halves stay mounted and only swap opacity, so each needs the iOS prop
	// beside Android's importantForAccessibility, or VoiceOver still reads (and
	// activates) the half that cannot be seen.
	expect(screen).toMatch(/accessibilityElementsHidden=\{!restarting\}\s+importantForAccessibility=\{restarting \? "auto" : "no-hide-descendants"\}/);
	expect(screen).toMatch(/accessibilityElementsHidden=\{restarting\}\s+importantForAccessibility=\{restarting \? "no-hide-descendants" : "auto"\}/);
});
