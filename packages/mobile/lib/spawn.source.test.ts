import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const spawn = readFileSync(fileURLToPath(new URL("../app/spawn.tsx", import.meta.url)), "utf8");
const attachmentRow = readFileSync(fileURLToPath(new URL("./spawn-attachment-row.tsx", import.meta.url)), "utf8");
const voiceInput = readFileSync(fileURLToPath(new URL("./voice/useVoiceInput.ts", import.meta.url)), "utf8");
const layout = readFileSync(fileURLToPath(new URL("../app/_layout.tsx", import.meta.url)), "utf8");

// A new task is a page pushed over the tabs, laid out as a conversation that
// has not begun, rather than a sheet: its settings sit above a chat-style
// composer at the bottom, and starting it replaces the page with that
// conversation.
describe("spawn page", () => {
	it("is a plain pushed page titled New task", () => {
		const options = layout.slice(layout.indexOf('name="spawn"'), layout.indexOf("/>", layout.indexOf('name="spawn"')));
		expect(options).toContain('options={{ title: "New task" }}');
		expect(options).not.toContain("presentation");
		expect(spawn).not.toContain("@expo/ui/community/bottom-sheet");
		expect(spawn).toContain('export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";');
	});

	it("docks the composer at the bottom on the chat's keyboard avoidance", () => {
		expect(spawn).toContain("const chatKeyboard = useChatKeyboard();");
		expect(spawn).toContain("<KeyboardAvoidingView");
		expect(spawn).toContain("transform: [{ translateY: (restingInset - KEYBOARD_DOCK_GAP) * (dockRiseOverride ?? keyboard.progress.value) }],");
		expect(spawn).toContain("<Animated.View style={[styles.dock, { paddingBottom: restingInset }, dockRise]}>");
		expect(spawn.indexOf("<Pressable accessible={false} style={styles.room}")).toBeLessThan(spawn.indexOf("<SpawnComposerControls"));
	});

	// A started spawn can't be called back. The page holds until the request
	// settles, then becomes the session's conversation in place.
	it("holds the page while a spawn runs and replaces it only while it is on top", () => {
		expect(spawn).toContain("if (holding.current) event.preventDefault();");
		expect(spawn).toContain("if (!busy) return;\n\t\tnavigation.setOptions({ gestureEnabled: false });\n\t\treturn () => navigation.setOptions({ gestureEnabled: true });\n\t}, [busy, navigation]);");
		const success = spawn.slice(spawn.indexOf("const session = await spawn("), spawn.indexOf("} catch (e) {", spawn.indexOf("const session = await spawn(")));
		// Covered mid-request, the page still lets go once the request settles:
		// otherwise coming back finds it locked, with Start stuck on Starting.
		const leftEarly = "if (!navigation.isFocused()) {\n\t\t\t\tsetBusy(false);\n\t\t\t\treturn;\n\t\t\t}";
		expect(success.indexOf("holding.current = false;")).toBeLessThan(success.indexOf(leftEarly));
		expect(success.indexOf(leftEarly)).toBeLessThan(success.indexOf("router.replace({"));
		expect(success).toContain('pathname: "/session/[id]"');
		expect(spawn).not.toContain("backOr(router)");
	});
});

describe("spawn composer", () => {
	it("puts Standalone agent after projects with a section break", () => {
		expect(spawn).toContain('...projects.map((item) => ({ id: item.id, label: item.name, icon: "folder" as const })), { id: STANDALONE_PROJECT, label: "Standalone agent", icon: "message-square-plus" as const, sectionBreakBefore: true }');
		expect(spawn).not.toContain('label: "Standalone worker"');
	});

	it("hands the prompt and the attachments to the chat-style composer", () => {
		expect(spawn).toContain("prompt={prompt}\n\t\t\t\t\tonChangePrompt={setPrompt}");
		expect(spawn).toContain('placeholder={harnessLabel ? `Ask ${harnessLabel}` : "What should this worker do?"}');
		expect(spawn).toContain("attachments={<SpawnAttachmentRow");
		expect(spawn).toContain("hasAttachments={attachments.length > 0}");
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

	// Live dictation is drawn in the composer's field (see SpawnComposer); the
	// banner above it flashed "Keep holding…" on every tap.
	it("shows live dictation in the composer rather than a banner", () => {
		expect(spawn).not.toContain("voiceFeedback");
		expect(spawn).not.toContain("Keep holding");
		expect(spawn).toContain("partial: voice.partial, subscribeLevel: voice.subscribeLevel");
		expect(spawn).toContain("|| listening\n");
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

describe("spawn effort", () => {
	// The model's own default applies unless a level was picked here, and a
	// level belongs to the model it was picked for.
	it("sends only an effort the user picked, and forgets it when the model changes", () => {
		expect(spawn).toContain("effort: chosenEffort,");
		expect(spawn).toContain("const effortChoices = useMemo(() => spawnEffortChoices(effortModel, effort), [effort, effortModel]);");
		expect(spawn).toContain("const clearModelOverride = () => { setModel(\"\"); setModelTouched(false); setEffort(undefined); };");
		expect(spawn).toContain("if (nextModel !== model) setEffort(undefined);");
	});

	// Claude Code's catalog names no default, so the default model would hide
	// effort; a stand-in offers its levels, and picking one pins it so the
	// server can check the level against a named model.
	it("offers effort on the default model and pins the stand-in when a level is picked", () => {
		expect(spawn).toContain("const aliasModel = harness === \"claude-code\" && runningModel && !runningModel.efforts?.length ? claudeAliasModel(currentModels, runningModel.id) : undefined;");
		expect(spawn).toContain("const effortModel = aliasModel ?? runningModel ?? (displayedModel ? undefined : defaultEffortModel(harness, currentModels));");
		expect(spawn).toContain("if (!runningModel && effortModel) {");
		expect(spawn).toContain("onSelectEffort={selectEffort}");
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
