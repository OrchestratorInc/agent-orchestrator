import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const actions = readFileSync(new URL("../app/sheets/review-actions.tsx", import.meta.url), "utf8");
const detail = readFileSync(new URL("../app/review/[sessionId].tsx", import.meta.url), "utf8");
const pickerIOS = readFileSync(new URL("./reviewer-picker.ios.tsx", import.meta.url), "utf8");
const picker = readFileSync(new URL("./reviewer-picker.tsx", import.meta.url), "utf8");

describe("reviewer control integration", () => {
	it("keeps the project-default override separate from the effective reviewer", () => {
		expect(actions).toContain('const [reviewerOverride, setReviewerOverride] = useState("")');
		expect(actions).toContain("const effectiveReviewer = reviewerOverride || projectDefaultReviewer;");
		expect(actions).toContain("defaultReviewerHarness(project?.config?.reviewers, session.harness ?? undefined)");
		// The daemon's reviewerHarness names the last reviewer that ran, so it
		// must never pick the model catalog after a switch.
		expect(actions).not.toContain("result.reviewerHarness");
		expect(actions).not.toContain("reviewState.reviewerHarness");
		expect(actions).toContain("selectedReviewer={reviewerOverride}");
		expect(actions).toContain("effectiveReviewer={effectiveReviewer}");
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
		expect(picker).toContain("<AgentLogo harness={harness}");
	});

	it("picks the reviewer and model from native iOS menus like the spawn sheet", () => {
		expect(pickerIOS).toContain('import { Button, HStack, Image, Menu, Spacer, Text, VStack } from "@expo/ui/swift-ui"');
		expect(pickerIOS).toContain('accessibilityIdentifier("review-reviewer")');
		expect(pickerIOS).toContain('accessibilityIdentifier("review-model")');
	});

	it("only offers reviewers that can run, like the spawn sheet", () => {
		expect(actions).toContain(".filter((agent) => agent.selectable || agent.id === reviewerOverride)");
		expect(actions).toContain("reviewers={availableReviewers}");
	});

	it("uses the desktop inspector's automation wording and the system switch colors", () => {
		expect(actions).toContain('title="Auto review"');
		expect(actions).toContain('title="Automatically fix review comments"');
		expect(actions).toContain('title="Automatically fix CI failures"');
		expect(actions).not.toContain("trackColor");
	});

	it("does not let auto review lose its persistent reviewer", () => {
		expect(detail).toContain("!data.reviewerHandleId || autoReviewEnabled");
		expect(detail).toContain("disabled={Boolean(mutation) || autoReviewEnabled}");
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
		expect(actions).toContain("<ItemActionsMenu accessibilityLabel={`Actions for ${reviewerId}'s comment`}");
		expect(actions).toContain('{ id: "resolve", label: "Resolve"');
		expect(detail).toContain("<ItemActionsMenu accessibilityLabel={`Actions for the ${reviewVerdictLabel(run).toLowerCase()} review`}");
	});

	it("chooses Open, Restore, or Stop from what is actually available", () => {
		expect(detail).toContain("const controls = reviewerControls(data, review, sessionId);");
		expect(detail).toContain("{controls.stop ? <>");
		expect(detail).toContain("Push a new commit to run another review.");
	});

	it("merges like desktop: only when ready, fenced to the head commit, after confirmation", () => {
		expect(detail).toContain("const merge = pr ? mergeReadiness(pr) : undefined;");
		expect(detail).toContain("await mergeSessionPR(config, pr)");
		expect(detail).toContain("This will squash-merge PR #${pr.number} in the remote repository.");
	});

	it("keys automatic-review dismissal to the stable run id", () => {
		expect(detail).toContain("const autoReviewFailureId = autoReviewFailure?.id");
		expect(detail).toContain("[autoReviewFailureId, dismissedAutoFailureId]");
	});
});
