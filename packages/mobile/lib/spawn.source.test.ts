import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const spawn = readFileSync(fileURLToPath(new URL("../app/spawn.tsx", import.meta.url)), "utf8");
const attachmentRow = readFileSync(fileURLToPath(new URL("./spawn-attachment-row.tsx", import.meta.url)), "utf8");
const voiceInput = readFileSync(fileURLToPath(new URL("./voice/useVoiceInput.ts", import.meta.url)), "utf8");
const layout = readFileSync(fileURLToPath(new URL("../app/_layout.tsx", import.meta.url)), "utf8");

// The iOS sheet says what it is and how to leave it without a drag: a native
// bar titled "New task" with Cancel across from the title. Android keeps its
// Material bottom sheet, which has neither bar nor title.
describe("spawn sheet header", () => {
	it("shows the iOS bar from the first frame, titled New task", () => {
		const options = layout.slice(layout.indexOf('name="spawn"'), layout.indexOf("/>", layout.indexOf('name="spawn"')));
		expect(options).toContain('headerShown: Platform.OS === "ios",');
		expect(options).toContain('title: "New task",');
		expect(options).toContain('presentation: Platform.OS === "ios" ? "formSheet" : "transparentModal",');
	});

	it("cancels from the leading bar item on iOS only", () => {
		expect(spawn).toContain('const headerItems = useMemo(() => Platform.OS === "ios" ? (\n\t\t<Stack.Toolbar placement="left">\n\t\t\t<Stack.Toolbar.Button disabled={busy} onPress={() => backOr(router)}>Cancel</Stack.Toolbar.Button>');
		expect(spawn).toContain(") : null, [busy, router]);");
		expect(spawn).toContain("return <View style={styles.screen}>{headerItems}{content}</View>;");
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
		expect(success.indexOf("if (!navigation.isFocused()) return;")).toBeGreaterThan(0);
		expect(success.indexOf("if (!navigation.isFocused()) return;")).toBeLessThan(success.indexOf("backOr(router);"));
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

	it("keeps Start task disabled while a stopped dictation waits for its final result", () => {
		// stop() is asynchronous: the hook stays in recording until onFinal runs.
		const finish = voiceInput.slice(voiceInput.indexOf("const finish = useCallback"), voiceInput.indexOf("const pressIn = useCallback"));
		expect(finish).toContain('if (current !== "recording") return;');
		expect(finish).toContain("device.stop();");
		expect(finish.slice(finish.indexOf('if (current !== "recording") return;'))).not.toContain('setPhase("idle")');
		expect(spawn).toContain('disabled={!selectedHostId || !config || !projectId || !harness || busy || modelLoading || loading || listening || voice.state === "transcribing"}');
	});

	it("clears model loading when changing to a machine without a ready model request", () => {
		expect(spawn).toContain('if (!config || !requestedHostId || !projectId || !harness) { setModelCatalog(undefined); setModelLoading(false); return; }');
	});
});
