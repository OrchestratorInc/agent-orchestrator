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

	it("asks how a busy session switches through the platform's own choice", () => {
		expect(screen).toContain("<InterfaceSwitchConfirm message={switchQuestion} onChoose={answerSwitchQuestion} onCancel={closeSwitchQuestion} />");
		expect(screen).not.toContain('Alert.alert(\n\t\t\t"Switch to Chat?"');
	});

	it("shows the switch in progress with the system spinner", () => {
		expect(screen).toMatch(/\{interfaceRecoveryMessage \? \(\s*<AppSymbol name="warning"[^>]*\/>\s*\) : \(\s*<Spinner size="large" \/>/);
		expect(screen).not.toContain('"repeat"');
		expect(screen).toContain('<Button\n\t\t\t\t\t\t\t\t\tvariant="secondary"\n\t\t\t\t\t\t\t\t\ttitle="Cancel switch"');
	});

	it("draws a dead terminal with the shared empty state and its Restore button", () => {
		expect(screen).toContain("<EmptyState\n\t\t\t\t\t\t\ticon={TERMINATED_GLYPH}");
		expect(screen).toContain('<Button title="Restore session" icon="refresh" loading={restoring} onPress={() => void onRestore()} />');
		expect(screen).not.toContain("deadIcon");
		expect(screen).not.toContain("restoreCta");
	});

	it("floats a native scroll-to-top button that drives the WebView", () => {
		// The jump stays in the page; the button, its colours and its visibility are React Native's.
		expect(screen).toContain("window.__aoScrollTop = function ()");
		expect(screen).toContain("'AO_SCROLLTOP ' + (show ? 1 : 0)");
		expect(screen).toContain("{canScrollTop && !dead ? <ScrollTopButton onPress={scrollToTop} /> : null}");
		expect(screen).not.toContain("ao-scrolltop");
		expect(screen).not.toContain("setInterval(update");
		expect(screen).not.toContain("rgba(16,17,20");
		expect(screen).not.toContain("no inject hook");
	});

	it("keeps pinch and double-tap zoom, and adds a fit-to-width step", () => {
		expect(screen).toContain("window.__aoAdjustTerminalZoom = function (direction)");
		expect(screen).toContain("window.__aoFitTerminalWidth = function ()");
		expect(screen).toContain("if (Z.zoomed) setZoom(Z.min, 0, 0);");
		expect(screen).toContain("mode = 'pinch';");
	});
});
