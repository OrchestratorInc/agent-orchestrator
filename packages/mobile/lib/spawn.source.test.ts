import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const spawn = readFileSync(fileURLToPath(new URL("../app/spawn.tsx", import.meta.url)), "utf8");
const tray = readFileSync(fileURLToPath(new URL("./chat/ComposerAttachmentTray.tsx", import.meta.url)), "utf8");
const composer = readFileSync(fileURLToPath(new URL("./spawn-composer.tsx", import.meta.url)), "utf8");
const platformControls = ["ios", "android"].map((platform) => readFileSync(fileURLToPath(new URL(`./spawn-composer-controls.${platform}.tsx`, import.meta.url)), "utf8"));
const voiceInput = readFileSync(fileURLToPath(new URL("./voice/useVoiceInput.ts", import.meta.url)), "utf8");

describe("spawn page", () => {
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

	// The same tray as a conversation's composer, inside the pill over the
	// field rather than a row of chips stacked above it.
	it("draws attachments inside the composer pill, over the field", () => {
		expect(spawn).toContain("<ComposerAttachmentTray");
		const pill = composer.slice(composer.indexOf("<View style={styles.composer}>"));
		expect(pill.indexOf("{attachments}")).toBeGreaterThan(0);
		expect(pill.indexOf("{attachments}")).toBeLessThan(pill.indexOf("<TextInput"));
		for (const controls of platformControls) {
			expect(controls).toContain("attachments={attachments}");
			expect(controls).not.toContain("\t{attachments}\n");
		}
		expect(tray).toContain("<View collapsable={false} style={styles.tray}>");
		expect(tray).toContain("tray: { flexGrow: 0, flexShrink: 0 }");
	});

	it("shows a prompt-specific error for the daemon's 100 KiB rejection", () => {
		expect(spawn).toContain('e instanceof ApiError && e.code === "PROMPT_TOO_LONG"');
		expect(spawn).toContain('Task prompt is too long. Keep it to 100 KiB or fewer');
	});

	// Live dictation is drawn in the composer's field (see SpawnComposer); the
	// banner above it flashed "Keep holding…" on every tap.
	it("shows live dictation in the composer and hands the mic the one-step latch", () => {
		expect(spawn).not.toContain("voiceFeedback");
		expect(spawn).toContain("partial: voice.partial, subscribeLevel: voice.subscribeLevel");
		expect(spawn).toContain("onToggleLatched: voice.toggleLatched }}");
	});

	it("keeps Start task disabled while a stopped dictation waits for its final result", () => {
		// stop() is asynchronous: the hook stays in recording until onFinal runs.
		const finish = voiceInput.slice(voiceInput.indexOf("const finish = useCallback"), voiceInput.indexOf("const pressIn = useCallback"));
		expect(finish).toContain('if (current !== "recording") return;');
		expect(finish).toContain("device.stop();");
		expect(finish.slice(finish.indexOf('if (current !== "recording") return;'))).not.toContain('setPhase("idle")');
		expect(spawn).toContain('disabled={!selectedHostId || (!config && !cloudSelected) || !projectId || !harness || busy || modelLoading || loading || listening || voice.state === "transcribing" || Boolean(planBlock)}');
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
		expect(spawn).toContain("if (nextModel !== model) setEffort(undefined);");
	});

	// Claude Code's catalog names no default, so the default model would hide
	// effort; a stand-in offers its levels, and picking one pins it.
	it("offers effort on the default model and pins the stand-in when a level is picked", () => {
		expect(spawn).toContain("const effortModel = aliasModel ?? runningModel ?? (displayedModel ? undefined : defaultEffortModel(harness, currentModels));");
		expect(spawn).toContain("if (!runningModel && effortModel) {");
	});

	// With no model sent, the stand-in's fallback level is not sent either: the
	// daemon has no model to check it against (#7979).
	it("sends no stand-in fallback effort with the default model", () => {
		expect(spawn).toContain("const usingStandIn = !runningModel && effortModel !== undefined;");
		expect(spawn).toContain("const chosenEffort = spawnEffortToSend(effortChoices, effort, effortModel, usingStandIn);");
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
