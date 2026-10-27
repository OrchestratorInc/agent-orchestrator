import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const spawn = readFileSync(fileURLToPath(new URL("../app/spawn.tsx", import.meta.url)), "utf8");
const attachmentRow = readFileSync(fileURLToPath(new URL("./spawn-attachment-row.tsx", import.meta.url)), "utf8");
const voiceInput = readFileSync(fileURLToPath(new URL("./voice/useVoiceInput.ts", import.meta.url)), "utf8");
const layout = readFileSync(fileURLToPath(new URL("../app/_layout.tsx", import.meta.url)), "utf8");

// iOS keeps an empty native bar (no title text, no Cancel): the grabber and a
// swipe down close the sheet, and the keyboard-riding controls are placed
// against the bar. Android's Material sheet has neither bar nor title.
describe("spawn sheet header", () => {
	it("keeps an empty transparent bar on iOS, titled for VoiceOver", () => {
		const options = layout.slice(layout.indexOf('name="spawn"'), layout.indexOf("/>", layout.indexOf('name="spawn"')));
		expect(options).toContain('headerShown: Platform.OS === "ios",');
		expect(options).toContain('{ headerTransparent: true, headerStyle: undefined, headerTitle: "" }');
		expect(options).toContain('title: "New task",');
		expect(options).toContain('presentation: Platform.OS === "ios" ? "formSheet" : "transparentModal",');
	});

	it("has no Cancel, and starts the prompt below the empty bar", () => {
		expect(spawn).not.toContain("Stack.Toolbar");
		expect(spawn).not.toContain(">Cancel<");
		expect(spawn).toContain('Platform.OS === "ios" && { paddingTop: headerOverlap },');
		expect(spawn).toContain("return <View style={styles.screen}>{content}</View>;");
		// Start keeps its place with the selectors, riding the keyboard.
		expect(spawn).toContain("onSpawn={() => { void onSpawn(); }}");
	});


	// A started spawn can't be called back. Leaving mid-request used to let the
	// success path close the sheet a second time, popping the page under it.
	it("holds the sheet while a spawn runs and navigates only from a sheet still on top", () => {
		expect(spawn).toContain("if (!busy) return;\n\t\tnavigation.setOptions({ gestureEnabled: false });\n\t\treturn () => navigation.setOptions({ gestureEnabled: true });\n\t}, [busy, navigation]);");
		// Android's Material sheet: no drag, back press or outside tap mid-request.
		expect(spawn).toContain("enablePanDownToClose={!busy}");
		const success = spawn.slice(spawn.indexOf("const session = await spawn("), spawn.indexOf("} catch (e) {", spawn.indexOf("const session = await spawn(")));
		// Left mid-request, the sheet still lets go once the request settles:
		// otherwise coming back finds it locked, with Start stuck on Starting.
		const leftEarly = "if (!navigation.isFocused()) {\n\t\t\t\tsetBusy(false);\n\t\t\t\treturn;\n\t\t\t}";
		expect(success.indexOf(leftEarly)).toBeGreaterThan(0);
		expect(success.indexOf(leftEarly)).toBeLessThan(success.indexOf("backOr(router);"));
		expect(success.indexOf("backOr(router);")).toBeLessThan(success.indexOf('pathname: "/session/[id]"'));
	});
});

describe("spawn composer", () => {
	it("puts Standalone agent after projects with a section break", () => {
		expect(spawn).toContain('...projects.map((item) => ({ id: item.id, label: item.name, icon: "folder" as const })), { id: STANDALONE_PROJECT, label: "Standalone agent", icon: "message-square-plus" as const, sectionBreakBefore: true }');
		expect(spawn).not.toContain('label: "Standalone worker"');
	});

	it("reflows attachments and messages above keyboard-lifted controls on iOS", () => {
		expect(spawn).toContain('height={Platform.OS === "ios" ? promptRoom : undefined}');
		expect(spawn).toContain('Platform.OS === "ios" && keyboardHeight > 0 ? (');
		expect(spawn).toContain('height: keyboardHeight, marginTop: -space.sm');
		expect(spawn.indexOf('style={styles.messages}')).toBeLessThan(spawn.indexOf('height: keyboardHeight, marginTop: -space.sm'));
		expect(spawn.indexOf('height: keyboardHeight, marginTop: -space.sm')).toBeLessThan(spawn.indexOf('<KeyboardStickyView'));
		expect(spawn).toContain('promptHostFill: { height: undefined, flex: 1, minHeight: 0 }');
	});

	it("keeps the attachment row at its own height so the chips sit on the selectors", () => {
		expect(spawn).toContain("<SpawnAttachmentRow");
		expect(attachmentRow).toContain("<View collapsable={false} style={styles.row}>");
		expect(attachmentRow).toContain("row: { flexGrow: 0, flexShrink: 0 }");
	});

	it("shows a prompt-specific error for the daemon's 16 KiB rejection", () => {
		expect(spawn).toContain('e instanceof ApiError && e.code === "PROMPT_TOO_LONG"');
		expect(spawn).toContain('Task prompt is too long. Keep it to 16 KiB or fewer');
	});

	it("keeps iOS live dictation feedback with the keyboard-sticky controls", () => {
		expect(spawn).toContain('Platform.OS === "android" ? voiceFeedback : null');
		expect(spawn).toContain('<KeyboardStickyView offset={{ closed: 0, opened: 0 }}>\n\t\t\t\t{Platform.OS === "ios" ? voiceFeedback : null}');
		expect(spawn.indexOf('Platform.OS === "ios" ? voiceFeedback : null')).toBeLessThan(spawn.indexOf('<SpawnComposerControls'));
	});

	it("passes the hook's one-step latch to the mic", () => {
		expect(spawn).toContain("onToggleLatched: voice.toggleLatched }}");
	});

	it("keeps Start task disabled while a stopped dictation waits for its final result", () => {
		// stop() is asynchronous: the hook stays in recording until onFinal runs.
		const finish = voiceInput.slice(voiceInput.indexOf("const finish = useCallback"), voiceInput.indexOf("const pressIn = useCallback"));
		expect(finish).toContain('if (current !== "recording") return;');
		expect(finish).toContain("device.stop();");
		expect(finish.slice(finish.indexOf('if (current !== "recording") return;'))).not.toContain('setPhase("idle")');
		expect(spawn).toContain('disabled={!selectedHostId || !config || !projectId || !harness || busy || modelLoading || loading || listening || voice.state === "transcribing"}');
	});

	// The machine's catalog loads before a project is chosen, so a project is
	// not part of the guard.
	it("clears model loading when changing to a machine without a ready model request", () => {
		expect(spawn).toContain('if (!config || !requestedHostId || !harness) { setModelCatalog(undefined); setModelLoading(false); return; }');
	});
});

describe("spawn approval mode", () => {
	// An untouched picker shows the project's mode, which the daemon applies on
	// its own; only a mode picked on the sheet is sent as an override.
	it("sends only an approval mode the user picked", () => {
		expect(spawn).toContain("approvalMode: approvalChoice === approvalMode ? approvalChoice : undefined,");
		expect(spawn).toContain("const approvalMode = resolveApprovalMode({ harness, chosen: approvalChoice, projectMode: projectApproval });");
	});
});
