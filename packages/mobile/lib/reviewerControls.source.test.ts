import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const actions = readFileSync(new URL("../app/sheets/review-actions.tsx", import.meta.url), "utf8");
const detail = readFileSync(new URL("../app/review/[sessionId].tsx", import.meta.url), "utf8");
const pickerIOS = readFileSync(new URL("./reviewer-picker.ios.tsx", import.meta.url), "utf8");
const picker = readFileSync(new URL("./reviewer-picker.tsx", import.meta.url), "utf8");
const sheetIOS = readFileSync(new URL("./review-actions-sheet.ios.tsx", import.meta.url), "utf8");
const sheetAndroid = readFileSync(new URL("./review-actions-sheet.tsx", import.meta.url), "utf8");
const terminal = readFileSync(new URL("./session/TerminalSessionScreen.tsx", import.meta.url), "utf8");
const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
// Where the page and the sheet reach a daemon or Orchestrator Cloud.
const backend = source("./reviewBackend.ts");
// The PR page's comment list, with each comment's actions.
const comments = source("./ReviewCommentsSection.tsx");

describe("reviewer control integration", () => {
	it("keeps the project-default override separate from the effective reviewer", () => {
		expect(actions).toContain('const [reviewerOverride, setReviewerOverride] = useState("")');
		expect(actions).toContain("const effectiveReviewer = reviewerOverride || projectDefaultReviewer;");
		expect(backend).toContain("defaultReviewerHarness(project?.config?.reviewers, session.harness ?? undefined)");
		expect(actions).toContain("setProjectDefaultReviewer(settings.defaultReviewer);");
		// The daemon's reviewerHarness names the last reviewer that ran, so it
		// must never pick the model catalog after a switch.
		expect(actions).not.toContain("result.reviewerHarness");
		expect(actions).not.toContain("reviewState.reviewerHarness");
		expect(actions).toContain("selectedReviewer: reviewerOverride,");
		expect(actions).toContain("\t\t\teffectiveReviewer,\n");
	});

	it("ignores stale model catalogs after the effective reviewer changes", () => {
		expect(actions).toContain("const request = ++modelRequest.current");
		expect(actions).toContain("request === modelRequest.current");
	});

	it("confirms model, mode and effort changes while a review is running", () => {
		expect(actions).toContain("confirmReviewerChange(reviewerOverride, reviewerModelPick(modelState, value))");
		expect(actions).toContain("confirmReviewerChange(reviewerOverride, reviewerEffortPick(modelState, level))");
	});

	it("does not warn or save when the selected reviewer settings are unchanged", () => {
		expect(actions).toContain("reviewerSelectionChanged(reviewerOverride, reviewerConfig, id, agentConfig)");
	});

	it("renders every reviewer through the shared harness logo registry", () => {
		expect(pickerIOS).toContain('import { HarnessImage } from "./spawn-composer-controls.ios"');
		expect(pickerIOS).toContain("<HarnessImage harness={agent.id} />");
		expect(picker).toContain("const source = logoFor(harness, scheme);");
	});

	it("keeps Android review actions scrolling inside the sheet", () => {
		expect(actions).toContain('import { ReviewActionsSheet } from "../../lib/review-actions-sheet";');
		expect(sheetAndroid).toMatch(/<ScrollView[^>]*nestedScrollEnabled/);
	});

	it("picks the reviewer, model and effort from native iOS menu pickers", () => {
		expect(pickerIOS).toContain('accessibilityIdentifier("review-reviewer")');
		expect(pickerIOS).toContain('tag="review-model"');
		expect(pickerIOS).toContain('tag="review-effort"');
		expect(pickerIOS).toContain("accessibilityIdentifier(identifier)");
	});

	it("only offers reviewers that can run, like the spawn sheet", () => {
		expect(actions).toContain(".filter((agent) => agent.selectable || agent.id === reviewerOverride)");
		expect(actions).toContain("reviewers: availableReviewers,");
	});

	it("uses the desktop inspector's automation wording and the system switch colors", () => {
		expect(actions).toContain('title: "Auto review"');
		expect(actions).toContain('title: "Automatically fix review comments"');
		expect(actions).toContain('title: "Automatically fix CI failures"');
		for (const file of [actions, sheetIOS, sheetAndroid]) expect(file).not.toContain("trackColor");
	});

	it("does not let auto review lose its persistent reviewer", () => {
		expect(detail).toContain("!data.reviewerHandleId || autoReviewEnabled");
		expect(detail).toContain('...(controls.stop && !autoReviewEnabled ? [{ id: "stop", label: "Stop reviewer session"');
	});

	it("checks fresh review state before changing reviewer, model, or mode", () => {
		expect(actions).toContain("const latest = await live.reviews(sessionId)");
		expect(actions).toContain("latest.reviews.some((item) => item.status === \"running\")");
		expect(actions).toContain("confirmReviewerChange(reviewerOverride");
		expect(actions).not.toContain('reviewerSwitchWarning(running === "true")');
	});

	it("does not fall back to a different pull request", () => {
		expect(actions).toContain("setPR(matchedPR)");
		expect(actions).not.toContain("?? prs[0]");
	});

	it("only offers open review comments with words to the worker", () => {
		expect(comments).toContain("onSend={commentCanSend(comment) ? () => void sendOne(comment) : undefined}");
		expect(comments).toContain('...(onSend && !sent ? [{ id: "send", label: "Send to agent"');
	});

	it("never presents the in-app browser on top of the actions sheet", () => {
		// The sheet holds the reviewer's settings now; whatever link it opens must say it comes from a sheet.
		for (const call of actions.match(/openGitHub\([^)]*\)/g) ?? []) expect(call).toContain("{ fromSheet: true }");
	});

	it("keeps per-item actions in a desktop-style menu", () => {
		expect(comments).toContain("<ItemActionsMenu accessibilityLabel={`Actions for ${name}'s comment`}");
		expect(comments).toContain('{ id: "resolve", label: "Resolve"');
		expect(detail).toContain('<ItemActionsMenu accessibilityLabel="Reviewer session actions" actions={reviewerActions}');
	});

	it("chooses Open, Restore, or Stop from what is actually available", () => {
		expect(detail).toContain("const daemonControls = reviewerControls(data, review, sessionId);");
		// Cloud reviewers run as conversations, with no session to restore or stop.
		expect(detail).toContain('const controls = isCloudHost(currentHostId) ? { open: daemonControls.open === "chat" ? "chat" as const : undefined, restore: false, stop: false } : daemonControls;');
		expect(detail).toContain('...(controls.open ? [{ id: "open", label: controls.open === "chat" ? "Open reviewer chat" : "Open reviewer terminal"');
		expect(detail).toContain('...(controls.restore ? [{ id: "restore", label: "Restore reviewer"');
		expect(detail).toContain("Push a new commit to run another review.");
	});

	// The reviewer pane is attached by handle, but it is not a daemon shell
	// terminal: closing it as one answered 404 "No such shell terminal".
	it("stops the reviewer, not a shell, when its terminal is closed", () => {
		expect(terminal).toContain('const reviewerPane = params.kind === "reviewer" && Boolean(params.sessionId);');
		expect(terminal).toContain("if (reviewerPane) await killSessionReviewer(activeConfig, String(params.sessionId));");
		expect(terminal).not.toContain("await loadConfig()");
	});

	// A native menu nested in the row's Pressable lost its tap to the row, which
	// opened the reviewer instead of the menu.
	it("lays the reviewer's button and menu over its row instead of nesting them", () => {
		const start = detail.indexOf("onPress={openReviewer} android_ripple=");
		const end = detail.indexOf("{closedNote ?");
		expect(start).toBeGreaterThanOrEqual(0);
		expect(end).toBeGreaterThan(start);
		const row = detail.slice(start, end);
		expect(row.indexOf("</Pressable>")).toBeLessThan(row.indexOf("<ItemActionsMenu"));
		expect(detail).toContain('<View style={styles.overlaySlot} pointerEvents="box-none">');
	});

	// A page pushed while a sheet is presented opens inside that sheet, so every
	// way into review detail and the reviewer goes through useOpenPage.
	it("opens review detail and the reviewer as pages, never inside a sheet", () => {
		expect(source("./PRCard.tsx")).toContain("openPage(reviewRouteForPR(session.id, pr, hostId))");
		expect(source("./worker-list-row.tsx")).toContain("if (reviewRoute) openPage(reviewRoute);");
		expect(source("./chat/ChatSessionScreen.tsx")).toContain("if (route) openPage(route);");
		expect(source("../app/notifications/index.tsx")).toContain('action.kind === "review") openPage(');
		expect(source("./PushManager.tsx")).toContain('if (target === "review") openPage(destination as Href);');
		expect(detail).toContain("openPage(destination);");
		for (const path of ["./PRCard.tsx", "./worker-list-row.tsx", "./chat/ChatSessionScreen.tsx", "../app/review/[sessionId].tsx"]) {
			expect(source(path)).not.toMatch(/router\.(push|navigate)\((reviewRoute|route|destination)\)/);
		}
	});

	it("lays the review screen out as flat board rows, not cards", () => {
		expect(detail).not.toContain("<Card");
		expect(detail).toContain('<ListSectionHeader label="Pull request" />');
		expect(detail).toContain("borderBottomWidth: rowDividerWidth");
	});

	it("merges like desktop: only when ready, fenced to the head commit, after confirmation", () => {
		expect(detail).toContain("const merge = pr ? mergeReadiness(pr) : undefined;");
		expect(detail).toContain("await live.merge(sessionId, pr)");
		expect(backend).toContain("merge: (_sessionId, pr) => mergeSessionPR(config, pr),");
		expect(backend).toContain("await client.mergePullRequest(orgId, sessionId, pr.number, pr.url, pr.headSha);");
		expect(detail).toContain("This will squash-merge PR #${pr.number} in the remote repository.");
	});

	// A raw message can be an engine string or a bare HTTP status; the shared
	// mapping turns those into copy that says what to do, as other screens do.
	it("shows failures through userFacingError, never a raw error message", () => {
		for (const screen of [detail, actions]) {
			expect(screen).toMatch(/import \{[^}]*\buserFacingError\b[^}]*\} from "\.\.\/\.\.\/lib\/connectionError";/);
			expect(screen).not.toMatch(/\.message\s*:/);
		}
		expect(detail).toContain('setError(userFacingError(value, "Could not load this review."))');
		expect(actions).toContain('setError(userFacingError(cause, "Could not load review actions."))');
	});

	// A reviewer asked again from a row is not offered again until the provider's next read.
	it("asks a reviewer again once from the comment list", () => {
		expect(comments).toContain("setAsked((current) => new Set(current).add(rerequestKey(reviewerId)));");
		expect(comments).toContain("!asked.has(rerequestKey(reviewerId))");
	});

	// GitHubComment's links read the chat's session directory; without its provider
	// the PR page threw on its first comment (the render tests stand GitHubComment in).
	it("renders comment bodies inside the provider their links read", () => {
		const open = comments.indexOf("<WorkerSessionsProvider>");
		expect(open).toBeGreaterThan(-1);
		expect(open).toBeLessThan(comments.indexOf("<ReviewCommentRow"));
		expect(comments.indexOf("</WorkerSessionsProvider>")).toBeGreaterThan(comments.lastIndexOf("<ReviewCommentRow"));
	});

	it("keys automatic-review dismissal to the stable run id", () => {
		expect(detail).toContain("const autoReviewFailureId = autoReviewFailure?.id");
		expect(detail).toContain("[autoReviewFailureId, dismissedAutoFailureId]");
	});
});
