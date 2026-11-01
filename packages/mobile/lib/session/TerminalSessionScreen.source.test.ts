import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
const screen = source("./TerminalSessionScreen.tsx");

describe("the terminal screen's native chrome", () => {
	it("declares its header actions as native toolbar items, shared with Chat", () => {
		const buttons = source("./sessionHeaderButtons.tsx");
		expect(screen).toContain('<Stack.Toolbar placement="right">');
		expect(screen).toContain("{shellOnly ? null : sessionHeaderButtons({");
		expect(screen).toContain('switchTo: "chat",');
		expect(screen).toContain("const hasPreview = useSessionPreview(activeConfig, id, !shellOnly);");
		expect(buttons).toContain('icon={toolbarIcon(previewReady && ios ? PREVIEW_READY_GLYPH : "globe")}');
		expect(buttons).toContain('icon={toolbarIcon("fileSearch")}');
		// The preview-ready dot is the system badge, not a view placed by hand.
		expect(buttons).toContain("<Stack.Toolbar.Badge style={{ backgroundColor: t.green }} />");
		// iOS has no empty badge, so the globe changes shape there, not only colour.
		expect(buttons).toContain('export const PREVIEW_READY_GLYPH: ToolbarSymbol = { ios: "globe.americas.fill", android: "language" };');
		expect(screen).not.toContain("headerRight: () =>");
		expect(screen).not.toContain("browserReadyDot");
	});

	it("keeps the Chat switch's spinner and the swap's empty frame", () => {
		const buttons = source("./sessionHeaderButtons.tsx");
		expect(buttons).toMatch(/switchBusy \? \(\s*<Stack\.Toolbar\.View key="switch" hidesSharedBackground>/);
		expect(screen).toContain("switchBusy: chatSwitchBusy,");
		expect(screen).toContain("switchDisabled: interfaceTransitionActive,");
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
		// The grid size titles the iOS menu, not the bar button that opens it.
		expect(screen).toContain("{ios ? <Stack.Toolbar.Label>Terminal actions</Stack.Toolbar.Label> : null}");
		// The old status row is gone; the connection speaks only while it is not live.
		expect(screen).not.toContain("styles.statusBar");
		expect(screen).not.toContain("zoomBtn");
		expect(screen).toContain('{status !== "open" && !dead ? (');
	});

	it("keeps the overflow menu off a screen with no paired machine", () => {
		// Nothing there to zoom, and no notice row to explain a failed archive.
		expect(screen).toContain("const canClose = !unpaired && (shellOnly || !dead);");
		expect(screen).toContain("if (!dead && !unpaired) {");
		expect(screen).toMatch(/if \(unpaired\) \{\s*return \(\s*<View style=\{styles\.center\}>\s*\{headerItems\}/);
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

	it("names the interface-switch notice's close button for what it closes", () => {
		expect(screen).toMatch(/<NoticeDismissButton\s+accessibilityLabel="Dismiss interface switch error"\s+busy=\{interfaceSwitch\.acknowledgingNotice\}/);
		expect(screen).not.toContain('title="Dismiss"');
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

	it("keeps explaining a crashed terminal view for as long as it stays black", () => {
		// A snackbar would time out while the grid stays black, so it is screen state.
		expect(screen).toContain("onRenderProcessGone: () => setViewGone(true),");
		expect(screen).toMatch(/\{viewGone \? \(\s*(\/\/[^\n]*\n\s*)*<View style=\{styles\.deadOverlay\} accessibilityRole="alert">\s*<EmptyState\s+icon="warning"\s+iconColor=\{t\.red\}/);
		expect(screen).toContain('action={<Button title="Go back" onPress={leave} />}');
	});

	it("floats a native scroll-to-top button that drives the WebView", () => {
		// The jump stays in the page; the button, its colours and its visibility are React Native's.
		expect(screen).toContain("window.__aoScrollTop = function ()");
		expect(screen).toContain("'AO_SCROLLTOP ' + (show ? 1 : 0)");
		expect(screen).toContain("{canScrollTop && !dead && !viewGone ? <ScrollTopButton onPress={scrollToTop} /> : null}");
		expect(screen).not.toContain("ao-scrolltop");
		expect(screen).not.toContain("setInterval(update");
		expect(screen).not.toContain("rgba(16,17,20");
		expect(screen).not.toContain("no inject hook");
	});

	it("rechecks the button once a viewport scroll settles, momentum included", () => {
		// xterm suppresses its own onScroll for scrolls of the viewport element, so
		// the element's scroll events are read directly, a frame later.
		expect(screen).toContain("document.addEventListener('scroll', function (e) {");
		expect(screen).toContain("el.classList.contains('xterm-viewport')) updateNextFrame();");
		expect(screen).toContain("}, { capture: true, passive: true });");
		expect(screen).toContain("queued = requestAnimationFrame(function () { queued = 0; update(); });");
	});

	it("keeps pinch and double-tap zoom, and adds a fit-to-width step", () => {
		expect(screen).toContain("window.__aoAdjustTerminalZoom = function (direction)");
		expect(screen).toContain("window.__aoFitTerminalWidth = function ()");
		expect(screen).toContain("if (Z.zoomed) setZoom(Z.min, 0, 0);");
		expect(screen).toContain("mode = 'pinch';");
	});
});
