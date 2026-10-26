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

describe("reviewer control integration", () => {
	it("keeps the project-default override separate from the effective reviewer", () => {
		expect(actions).toContain('const [reviewerOverride, setReviewerOverride] = useState("")');
		expect(actions).toContain("const effectiveReviewer = reviewerOverride || projectDefaultReviewer;");
		expect(actions).toContain("defaultReviewerHarness(project?.config?.reviewers, session.harness ?? undefined)");
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

	it("confirms model and mode changes while a review is running", () => {
		expect(actions).toContain("confirmReviewerChange(reviewerOverride, { ...reviewerConfig, [key]: value })");
	});

	it("does not warn or save when the selected reviewer settings are unchanged", () => {
		expect(actions).toContain("reviewerSelectionChanged(reviewerOverride, reviewerConfig, id, agentConfig)");
	});

	it("renders every reviewer through the shared harness logo registry", () => {
		expect(pickerIOS).toContain('import { HarnessImage, useHarnessLogoUris } from "./spawn-composer-controls.ios"');
		expect(pickerIOS).toContain("<HarnessImage uri={logoUris[agent.id]} harness={agent.id} />");
		// Compose draws the mark itself, in its own colours, on the same chip rule.
		expect(picker).toContain("const source = logoFor(harness);");
		expect(picker).toContain("const chip = chipColorFor(harness);");
		expect(picker).toContain("<Icon source={source} tint={null} size={iconSize.xl} />");
	});

	// The inline pseudo-dropdown pushed the sheet's content down and had no
	// Back to close it; Material's own menu does both.
	it("opens reviewer and model choices as Material dropdown menus on Android", () => {
		expect(picker).toContain('import { Box, DropdownMenu, DropdownMenuItem, Icon, ListItem, Text } from "@expo/ui/jetpack-compose";');
		expect(picker).toContain("<DropdownMenu expanded={open && !busy} onDismissRequest={onDismiss}");
		expect(picker).toContain('tag="review-reviewer"');
		expect(picker).toContain('tag="review-model"');
		expect(picker).toContain('contentDescription="Selected"');
		expect(picker).not.toContain("expanded === \"reviewer\" ? <View");
	});

	it("keeps Android review actions scrolling inside the sheet", () => {
		expect(actions).toContain('import { ReviewActionsSheet } from "../../lib/review-actions-sheet";');
		expect(sheetAndroid).toMatch(/<ScrollView[^>]*nestedScrollEnabled/);
	});

	it("picks the reviewer and model from native iOS menu pickers", () => {
		expect(pickerIOS).toContain('import { Label, Picker, Text } from "@expo/ui/swift-ui"');
		expect(pickerIOS).toContain('pickerStyle("menu")');
		expect(pickerIOS).toContain('accessibilityIdentifier("review-reviewer")');
		expect(pickerIOS).toContain('accessibilityIdentifier("review-model")');
		// A choice the sheet declines must not stay drawn as the selection.
		expect(pickerIOS).toContain("key={`reviewer-${reviewerChoice.revision}`}");
		expect(pickerIOS).toContain("key={`model-${modelChoice.revision}`}");
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
		expect(sheetIOS).not.toMatch(/<Toggle[^>]*tint\(/);
	});

	// A SwiftUI Form on iOS and Material list items on Android, with each
	// section's hint as its footer, both platforms' own switches, and a
	// spinner rather than a chevron on a row that acts in place.
	it("lays the review actions sheet out with each platform's form controls", () => {
		expect(sheetIOS).toContain('import { Button, Form, HStack, Image, Label, ProgressView, Section, Spacer, Text, Toggle, VStack } from "@expo/ui/swift-ui";');
		expect(sheetIOS).toContain('<Section title="Automation" footer={<Text>The same settings as this session\'s inspector on desktop.</Text>}>');
		expect(sheetIOS).toContain("<Toggle isOn={policy.value} onIsOnChange={(value) => onChange(policy.key, value)}");
		expect(sheetIOS).toContain("{item.busy ? <ProgressView /> : null}");
		expect(sheetAndroid).toContain('toggleable(policy.value, () => onChange(policy.key, !policy.value), { role: "switch" })');
		expect(sheetAndroid).toContain("<Switch value={policy.value} enabled={interactive}");
		expect(sheetAndroid).toContain('<Section title="Automation" footer="The same settings as this session\'s inspector on desktop.">');
		for (const file of [actions, sheetIOS, sheetAndroid]) {
			expect(file).not.toContain("chevron");
			expect(file).not.toContain("@expo/vector-icons");
			expect(file).not.toMatch(/\bSwitch\b[^\n]*from "react-native"/);
			// Sentence case, and every size from the type ramp.
			expect(file).not.toMatch(/title[=:] ?"[A-Z ]{4,}"/);
			expect(file).not.toMatch(/fontSize: \d/);
			expect(file).not.toContain("hairlineWidth");
		}
	});

	it("does not let auto review lose its persistent reviewer", () => {
		expect(detail).toContain("!data.reviewerHandleId || autoReviewEnabled");
		expect(detail).toContain("const stopActions: ItemAction[] = controls.stop && !autoReviewEnabled");
	});

	it("checks fresh review state before changing reviewer, model, or mode", () => {
		expect(actions).toContain("const latest = await getSessionReviews(config, sessionId)");
		expect(actions).toContain("latest.reviews.some((item) => item.status === \"running\")");
		expect(actions).toContain("confirmReviewerChange(reviewerOverride");
		expect(actions).not.toContain('reviewerSwitchWarning(running === "true")');
	});

	it("does not fall back to a different pull request", () => {
		expect(actions).toContain("setPR(matchedPR)");
		expect(actions).not.toContain("?? prs[0]");
	});

	it("only offers finished review findings to the worker", () => {
		expect(detail).toContain("...(reviewRunSendable(run) && !sent ? [{ id: \"send\", label: \"Send to worker\"");
	});

	it("never presents the in-app browser on top of the actions sheet", () => {
		const calls = actions.match(/openGitHub\([^)]*\)/g) ?? [];
		expect(calls.length).toBeGreaterThan(0);
		for (const call of calls) expect(call).toContain("{ fromSheet: true }");
	});

	it("keeps per-item actions in a desktop-style menu", () => {
		expect(actions).toContain("menuLabel: `Actions for ${reviewerId}'s comment`,");
		expect(sheetAndroid).toContain("<ItemActionsMenu accessibilityLabel={item.menuLabel} actions={item.actions}");
		expect(sheetIOS).toContain("<ItemActionsPullDown actions={item.actions} accessibilityLabel={item.menuLabel}");
		expect(actions).toContain('{ id: "resolve", label: "Resolve"');
		expect(detail).toContain("<ItemActionsMenu accessibilityLabel={`Actions for the ${reviewVerdictLabel(run).toLowerCase()} review`}");
	});

	it("chooses Open, Restore, or Stop from what is actually available", () => {
		expect(detail).toContain("const controls = reviewerControls(data, review, sessionId);");
		expect(detail).toContain("{controls.open\n\t\t\t\t\t\t? <ReviewActionButton label=\"Open\"");
		expect(detail).toContain(": controls.restore ? <ReviewActionButton label=\"Restore\"");
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
		const end = detail.indexOf('<ListSectionHeader label="Orchestrator review" />');
		expect(start).toBeGreaterThanOrEqual(0);
		expect(end).toBeGreaterThan(start);
		const row = detail.slice(start, end);
		expect(row.indexOf("</Pressable>")).toBeLessThan(row.indexOf("<ItemActionsMenu"));
		expect(detail).toContain('<View style={styles.overlaySlot} pointerEvents="box-none">');
	});

	// A page pushed while a sheet is presented opens inside that sheet, so every
	// way into review detail and the reviewer goes through useOpenPage.
	it("opens review detail and the reviewer as pages, never inside a sheet", () => {
		expect(source("./PRCard.tsx")).toContain("openPage({\n\t\t\t\t\tpathname: \"/review/[sessionId]\"");
		expect(source("./worker-list-row.tsx")).toContain("if (reviewRoute) openPage(reviewRoute);");
		expect(source("./chat/ChatSessionScreen.tsx")).toContain("if (route) openPage(route);");
		expect(source("../app/notifications.tsx")).toContain('action.kind === "review") openPage(');
		expect(source("./PushManager.tsx")).toContain('if (target === "review") openPage(destination as Href);');
		expect(detail).toContain("openPage(destination);");
		for (const path of ["./PRCard.tsx", "./worker-list-row.tsx", "./chat/ChatSessionScreen.tsx", "../app/review/[sessionId].tsx"]) {
			expect(source(path)).not.toMatch(/router\.(push|navigate)\((reviewRoute|route|destination)\)/);
		}
	});

	// Row actions are each platform's own small buttons, and the page's chrome
	// glyphs come from the system symbol sets rather than a second icon family.
	it("draws the review page with native buttons and system symbols", () => {
		expect(detail).not.toContain("@expo/vector-icons");
		expect(detail).not.toContain("RowPill");
		expect(detail).toContain('<ReviewActionButton label="Open" icon={controls.open === "chat" ? symbols.chat : symbols.terminal} tone="filled"');
		expect(detail).toContain('tone={primaryAction === "cancel" ? "destructive" : primaryAction === "start" ? "filled" : "outlined"}');
		expect(detail).toContain("{...refreshControlColors(t)}");
		// Nothing under the 13pt footnote step, and dividers that draw on Android.
		expect(detail).not.toMatch(/caption[12]/);
		expect(detail).not.toContain("hairlineWidth");
		expect(detail).not.toContain("hitSlop");
	});

	it("lays the review screen out as flat board rows, not cards", () => {
		expect(detail).not.toContain("<Card");
		expect(detail).toContain('<ListSectionHeader label="Pull request" />');
		expect(detail).toContain("borderBottomWidth: rowDividerWidth");
	});

	it("merges like desktop: only when ready, fenced to the head commit, after confirmation", () => {
		expect(detail).toContain("const merge = pr ? mergeReadiness(pr) : undefined;");
		expect(detail).toContain("await mergeSessionPR(config, pr)");
		expect(detail).toContain("This will squash-merge PR #${pr.number} in the remote repository.");
	});

	// A raw message can be an engine string or a bare HTTP status; the shared
	// mapping turns those into copy that says what to do, as other screens do.
	it("shows failures through userFacingError, never a raw error message", () => {
		for (const screen of [detail, actions]) {
			expect(screen).toContain('import { userFacingError } from "../../lib/connectionError";');
			expect(screen).not.toMatch(/\.message\s*:/);
		}
		expect(detail).toContain('setError(userFacingError(value, "Could not load this review."))');
		expect(actions).toContain('setError(userFacingError(cause, "Could not load review actions."))');
	});

	it("keys automatic-review dismissal to the stable run id", () => {
		expect(detail).toContain("const autoReviewFailureId = autoReviewFailure?.id");
		expect(detail).toContain("[autoReviewFailureId, dismissedAutoFailureId]");
	});
});
