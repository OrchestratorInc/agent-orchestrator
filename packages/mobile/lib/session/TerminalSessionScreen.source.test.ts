import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
const screen = source("./TerminalSessionScreen.tsx");

describe("the terminal screen's native chrome", () => {
	it("declares its header actions as native toolbar items", () => {
		expect(screen).toContain('<Stack.Toolbar placement="right">');
		expect(screen).toContain('icon={toolbarIcon("chat")}');
		expect(screen).toContain('icon={toolbarIcon("globe")}');
		expect(screen).toContain('icon={toolbarIcon("fileSearch")}');
		// The preview-ready dot is the system badge, not a view placed by hand.
		expect(screen).toContain("<Stack.Toolbar.Badge style={{ backgroundColor: t.green }} />");
		expect(screen).not.toContain("headerRight: () =>");
		expect(screen).not.toContain("browserReadyDot");
	});

	it("keeps the Chat switch's spinner and the swap's empty frame", () => {
		expect(screen).toMatch(/chatSwitchBusy \? \(\s*<Stack\.Toolbar\.View hidesSharedBackground>/);
		expect(screen).toContain("disabled={interfaceTransitionActive}");
		expect(screen).toContain("resetHeaderRightForSwap(");
		expect(screen).toContain("if (shellOnly || !headerRightReady) return null;");
	});
});
