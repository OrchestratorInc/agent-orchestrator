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
		expect(screen).toContain("if (!headerRightReady) return null;");
	});

	it("moves zoom, archive and the grid size into the header's overflow menu", () => {
		expect(screen).toContain('<Stack.Toolbar.Menu icon={toolbarIcon("more")} accessibilityLabel="Terminal actions"');
		expect(screen).toContain(">Smaller</Stack.Toolbar.MenuAction>");
		expect(screen).toContain(">Larger</Stack.Toolbar.MenuAction>");
		expect(screen).toContain(">Fit width</Stack.Toolbar.MenuAction>");
		expect(screen).toMatch(/<Stack\.Toolbar\.MenuAction key="close" destructive[^>]*onPress=\{\(\) => headerActions\.current\.confirmKill\(\)\}>/);
		expect(screen).toContain('{ label: "Archive session", symbol: "archivebox" as const }');
		// The old status row is gone; the connection speaks only while it is not live.
		expect(screen).not.toContain("styles.statusBar");
		expect(screen).not.toContain("zoomBtn");
		expect(screen).toContain('{status !== "open" && !dead ? (');
	});

	it("raises messages as platform notices that tell failures from news", () => {
		expect(screen).toContain("<TerminalNotice notice={notice} onDismiss={clearNotice} />");
		expect(screen).toContain("<TerminalNoticeHost notice={notice} onDismiss={clearNotice} bottomOffset={rootPad + dockHeight + space.sm} />");
		expect(screen).toContain('showNotice(TERMINAL_MODE_NOTICE, "info");');
		expect(screen).toContain('showNotice(REROUTED_NOTICE, "info");');
		expect(screen).toContain("showNotice(TERMINAL_UNAVAILABLE_NOTICE);");
		expect(screen).not.toContain("(tap to dismiss)");
		expect(screen).not.toContain("setBanner");
		expect(screen).not.toContain("t.amber");
	});

	it("keeps pinch and double-tap zoom, and adds a fit-to-width step", () => {
		expect(screen).toContain("window.__aoAdjustTerminalZoom = function (direction)");
		expect(screen).toContain("window.__aoFitTerminalWidth = function ()");
		expect(screen).toContain("if (Z.zoomed) setZoom(Z.min, 0, 0);");
		expect(screen).toContain("mode = 'pinch';");
	});
});
