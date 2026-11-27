import { useLocalSearchParams } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, StyleSheet, View } from "react-native";
import {
	type AgentModelCatalog,
	type ReviewerAgentConfig,
	type SessionPRSummary,
	type SessionReviews,
} from "../../lib/api";
import { RECONNECTING_ACTION_COPY, userFacingError } from "../../lib/connectionError";
import { haptics } from "../../lib/haptics";
import { hostRouteMatches } from "../../lib/hostRoute";
import { ReviewActionsSheet } from "../../lib/review-actions-sheet";
import type { ReviewPolicyKey, ReviewPolicyRow } from "../../lib/review-actions-sheet.types";
import { useReviewBackend, type ReviewSettings } from "../../lib/reviewBackend";
import { reviewerEffortPick, reviewerModelPick, reviewerModelView, type ReviewerModelState } from "../../lib/reviewer-picker.types";
import { reviewerSelectionChanged, reviewerSwitchSelection, reviewerSwitchWarning } from "../../lib/reviewerControls";
import { pullRequestSummaryForURL, rerequestableReviewers, rerequestKey } from "../../lib/reviewView";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useThemedStyles } from "../../lib/ThemeProvider";
import { SHEET_SCROLL_CONTENT, SheetHeader } from "../../lib/ui";

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

type ReviewPolicies = Record<ReviewPolicyKey, boolean>;
type BusyAction = { kind: "reviewer" | "rerequest"; id: string } | { kind: "policy"; id: ReviewPolicyKey };

// The desktop inspector's wording, and what each switch reads before the session loads.
const POLICIES: readonly { key: ReviewPolicyKey; title: string; description: string; fallback: boolean }[] = [
	{ key: "autoReviewEnabled", title: "Auto review", description: "When enabled, PRs will get auto-reviewed. When disabled, you can manually trigger the reviews.", fallback: false },
	{ key: "autoInjectReview", title: "Automatically fix review comments", description: "Sends review comments to the worker.", fallback: true },
	{ key: "autoInjectCI", title: "Automatically fix CI failures", description: "Sends CI failures and merge conflicts to the worker for this session's PRs.", fallback: true },
];

export default function ReviewActionsRoute() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><ReviewActionsContent /></HostScope> : <ReviewActionsContent />;
}

function ReviewActionsContent() {
	const styles = useThemedStyles(makeStyles);
	const { currentHostId } = useApp();
	// The paired machine's daemon, or Orchestrator Cloud for a cloud worker;
	// what Cloud cannot do (pick the reviewer's model, ask a GitHub reviewer
	// again) the backend leaves out, and so does the sheet. The PR's comments
	// are on its page.
	const { backend } = useReviewBackend();
	const { sessionId = "", prUrl = "", hostId: routeHostId } = useLocalSearchParams<{ sessionId?: string; prUrl?: string; reviewer?: string; hostId?: string }>();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const [agents, setAgents] = useState<ReviewSettings["reviewers"]>([]);
	// With the reviewer it lists, so a reconnect keeps it on screen and only a new reviewer drops it.
	const [models, setModels] = useState<{ harness: string; catalog: AgentModelCatalog }>();
	const [reviewerConfig, setReviewerConfig] = useState<ReviewerAgentConfig>({});
	const [inheritedConfig, setInheritedConfig] = useState<ReviewSettings["inheritedConfig"]>({});
	const [pr, setPR] = useState<SessionPRSummary>();
	const [prMissing, setPRMissing] = useState(false);
	const [reviews, setReviews] = useState<SessionReviews>();
	const [reviewerOverride, setReviewerOverride] = useState("");
	const [projectDefaultReviewer, setProjectDefaultReviewer] = useState("");
	// The harness the next review runs under, resolved like desktop. The model
	// catalog must follow this, not the daemon's last-run reviewerHarness.
	const effectiveReviewer = reviewerOverride || projectDefaultReviewer;
	const modelRequest = useRef(0);
	const [policies, setPolicies] = useState<ReviewPolicies>();
	const [busy, setBusy] = useState<BusyAction>();
	const [error, setError] = useState("");
	// The machine's live connection is null while the app re-races its endpoints
	// (after an app switch). The sheet stays as it was: what this machine can do
	// comes from its last connection, requests only ever go over the live one,
	// and one asked for meanwhile says it is still reconnecting.
	const knownBackend = useRef(backend);
	if (backend) knownBackend.current = backend;
	const can = backend ?? knownBackend.current;
	// As of now, not as of the render that made a handler: a confirmation can stay open across a reconnect.
	const liveBackend = useRef(backend);
	liveBackend.current = backend;
	const connection = () => {
		if (!liveBackend.current) setError(RECONNECTING_ACTION_COPY);
		return liveBackend.current;
	};
	const connected = Boolean(backend);
	useEffect(() => {
		if (connected) setError((current) => current === RECONNECTING_ACTION_COPY ? "" : current);
	}, [connected]);

	const load = useCallback(async () => {
		if (!hostMatches || !backend || !sessionId) return;
		setError("");
		try {
			const [settings, prs, reviewState] = await Promise.all([backend.settings(sessionId), backend.pullRequests(sessionId), backend.reviews(sessionId)]);
			setAgents(settings.reviewers);
			setReviewerOverride(settings.selectedReviewer);
			setProjectDefaultReviewer(settings.defaultReviewer);
			setReviewerConfig(settings.reviewerConfig);
			setInheritedConfig(settings.inheritedConfig);
			const matchedPR = pullRequestSummaryForURL(prs, prUrl);
			setPR(matchedPR);
			setPRMissing(!matchedPR);
			setReviews(reviewState);
			setPolicies(settings.policies);
		} catch (cause) {
			setError(userFacingError(cause, "Could not load review actions."));
		}
	}, [backend, hostMatches, prUrl, sessionId]);

	useEffect(() => { void load(); }, [load]);
	useEffect(() => {
		const request = ++modelRequest.current;
		const reviewerModels = backend?.reviewerModels;
		if (!hostMatches || !reviewerModels || !effectiveReviewer) return;
		const polling = new AbortController();
		void reviewerModels(effectiveReviewer, polling.signal)
			.then((result) => { if (request === modelRequest.current) setModels({ harness: effectiveReviewer, catalog: result }); })
			.catch(() => { if (request === modelRequest.current) setModels(undefined); });
		return () => polling.abort();
	}, [backend, effectiveReviewer, hostMatches]);
	// Asked from here: left out at once, so a second tap cannot ask again before the provider's next read shows them waited on.
	const [asked, setAsked] = useState<ReadonlySet<string>>(new Set());
	const externalReviewers = useMemo(() => rerequestableReviewers(pr, reviews, asked), [asked, pr, reviews]);
	// Like the spawn sheet, only offer reviewers that can run now. A saved
	// override that has since become unavailable stays listed so it can be read.
	const availableReviewers = useMemo(() => agents
		.filter((agent) => agent.selectable || agent.id === reviewerOverride)
		.map((agent) => ({ id: agent.id, label: agent.label || agent.id })), [agents, reviewerOverride]);
	// What the next review runs with: the session's own model and effort, else
	// the project's for this reviewer, else the catalog's defaults.
	const modelState: ReviewerModelState = { catalog: effectiveReviewer && models?.harness === effectiveReviewer ? models.catalog : undefined, saved: reviewerConfig, inherited: inheritedConfig[effectiveReviewer] ?? {} };
	const modelView = reviewerModelView(modelState);

	// The switch, the picker and the request each show their own progress, so
	// none of them adds a haptic of its own; success is the moment worth feeling.
	async function saveReviewer(id: string, agentConfig: ReviewerAgentConfig) {
		const live = connection();
		if (!live) { setBusy(undefined); return; }
		setBusy({ kind: "reviewer", id });
		setError("");
		try {
			const selection = reviewerSwitchSelection(id, agentConfig);
			await live.setReviewer(sessionId, id, agentConfig);
			setReviewerOverride(id);
			setReviewerConfig(selection.agentConfig ?? {});
			haptics.success();
		} catch (cause) {
			setError(userFacingError(cause, "Could not change reviewer."));
		} finally {
			setBusy(undefined);
		}
	}

	async function confirmReviewerChange(id: string, agentConfig: ReviewerAgentConfig) {
		if (busy) return;
		if (!reviewerSelectionChanged(reviewerOverride, reviewerConfig, id, agentConfig)) return;
		const live = connection();
		if (!live) return;
		setBusy({ kind: "reviewer", id });
		setError("");
		try {
			const latest = await live.reviews(sessionId);
			setReviews(latest);
			const warning = reviewerSwitchWarning(latest.reviews.some((item) => item.status === "running"));
			if (!warning) { await saveReviewer(id, agentConfig); return; }
			Alert.alert("Change active reviewer?", warning, [
				{ text: "Keep current", style: "cancel", onPress: () => setBusy(undefined) },
				{ text: "Change reviewer", style: "destructive", onPress: () => void saveReviewer(id, agentConfig) },
			], { cancelable: true, onDismiss: () => setBusy(undefined) });
		} catch (cause) {
			setError(userFacingError(cause, "Could not check the active review before changing reviewer settings."));
			setBusy(undefined);
		}
	}

	function chooseReviewer(id: string) {
		const nextConfig = id && id === reviewerOverride ? reviewerConfig : {};
		void confirmReviewerChange(id, nextConfig);
	}

	function chooseModel(value: string) {
		void confirmReviewerChange(reviewerOverride, reviewerModelPick(modelState, value));
	}

	function chooseEffort(level: string) {
		void confirmReviewerChange(reviewerOverride, reviewerEffortPick(modelState, level));
	}

	async function updatePolicy(key: ReviewPolicyKey, value: boolean) {
		if (!policies || busy) return;
		const live = connection();
		if (!live) return;
		const previous = policies;
		setPolicies({ ...policies, [key]: value });
		setBusy({ kind: "policy", id: key });
		setError("");
		try {
			await live.setPolicy(sessionId, key, value);
			haptics.success();
		} catch (cause) {
			setPolicies(previous);
			setError(userFacingError(cause, "Could not update the review automation setting."));
		} finally {
			setBusy(undefined);
		}
	}

	async function rerequest(reviewerId: string) {
		if (!can?.rerequest || !pr || busy) return;
		const ask = connection()?.rerequest;
		if (!ask) return;
		setBusy({ kind: "rerequest", id: reviewerId });
		setError("");
		try {
			await ask(sessionId, pr.url, reviewerId);
			setAsked((current) => new Set(current).add(rerequestKey(reviewerId)));
			haptics.success();
		} catch (cause) {
			setError(userFacingError(cause, "Could not request another review."));
		} finally {
			setBusy(undefined);
		}
	}

	if (!hostMatches) return <View style={styles.screen}><View style={SHEET_SCROLL_CONTENT}><SheetHeader title="Review actions" subtitle="Review belongs to another machine" /></View></View>;

	const policyRows: ReviewPolicyRow[] = POLICIES.map(({ key, title, description, fallback }) => ({
		key,
		title,
		description,
		value: policies?.[key] ?? fallback,
		busy: !policies || busy?.kind === "policy" && busy.id === key,
	}));
	return <ReviewActionsSheet
		subtitle={pr ? `PR #${pr.number} · ${pr.title}` : "Reviewer and automation"}
		alerts={[
			...(error ? [error] : []),
			// Opened before the machine answers: say so, rather than spin without a word.
			...(!backend && !policies && error !== RECONNECTING_ACTION_COPY ? [RECONNECTING_ACTION_COPY] : []),
			...(prMissing ? ["Pull request not found. Its URL may have changed; reopen review details from the current pull request."] : []),
		]}
		policies={policyRows}
		reviewersLoading={!policies}
		locked={!policies || Boolean(busy)}
		onPolicyChange={(key, value) => void updatePolicy(key, value)}
		reviewer={availableReviewers.length || reviewerOverride ? {
			reviewers: availableReviewers,
			selectedReviewer: reviewerOverride,
			effectiveReviewer,
			onSelectReviewer: chooseReviewer,
			model: modelView.model,
			onSelectModel: chooseModel,
			effort: modelView.effort,
			onSelectEffort: chooseEffort,
			busy: Boolean(busy),
			modelChoice: Boolean(can?.reviewerModels),
		} : undefined}
		externalReviewers={(can?.rerequest ? externalReviewers : []).map((item) => ({
			id: item.reviewerId,
			detail: `${item.count} ${item.count === 1 ? "comment" : "comments"}`,
			busy: busy?.kind === "rerequest" && busy.id === item.reviewerId,
		}))}
		onRerequest={(id) => {
			if (externalReviewers.some((reviewer) => reviewer.reviewerId === id)) void rerequest(id);
		}}
	/>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgSurface },
});
