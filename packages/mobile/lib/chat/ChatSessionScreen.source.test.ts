import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const screenSource = readFileSync(new URL("./ChatSessionScreen.tsx", import.meta.url), "utf8");
const composerSource = readFileSync(new URL("./ChatComposer.tsx", import.meta.url), "utf8");
const apiSource = readFileSync(new URL("./api.ts", import.meta.url), "utf8");
const cloudScreenSource = readFileSync(new URL("../cloud/CloudSessionScreen.tsx", import.meta.url), "utf8");

describe("active turn controls", () => {
	it("remounts the composer when a same-ID session changes host", () => {
		// Keyed by the machine, not the connection: an app switch drops `config`
		// and must not remount the composer (the resume flicker).
		expect(screenSource).toContain('const composerKey = JSON.stringify([machine, session.id]);');
		expect(screenSource).toContain('key={composerKey}');
		expect(screenSource).toContain('quoteRequest?.composerKey === composerKey ? quoteRequest : undefined');
		expect(composerSource).toContain('`ao.chat.draft.${machine}.${sessionId}`');
		expect(composerSource).toContain('if (!draftKey || !draftLoaded) return;');
	});

	it("keeps working state in the conversation instead of a redundant status strip", () => {
		expect(screenSource).not.toContain("LiveTurnBar");
		expect(screenSource).not.toContain("Agent is working");
		expect(screenSource).not.toContain("stopTurn:");
	});

	it("uses one composer control for queued/running sends and interruption", () => {
		expect(composerSource).toContain("Boolean(activeTurn(snapshot))");
		expect(composerSource).toContain("composerPrimaryAction({ active, hasDraft, hasAttachments:");
		expect(composerSource).toContain("interrupting ? <ActivityIndicator");
	});

	it("docks queued messages above the composer with a per-message delete action", () => {
		expect(screenSource).toContain("onCancelQueuedTurn={conversation.cancelQueuedTurn}");
		expect(composerSource).toContain('accessibilityLabel={`Delete queued message: ${entry.message.text}`}');
	});

	it("promotes the exact queued turn when the user steers its dock row", () => {
		expect(apiSource).toContain('`/turns/${encodeURIComponent(turnId)}/steer`');
		expect(screenSource).toContain("onPromoteQueuedTurn={conversation.promoteQueuedTurn}");
		expect(composerSource).toContain('accessibilityLabel={`Steer queued message: ${entry.message.text}`}');
	});

	// Orchestrator Cloud has no route to steer with or delete a queued turn: its
	// queue showed both, and Steer hid a message that stayed queued.
	it("offers a queued message's actions only where the host takes them", () => {
		expect(composerSource).toContain("{canSteer && onPromoteQueuedTurn ? <Pressable");
		expect(composerSource).toContain("{onCancelQueuedTurn ? <Pressable");
		expect(cloudScreenSource).not.toMatch(/onPromoteQueuedTurn=|onCancelQueuedTurn=/);
	});

	it("keeps the queued row compact without a second labeled steer pill", () => {
		expect(composerSource).not.toContain("queueSteerText");
	});

	it("keeps the turn settings in the pill's controls row", () => {
		expect(composerSource).toContain("style={[styles.controlsRow, { paddingLeft: CORNER_CONTROL, paddingRight: CORNER_CONTROL }, controlsOpening]}>");
	});
	// One name per session, as on desktop: useWorkspaceQuery derives
	// `displayName ?? issueId ?? id`, ChatWorkspace prefers that over the
	// conversation's own title, and renaming anywhere is the same displayName
	// PATCH. With the precedence inverted, renaming on the board changed the row
	// and left this header showing the agent's auto-generated title.
	it("prefers the session's name over the conversation title", () => {
		expect(screenSource).toContain("const title = sessionName || conversation.snapshot?.title");
	});

	// The actions sheet holds only Shell, Preview, Artifacts, Pin, PR Review, Compact and Archive.
	it("offers no history map from the actions sheet", () => {
		expect(screenSource).not.toContain('kind: "conversation-map"');
		expect(screenSource).toContain("contextPercent: menuContextPercent,");
	});

	it("shows a failed start's reason instead of only a stopped-agent banner", () => {
		expect(screenSource).toContain('session.provisionState !== "failed"');
		expect(screenSource).toContain('title="Session failed to start" message={failedStart}');
		// The setup checklist reports a failed start in place, with Retry; the banner covers the rest.
		expect(screenSource).toContain('startFailure={startupShown ? undefined : failedStart}');
		// A starting session shows its checklist while the conversation loads, not a bare spinner.
		expect(screenSource).toContain("conversation.loading && !conversation.snapshot && startupView && !startupView.failed");
		expect(screenSource).toContain("<SessionStartupStatus startup={startupView} />");
		// While the checklist reports setup, the yellow "Starting the agent…" banner stands down.
		expect(screenSource).toContain('!startFailure && !starting && (snapshot.controller.state === "recovering"');
		// The agent coming up emits no conversation event; keep re-reading until it reports connected.
		expect(screenSource).toContain('if (before === "provisioning" && provisionState === "ready") setAwaitingAgent(true);');
		// Serially, so a read slower than the poll interval is never superseded (see serialPoll.test.ts).
		expect(screenSource).toContain("return startSerialPoll(refreshConversation, STARTUP_POLL_MS);");
		expect(screenSource).not.toContain("setInterval(() => void refreshConversation()");
		expect(screenSource).toContain('disabled={switchingToTerminal || Boolean(failedStart)}');
	});

	it("offers a live PR review shortcut while leaving the chat composer in place", () => {
		expect(screenSource).toContain("sessionPRReadyForReview(session)");
		expect(screenSource).toContain("reviewPR={reviewPromptPR}");
		expect(screenSource).toContain("reviewRouteForPR(session.id, reviewPromptPR, currentHostId)");
		expect(screenSource).toContain("reviewRouteForSession(session, currentHostId)");
		expect(screenSource).toContain("reviewSummaries.summaryFor(summaryConfig, session.id, reviewPromptPR.number)");
		expect(screenSource).toContain("<ChatComposer");
	});

});

describe("whole-screen states", () => {
	it("shows loading, and failures as an alert with Retry", () => {
		expect(screenSource).toContain('<EmptyState busy title="Loading conversation…" />');
		expect(screenSource).toContain('onPress={() => void resume()} />}');
		expect(screenSource).toContain('<View accessibilityRole="alert" style={[styles.center, { paddingTop: headerOverlap }]}>{children}</View>');
	});
});

describe("stopped agents", () => {
	it("resumes an exited agent once when its session is opened", () => {
		expect(screenSource).toContain("const autoResumeTried = useRef<string | undefined>(undefined);");
		expect(screenSource).toContain("shouldAutoResume(session, terminated, Boolean(config))");
		expect(screenSource).toContain("controllerStoppedBanner(terminated, resumeError ?? snapshot.controller.error)");
		expect(screenSource).toContain("void resume(true);");
	});
});

describe("ao://sessions links in the conversation", () => {
	const source = screenSource;
	it("opens a session link as that session's chat on this conversation's machine, and names it", () => {
		const openLink = source.slice(source.indexOf("const openLink = useCallback"), source.indexOf("const nameOfSession"));
		expect(openLink).toMatch(/const target = sessionLink\(url\);\s*if \(target\) \{\s*router\.push\(\{ pathname: "\/session\/\[id\]", params: \{ id: target\.sessionId, projectId: target\.projectId, hostId: currentHostId \} \}\);\s*return;/);
		expect(source).toContain("<ChatLinkProvider onLinkOpen={openLink} sessionName={nameOfSession}");
	});
});

describe("the composer and the keyboard", () => {
	// In a long chat, or one still loading as it opened, focusing the field kept
	// the JS thread busy for most of a second, and the keyboard came up over a
	// composer still at the bottom: the padded root held it back.
	it("rides the keyboard outside the padded root, by a transform", () => {
		const root = screenSource.indexOf("</KeyboardAvoidingView>");
		const follower = screenSource.indexOf("<KeyboardFollower lift={chatKeyboard.composerLift} style={styles.floatingComposer} onLayout={measureComposer}>");
		expect(follower).toBeGreaterThan(root);
		expect(screenSource.indexOf("<ChatComposer")).toBeGreaterThan(follower);
	});
});

// iOS 26's glass blurred only the status bar over the conversation and left
// the title over sharp text; iOS 27's covers the whole bar.
describe("the bar over the conversation", () => {
	it("is the clear glass bar on iOS 27 and later only, and the stack's own bar on every other iOS", () => {
		expect(screenSource).toContain('const CLEAR_HEADER = (SYSTEM_GLASS_HEADER && parseInt(String(Platform.Version), 10) >= 27) || Platform.OS === "android";');
		expect(screenSource).toContain("if (!CLEAR_HEADER) return;");
		expect(screenSource).not.toContain("headerBlurEffect");
	});
});
