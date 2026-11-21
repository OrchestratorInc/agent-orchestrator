import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const spawn = readFileSync(fileURLToPath(new URL("../app/spawn.tsx", import.meta.url)), "utf8");
const tray = readFileSync(fileURLToPath(new URL("./chat/ComposerAttachmentTray.tsx", import.meta.url)), "utf8");

/** Both are in `text`, `first` before `then`. */
function expectBefore(text: string, first: string, then: string) {
	expect(text).toContain(first);
	expect(text).toContain(then);
	expect(text.indexOf(first)).toBeLessThan(text.indexOf(then));
}
const composer = readFileSync(fileURLToPath(new URL("./spawn-composer.tsx", import.meta.url)), "utf8");
const platformControls = ["ios", "android"].map((platform) => readFileSync(fileURLToPath(new URL(`./spawn-composer-controls.${platform}.tsx`, import.meta.url)), "utf8"));
const voiceInput = readFileSync(fileURLToPath(new URL("./voice/useVoiceInput.ts", import.meta.url)), "utf8");

/** The text from `start` up to `end`; both must be there, or the slice would be the wrong text. */
function between(text: string, start: string, end: string): string {
	const from = text.indexOf(start);
	expect(from, start).toBeGreaterThanOrEqual(0);
	const to = text.indexOf(end, from);
	expect(to, end).toBeGreaterThan(from);
	return text.slice(from, to);
}

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
		expect(spawn).toContain('if (!requestedHostId || !harness) { loadedFor.current.models = undefined; setModelCatalog(undefined); setModelLoading(false); return; }');
		// A machine whose connection is away (or not up yet) stops loading too, and keeps what it showed.
		expect(spawn).toContain('if (!config) { setModelLoading(false); return; }');
	});

	it("keeps the pickers through an app switch, loading once per machine rather than per connection", () => {
		expect(spawn).toContain("if (loadedFor.current.catalog === wanted) return;");
		expect(spawn).toContain("if (!config || loadedFor.current.project === wanted) return;");
		expect(spawn).toContain("}, [cloudSelected, connected, epoch, reloadKey, selectedHostId]);");
	});

	// Cleared is not loaded: A, B, then A again loads A rather than skipping it
	// and leaving the menu empty with Start disabled.
	it("forgets a load once what it loaded is cleared", () => {
		expect(spawn).toContain("const resetModelSource = () => { clearModelOverride(); loadedFor.current.models = undefined; setModelCatalog(undefined); setModelError(undefined); };");
		expect(spawn).toContain("if (!requestedHostId || !projectId || projectId === STANDALONE_PROJECT) { loadedFor.current.project = undefined; setProjectDetail(undefined); setProjectDetailLoadedFor(null); return; }");
		const changeProject = between(spawn, "const changeProject = ", "const selectAgent = ");
		expect(changeProject.indexOf("loadedFor.current.project = undefined;")).toBeGreaterThan(0);
		expect(changeProject.indexOf("setProjectDetail(undefined);")).toBeGreaterThan(0);
		expect(changeProject.indexOf("loadedFor.current.project = undefined;")).toBeLessThan(changeProject.indexOf("setProjectDetail(undefined);"));
		const chooseHost = between(spawn, "const chooseHost = ", "const changeProject = ");
		expect(chooseHost).toContain("loadedFor.current.catalog = undefined;");
		expect(chooseHost).toContain("loadedFor.current.project = undefined;");
	});

	// The same connection back after an app switch loads nothing; another
	// endpoint or credential (an upgrade race) loads every picker through it.
	it("loads the pickers again only when the connection really changed", () => {
		expect(spawn).toContain("connectionEpoch.current = nextConnectionEpoch(connectionEpoch.current, config);");
		for (const key of ["`${catalogGeneration.current}|${epoch}|${requestedHostId}|${reloadKey}`", "`${catalogGeneration.current}|${epoch}|${requestedHostId}|${projectId}|${reloadKey}`", "`${catalogGeneration.current}|${epoch}|${requestedHostId}|${harness}|${projectId ?? \"\"}|${reloadKey}`", "`${catalogGeneration.current}|${epoch}|${requestedHostId}`"]) {
			expect(spawn).toContain(key);
		}
		expect(spawn).toContain("}, [cloudSelected, connected, epoch, projectId, reloadKey, selectedHostId]);");
		expect(spawn).toContain("}, [agentIds, cloudSelected, connected, epoch, reloadKey, selectedHostId]);");
		expect(spawn).toContain("}, [cloudSelected, connected, epoch, harness, projectId, reloadKey, selectedHostId]);");
	});

	// A failed agent's list is asked again on the next load; the ones that loaded are not.
	it("asks again only for the agent model lists that failed", () => {
		const effect = between(spawn, "const cache = agentModelCache.current;", "}, [agentIds, cloudSelected, connected, epoch, reloadKey, selectedHostId]);");
		expect(effect).toContain("void Promise.all(missing.map(async (id) => {");
		expect(effect).toContain("return [id, undefined] as const;");
		expect(effect).toContain("for (const [id, models] of entries) if (models) cache.record(scope, id, models);");
		expect(effect).not.toContain("loadedFor");
	});
});

describe("spawn offline notice", () => {
	// The store closes the connection on purpose whenever the app goes to the
	// background; that is not the machine being offline (see spawnHostState).
	it("calls the machine offline or rejected by the rule the other screens use, not by a closed connection", () => {
		expect(spawn).toContain("const machine = spawnHostState(selectedHost, phoneNetwork, Date.now());");
		expect(spawn).not.toContain('connection === "closed");');
		const notices = between(spawn, "const notices = [...hostNotices, ...cloudNotices, ...(setupBlocked ? [] : spawnNotices({", "}))];");
		expect(notices).toContain("machine,");
		// The refusal's status, for the board's copy of it.
		expect(notices).toContain("errorStatus: selectedHost?.errorStatus,");
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

describe("spawn draft", () => {
	// Saving waits for the saved draft to be read; state, so what was typed
	// meanwhile is saved once it is, and the old draft never replaces it.
	it("saves text typed before the saved draft was read, and does not restore over it", () => {
		expect(spawn).toContain("const [draftRestored, setDraftRestored] = useState(false);");
		expect(spawn).toContain("}).finally(() => { if (generation === restoreGeneration.current) setDraftRestored(true); });");
		expect(spawn).toContain("}, [accountKnown, attachments, draftOwner, draftRestored, draftSaver, draftSelections, prompt, selectedHostId]);");
		expect(spawn).toContain("if (promptRef.current || attachmentsRef.current.length) return;");
		expect(spawn).not.toContain("draftRestored.current");
	});

	// A failed read may have left the draft on the phone: an empty composer
	// saved then would delete it.
	it("does not save an empty composer over a draft it could not read", () => {
		expect(spawn).toContain("if (generation === restoreGeneration.current) draftUnread.current = true;");
		expect(spawn).toContain("if ((draftUnread.current || draftElsewhere.current) && !prompt.trim() && attachments.length === 0) return;");
	});

	// Opened for machine B while machine A owns the saved draft: restored, A's
	// words were saved back as B's draft, and an empty page deleted A's.
	it("keeps another machine's draft off a page routed to this one, and does not save over it", () => {
		const restore = spawn.slice(spawn.indexOf("const restoreDraft = () => {"), spawn.indexOf("setPrompt(draft.prompt);"));
		expect(restore).toContain("if (routeHostId && draftHostId !== null && draftHostId !== routeHostId) {\n\t\t\t\tdraftElsewhere.current = true;\n\t\t\t\trestoredHostId.current = null;\n\t\t\t\treturn;");
		expect(restore.indexOf("draftElsewhere.current = false;")).toBeLessThan(restore.indexOf("if (!draft) return;"));
		const save = spawn.slice(spawn.indexOf("if (!draftRestored || !accountKnown) return;"), spawn.indexOf("draftSaver.schedule({ prompt, attachments, owner"));
		expect(save).toContain("if ((draftUnread.current || draftElsewhere.current) && !prompt.trim() && attachments.length === 0) return;\n\t\t// Written here, this page's draft replaces the other machine's.\n\t\tdraftElsewhere.current = false;");
	});

	// A Cloud draft picked before Cloud loaded landed on a machine (or none) for good.
	it("waits for Cloud before picking the machine for a Cloud draft", () => {
		expect(spawn).toContain("const cloudResolved = Boolean(cloud) || (cloudState.status !== \"loading\" && cloudState.status !== \"connecting\");");
		expect(spawn).toContain("}, [cloudResolved, configResolved, connectedHosts, currentHostId, draftRestored, hostStates, machineHosts, routeHostId]);");
	});

	// A Start sent with a picked account and restored without it sent another
	// request under a new id, starting the task twice.
	it("saves and restores a picked agent account for the draft's machine and agent", () => {
		expect(spawn).toContain("...(accountPick ? { accountId: accountPick, accountHarness: harness } : restoredAccount ? { accountId: restoredAccount.accountId, accountHarness: restoredAccount.harness } : {}),");
		expect(spawn).toContain("if (picked.accountId && picked.accountHarness) setRestoredAccount({ hostId: restoredHostId.current, harness: picked.accountHarness, accountId: picked.accountId });");
		const reset = spawn.slice(spawn.indexOf("const accountScope = useRef<string | null>(null);"), spawn.indexOf("}, [restoredAccount, selectedHostId, harness]);"));
		expectBefore(reset, "if (restoredAccount && selectedHostId === restoredAccount.hostId && harness === restoredAccount.harness) {\n\t\t\tsetAccountPick(restoredAccount.accountId);", "if (moved) setAccountPick(undefined);");
		const change = spawn.slice(spawn.indexOf("const draftAccountSeen = useRef"), spawn.indexOf("}, [accountKnown, draftAccountId]);"));
		expect(change).toContain("setRestoredAccount(null);");
	});

	// Picked under one account and read after another signed in, the file landed in the next account's task.
	it("drops attachments picked under the account that signed out while they loaded", () => {
		const add = spawn.slice(spawn.indexOf("const addAttachments = async"), spawn.indexOf("const pickFiles = "));
		expect(add).toContain("const pickedFor = draftAccountRef.current;");
		expectBefore(add, "const picked = await pick();\n\t\t\tif (draftAccountRef.current !== pickedFor) return 0;", "const next = await readSpawnAttachments(before, picked);\n\t\t\tif (draftAccountRef.current !== pickedFor) return 0;");
		expectBefore(add, "if (draftAccountRef.current !== pickedFor) return 0;\n\t\t\tconst merged", "attachmentsRef.current = merged.attachments;");
	});

	// A save the account change or a forgotten machine dropped is not saved: the
	// Start stops rather than send a task whose draft has no id on the phone.
	it("stops a Start whose draft save was dropped, and clears only its own draft once started", () => {
		const send = spawn.slice(spawn.indexOf("const saved = await saveBeforeStart("), spawn.indexOf("if (projectId === STANDALONE_PROJECT)"));
		expectBefore(send, "if (saved === \"dropped\") {\n\t\t\t\tholding.current = false;\n\t\t\t\thaptics.error();\n\t\t\t\tsetError(START_DROPPED_NOTICE);\n\t\t\t\tsetBusy(false);\n\t\t\t\treturn;\n\t\t\t}", "await spawn({ ...request, clientRequestId: sent.id });");
		expect(send).toContain("void draftSaver.cancel().then(() => clearStartedSpawnDraft(sent.id)).catch(() => {});");
		expect(spawn).not.toContain("then(clearSpawnDraft)");
	});

	// A dropped Start used to stop silently: Start seemed to do nothing. It
	// says why, sends no task, and the line goes once the form is edited.
	describe("a Start whose draft save was dropped", () => {
		const onSpawn = between(spawn, "const onSpawn = async () => {", "// While a request runs, the back swipe is off too");
		const dropped = between(onSpawn, "if (saved === \"dropped\") {", "}");

		it("shows the notice in the page's Start error line and sends no task", () => {
			expect(dropped).toContain("setError(START_DROPPED_NOTICE);");
			expect(dropped).toContain("return;");
			expect(dropped).not.toContain("spawn(");
			// The only request a Start sends comes after the dropped check.
			expect(onSpawn.match(/await spawn\(/g)).toHaveLength(1);
			expectBefore(onSpawn, "if (saved === \"dropped\") {", "await spawn({ ...request, clientRequestId: sent.id });");
			expect(spawn).toContain("{error ? <Text accessibilityRole=\"alert\" style={styles.error}>{error}</Text> : null}");
		});

		it("clears the notice when Start is pressed again, before the next save", () => {
			expectBefore(onSpawn, "setError(null);", "const saved = await saveBeforeStart(");
		});

		it("clears the notice on every edit the user makes to the form", () => {
			expect(spawn).toContain("const edited = <A extends unknown[]>(change: (...args: A) => void) => (...args: A) => {\n\t\tsetError(errorAfterEdit);\n\t\tchange(...args);\n\t};");
			const controls = between(spawn, "<SpawnComposerControls", "onSpawn={() => { void onSpawn(); }}");
			for (const prop of [
				"onSelectHost={edited(chooseHost)}",
				"onSelectProject={edited(changeProject)}",
				"onSelectAccount={edited(setAccountPick)}",
				"onSelectHarness={edited(selectAgent)}",
				"onSelectModel={edited(selectModel)}",
				"onSelectAgentModel={edited(selectAgentModel)}",
				"onSelectApprovalMode={edited((next: string) => setApprovalChoice(next as ApprovalMode))}",
				"onSelectEffort={edited(selectEffort)}",
				"onChoosePhoto={edited(() => { void pickPhotos(); })}",
				"onChooseFile={edited(() => { void pickFiles(); })}",
				"onPasteImages={busy ? null : edited(pasteImages)}",
				"onChangePrompt={edited(setPrompt)}",
				"onRemove={edited((key: string) => {",
			]) expect(controls).toContain(prop);
			// Every handler the controls take goes through it, so a new one can't skip it.
			const handlers = [...controls.matchAll(/\n\t+(on[A-Z]\w*)=\{(.*)/g)];
			expect(handlers.length).toBeGreaterThan(10);
			for (const [, name, value] of handlers) {
				// Opens the model sheet; the pick it answers with is wrapped below.
				if (name === "onBrowseModels") continue;
				expect(value, name).toMatch(/^(busy \? null : )?edited\(/);
			}
			expect(spawn).toContain("onSelect: edited((id: string) => selectAgentModel(agentId, id || \"__auto__\")),");
			// Dictation types into the prompt too.
			expectBefore(between(spawn, "const voice = useVoiceInput({", "const listening ="), "setError(errorAfterEdit);", "setPrompt((old) =>");
		});

		it("leaves a Start that saved its draft to send the task as before", () => {
			const sent = onSpawn.slice(onSpawn.indexOf("if (saved === \"dropped\") {"));
			expectBefore(sent, "return;\n\t\t\t}\n\t\t\tconst session = await spawn({ ...request, clientRequestId: sent.id });", "router.replace({");
			expect(sent.slice(sent.indexOf("const session = await spawn(")).includes("START_DROPPED_NOTICE")).toBe(false);
		});
	});

	// Saved before the machine is picked, a restored draft keeps its machine.
	it("keeps the restored draft's machine while the machine is still being picked", () => {
		expect(spawn).toContain("const owner = selectedHostId === null && !hostSelectionInitialized.current\n\t\t\t? { ...draftOwner, hostId: restoredHostId.current }\n\t\t\t: draftOwner;");
		expect(spawn).toContain("draftSaver.schedule({ prompt, attachments, owner, selections: draftSelections");
	});

	// Made once: a saver per render would each stay registered for forgetting.
	it("makes one draft saver for the page and lets it go when the page closes", () => {
		expect(spawn).toContain("const [draftSaver] = useState(() => createSpawnDraftSaver());");
		expect(spawn).not.toContain("useRef(createSpawnDraftSaver())");
		expect(spawn).toContain("void draftSaver.flush();\n\t\t\tdraftSaver.release();");
	});

	// On a page kept open after its task started, a new draft saves again.
	it("stops saving only the draft a Start began a task with", () => {
		expect(spawn).not.toContain("startedRef");
		expect(spawn).toContain("if (started && started.prompt === prompt && started.attachments === attachments) return;");
		expect(spawn).toContain("startedDraft.current = { prompt, attachments: attachmentsRef.current };");
	});

	// Closed after the daemon took the task but before the draft was cleared,
	// the restored draft's Start reuses the id and the daemon answers with that task.
	it("keeps the request id with the draft before the request leaves, and reuses it once restored", () => {
		const send = spawn.slice(spawn.indexOf("const payload = JSON.stringify(requestFields);"), spawn.indexOf("startedDraft.current = { prompt"));
		expect(send).toContain("requestMatches(requestRef.current, payload, attachmentsRef.current)");
		expect(send).toContain("attachments: attachmentsKey(attachmentsRef.current)");
		expect(send.indexOf("const saved = await saveBeforeStart(draftSaver, { prompt, attachments: attachmentsRef.current, owner: draftOwner, selections: draftSelections, request: sent });")).toBeGreaterThan(0);
		expect(send.indexOf("await saveBeforeStart(")).toBeLessThan(send.indexOf("await spawn({ ...request, clientRequestId: sent.id });"));
		expect(spawn).toContain("if (draft.request) requestRef.current = draft.request;");
		// Compared by content: never by the attachments array object.
		expect(spawn).not.toContain("requestRef.current?.attachments !== attachmentsRef.current");
	});

	// After a Start that did not land, changed attachments must not be saved
	// under its id: restored, the next Start would send them with it.
	it("saves the last Start's id only while the draft has the attachments it sent", () => {
		expect(spawn).toContain("const current = request && request.attachments === attachmentsKey(attachments) ? request : undefined;");
		expect(spawn).toContain("...(current ? { request: current } : {})");
	});

	// A model picked for the Start comes back with the draft, so the next Start
	// sends the same request and reuses its id instead of starting another task.
	it("saves and restores what was picked, a model override included", () => {
		expect(spawn).toContain("...(modelTouched && model ? { model } : {}),");
		expect(spawn).toContain("...(agentTouched && harness ? { harness } : {}),");
		expect(spawn).toContain("if (picked.model) { setModel(picked.model); setModelTouched(true); }");
		expect(spawn).toContain("if (picked.harness) { setHarness(picked.harness); setAgentTouched(true); }");
		expect(spawn).toContain("if (picked.projectId) setProjectId(picked.projectId);");
		expect(spawn).toContain("if (isApprovalMode(picked.approvalMode)) setApprovalChoice(picked.approvalMode);");
		expect(spawn).toContain("if (picked.effort) setEffort(picked.effort);");
		// The machine waits for the draft, and goes back to the draft's own.
		expect(spawn).toContain("if (!draftRestored) return;\n\t\tconst draftHostId = restoredHostId.current;");
		// Unrouted, the restored draft's machine is waited for before another is picked.
		expect(spawn).toContain("const awaitedHostId = routeHostId ?? draftHostId ?? undefined;");
		expect(spawn).toContain("if (!spawnHostSelectionReady({ routeHostId: awaitedHostId, configResolved, hosts: hostStates, connectedMachineCount: machineHosts.length, cloudResolved })) return;");
		expect(spawn).toContain("const preferredHostId = draftHostId && connectedHosts.some((host) => host.id === draftHostId) ? draftHostId : currentHostId ?? undefined;");
	});

	it("saves the draft for the machine picked and the account signed in", () => {
		expect(spawn).toContain("const draftOwner = useMemo(() => ({ hostId: selectedHostId, accountId: draftAccountId }), [draftAccountId, selectedHostId]);");
		// Not before Cloud read who is signed in: saved as no account's, any account would find it.
		expect(spawn).toContain("if (!draftRestored || !accountKnown) return;");
	});

	// The page stays mounted through an account change: the last account's
	// words must not carry over to, or be saved for, the next one.
	it("empties the composer when the account changes, and loads only the new account's draft", () => {
		const change = spawn.slice(spawn.indexOf("const draftAccountSeen = useRef"), spawn.indexOf("}, [accountKnown, draftAccountId]);"));
		expect(change).toContain("if (previous === undefined || previous === draftAccountId) return;");
		for (const reset of ["void draftSaver.cancel();", "setPrompt(\"\");", "attachmentsRef.current = [];", "setAttachments([]);", "startedDraft.current = null;", "requestRef.current = undefined;"]) {
			expect(change.indexOf(reset)).toBeGreaterThan(0);
			expect(change.indexOf(reset)).toBeLessThan(change.indexOf("restoreDraft();"));
		}
	});
});
