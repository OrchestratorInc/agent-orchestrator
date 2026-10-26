import { useLocalSearchParams } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, StyleSheet, View } from "react-native";
import {
	getAgents,
	getAgentModels,
	getProject,
	getSession,
	getSessionPR,
	getSessionReviews,
	requestSessionRereview,
	resolveSessionReviewComment,
	sendMessage,
	setSessionAutoInjectCI,
	setSessionAutoInjectReview,
	setSessionAutoReview,
	switchSessionReviewer,
	type AgentModelCatalog,
	type ReviewerAgentConfig,
	type PRReviewCommentLink,
	type PRReviewEntry,
	type PRUnresolvedReviewer,
	type SessionPRSummary,
	type SessionReviews,
} from "../../lib/api";
import { userFacingError } from "../../lib/connectionError";
import { haptics } from "../../lib/haptics";
import { hostRouteMatches } from "../../lib/hostRoute";
import { openGitHub } from "../../lib/openGitHub";
import { formatExternalReviewMessage, formatInlineReviewCommentMessage } from "../../lib/reviewFeedback";
import type { ItemAction } from "../../lib/item-actions-menu.types";
import { ReviewActionsSheet } from "../../lib/review-actions-sheet";
import type { ReviewFeedbackItem, ReviewFeedbackSection, ReviewPolicyKey, ReviewPolicyRow } from "../../lib/review-actions-sheet.types";
import { defaultReviewerHarness, reviewerChoices, reviewerSelectionChanged, reviewerSwitchSelection, reviewerSwitchWarning } from "../../lib/reviewerControls";
import { pullRequestSummaryForURL } from "../../lib/reviewView";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useThemedStyles } from "../../lib/ThemeProvider";
import { SHEET_SCROLL_CONTENT, SheetHeader } from "../../lib/ui";

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

type ReviewPolicies = Record<ReviewPolicyKey, boolean>;
type BusyAction = { kind: "reviewer" | "rerequest" | "resolve" | "send"; id: string } | { kind: "policy"; id: ReviewPolicyKey };

// The desktop inspector's wording, and what each switch reads before the session loads.
const POLICIES: readonly { key: ReviewPolicyKey; title: string; description: string; fallback: boolean }[] = [
	{ key: "autoReviewEnabled", title: "Auto review", description: "When enabled, PRs will get auto-reviewed. When disabled, you can manually trigger the reviews.", fallback: false },
	{ key: "autoInjectReview", title: "Automatically fix review comments", description: "Sends review comments to the worker.", fallback: true },
	{ key: "autoInjectCI", title: "Automatically fix CI failures", description: "Sends CI failures to the worker for this session's PRs.", fallback: true },
];

export default function ReviewActionsRoute() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><ReviewActionsContent /></HostScope> : <ReviewActionsContent />;
}

function ReviewActionsContent() {
	const styles = useThemedStyles(makeStyles);
	const { config, currentHostId } = useApp();
	const { sessionId = "", prUrl = "", hostId: routeHostId } = useLocalSearchParams<{ sessionId?: string; prUrl?: string; reviewer?: string; hostId?: string }>();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const [agents, setAgents] = useState<ReturnType<typeof reviewerChoices>>([]);
	const [models, setModels] = useState<AgentModelCatalog>();
	const [reviewerConfig, setReviewerConfig] = useState<ReviewerAgentConfig>({});
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
	const [sent, setSent] = useState<ReadonlySet<string>>(new Set());

	const load = useCallback(async () => {
		if (!hostMatches || !config || !sessionId) return;
		setError("");
		try {
			const [catalog, prs, session, reviewState] = await Promise.all([getAgents(config), getSessionPR(config, sessionId), getSession(config, sessionId), getSessionReviews(config, sessionId)]);
			setAgents(reviewerChoices(catalog));
			const project = session.projectId ? await getProject(config, session.projectId).catch(() => undefined) : undefined;
			setReviewerOverride(session.reviewerHarness || "");
			setProjectDefaultReviewer(defaultReviewerHarness(project?.config?.reviewers, session.harness ?? undefined));
			setReviewerConfig(session.reviewerConfig ?? {});
			const matchedPR = pullRequestSummaryForURL(prs, prUrl);
			setPR(matchedPR);
			setPRMissing(!matchedPR);
			setReviews(reviewState);
			setPolicies({
				autoReviewEnabled: session.autoReviewEnabled ?? false,
				autoInjectReview: session.autoInjectReview ?? true,
				autoInjectCI: session.autoInjectCI ?? true,
			});
		} catch (cause) {
			setError(userFacingError(cause, "Could not load review actions."));
		}
	}, [config, hostMatches, prUrl, sessionId]);

	useEffect(() => { void load(); }, [load]);
	useEffect(() => {
		const request = ++modelRequest.current;
		setModels(undefined);
		if (!hostMatches || !config || !effectiveReviewer) return;
		const polling = new AbortController();
		void getAgentModels(config, effectiveReviewer, undefined, polling.signal)
			.then((result) => { if (request === modelRequest.current) setModels(result); })
			.catch(() => { if (request === modelRequest.current) setModels(undefined); });
		return () => polling.abort();
	}, [config, effectiveReviewer, hostMatches]);
	const aoReviewIds = useMemo(() => new Set((reviews?.runs ?? []).filter((run) => run.prUrl === pr?.url).map((run) => run.githubReviewId).filter(Boolean)), [pr?.url, reviews?.runs]);
	const externalReviewers = useMemo(() => uniqueReviewers(pr, aoReviewIds), [aoReviewIds, pr]);
	// Like the spawn sheet, only offer reviewers that can run now. A saved
	// override that has since become unavailable stays listed so it can be read.
	const availableReviewers = useMemo(() => agents
		.filter((agent) => agent.selectable || agent.id === reviewerOverride)
		.map((agent) => ({ id: agent.id, label: agent.label || agent.id })), [agents, reviewerOverride]);
	const comments = useMemo(() => reviewComments(pr, false), [pr]);
	const resolvedComments = useMemo(() => reviewComments(pr, true), [pr]);
	const externalReviews = useMemo(() => (pr?.review.reviews ?? []).filter((item) => item.reviewerId !== pr?.author && ![...aoReviewIds].some((id) => item.reviewUrl?.includes(`pullrequestreview-${id}`))), [aoReviewIds, pr]);

	// The switch, the picker and the request each show their own progress, so
	// none of them adds a haptic of its own; success is the moment worth feeling.
	async function saveReviewer(id: string, agentConfig: ReviewerAgentConfig) {
		if (!config) return;
		setBusy({ kind: "reviewer", id });
		setError("");
		try {
			const selection = reviewerSwitchSelection(id, agentConfig);
			await switchSessionReviewer(config, sessionId, selection.harness, selection.agentConfig);
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
		if (!config || busy) return;
		if (!reviewerSelectionChanged(reviewerOverride, reviewerConfig, id, agentConfig)) return;
		setBusy({ kind: "reviewer", id });
		setError("");
		try {
			const latest = await getSessionReviews(config, sessionId);
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
		const key = models?.selectionMode === "mode" ? "mode" : "model";
		void confirmReviewerChange(reviewerOverride, { ...reviewerConfig, [key]: value });
	}

	async function updatePolicy(key: ReviewPolicyKey, value: boolean) {
		if (!config || !policies || busy) return;
		const previous = policies;
		setPolicies({ ...policies, [key]: value });
		setBusy({ kind: "policy", id: key });
		setError("");
		try {
			if (key === "autoReviewEnabled") await setSessionAutoReview(config, sessionId, value);
			else if (key === "autoInjectReview") await setSessionAutoInjectReview(config, sessionId, value);
			else await setSessionAutoInjectCI(config, sessionId, value);
			haptics.success();
		} catch (cause) {
			setPolicies(previous);
			setError(userFacingError(cause, "Could not update the review automation setting."));
		} finally {
			setBusy(undefined);
		}
	}

	async function rerequest(item: PRUnresolvedReviewer) {
		if (!config || !pr || busy) return;
		setBusy({ kind: "rerequest", id: item.reviewerId });
		setError("");
		try {
			await requestSessionRereview(config, sessionId, pr.url, item.reviewerId);
			haptics.success();
		} catch (cause) {
			setError(userFacingError(cause, "Could not request another review."));
		} finally {
			setBusy(undefined);
		}
	}

	function confirmResolve(comment: PRReviewCommentLink) {
		if (!config || !pr || busy) return;
		Alert.alert("Resolve feedback?", comment.body?.trim() || "Mark this addressed comment as resolved on GitHub.", [
			{ text: "Cancel", style: "cancel" },
			{ text: "Resolve", onPress: () => void resolve(comment) },
		]);
	}

	async function resolve(comment: PRReviewCommentLink) {
		if (!config || !pr) return;
		const commentUrl = comment.url;
		if (!commentUrl) return;
		setBusy({ kind: "resolve", id: commentUrl });
		setError("");
		try {
			await resolveSessionReviewComment(config, sessionId, pr.url, commentUrl);
			haptics.success();
			await load();
		} catch (cause) {
			setError(userFacingError(cause, "Could not resolve this comment."));
		} finally {
			setBusy(undefined);
		}
	}

	async function send(id: string, message: string) {
		if (!config || busy) return;
		setBusy({ kind: "send", id });
		setError("");
		try {
			await sendMessage(config, sessionId, message);
			setSent((current) => new Set(current).add(id));
			haptics.success();
		} catch (cause) {
			setError(userFacingError(cause, "Could not send this feedback to the worker."));
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
	const reviewItem = (item: PRReviewEntry): ReviewFeedbackItem => {
		const id = item.reviewUrl || item.reviewerId;
		const actions: ItemAction[] = [
			...(item.reviewUrl ? [{ id: "open", label: "Open review", icon: "openExternal" as const, onPress: () => void openGitHub(item.reviewUrl!, { fromSheet: true }) }] : []),
			...(item.body && pr && !sent.has(id) ? [{ id: "send", label: "Send to worker", icon: "send" as const, onPress: () => void send(id, formatExternalReviewMessage(item, pr.url)) }] : []),
		];
		return {
			key: item.reviewUrl || `${item.reviewerId}:${item.submittedAt}`,
			author: item.reviewerId,
			meta: sentenceCase(item.verdict.replaceAll("_", " ")),
			body: item.body || "No written summary.",
			bodyMissing: !item.body,
			notInjected: item.autoInjectReview === false,
			sent: sent.has(id),
			menuLabel: `Actions for ${item.reviewerId}'s review`,
			actions,
			working: busy?.kind === "send" && busy.id === id,
		};
	};
	// Desktop keeps these in a per-comment "⋯" menu rather than as buttons under every comment.
	const commentItem = (item: ReviewCommentItem, resolved: boolean): ReviewFeedbackItem => {
		const { reviewerId, comment } = item;
		const id = commentKey(item);
		const aoOwned = Boolean(comment.reviewId && aoReviewIds.has(comment.reviewId));
		const actions: ItemAction[] = [
			...(comment.url ? [{ id: "open", label: comment.file ? "Open file & line" : "Open on GitHub", icon: "openExternal" as const, onPress: () => void openGitHub(comment.url!, { fromSheet: true }) }] : []),
			...(!sent.has(id) ? [{ id: "send", label: "Send to worker", icon: "send" as const, onPress: () => void send(id, formatInlineReviewCommentMessage(comment, reviewerId)) }] : []),
			...(!resolved && comment.url ? [{ id: "resolve", label: "Resolve", icon: "resolve" as const, onPress: () => confirmResolve(comment) }] : []),
		];
		return {
			key: id,
			author: reviewerId,
			meta: `${aoOwned ? "Orchestrator review" : "GitHub"} · ${resolved ? "resolved" : "open"}`,
			location: comment.file ? `${comment.file}${comment.line ? `:${comment.line}` : ""}` : "Inline comment",
			body: comment.body || "No comment text.",
			bodyMissing: !comment.body,
			notInjected: comment.autoInjectReview === false,
			sent: sent.has(id),
			menuLabel: `Actions for ${reviewerId}'s comment`,
			actions,
			working: (busy?.kind === "send" && busy.id === id) || (busy?.kind === "resolve" && busy.id === comment.url),
		};
	};
	const feedback: ReviewFeedbackSection[] = [
		{ id: "reviews" as const, title: "GitHub reviews", footer: "Complete review summaries submitted on GitHub.", items: pr ? externalReviews.map(reviewItem) : [] },
		{ id: "unresolved" as const, title: "Unresolved comments", footer: "Use ⋯ to open the exact GitHub file and line, send feedback to the worker, or resolve it once addressed.", items: comments.map((item) => commentItem(item, false)) },
		{ id: "resolved" as const, title: "Resolved comments", footer: "Previously addressed Orchestrator and GitHub feedback.", items: resolvedComments.map((item) => commentItem(item, true)) },
	].filter((section) => section.items.length > 0);

	return <ReviewActionsSheet
		subtitle={pr ? `PR #${pr.number} · ${pr.title}` : "Reviewer and GitHub feedback"}
		alerts={[
			...(error ? [error] : []),
			...(prMissing ? ["Pull request not found. Its URL may have changed; reopen review details from the current pull request."] : []),
		]}
		policies={policyRows}
		locked={!policies || Boolean(busy)}
		onPolicyChange={(key, value) => void updatePolicy(key, value)}
		reviewer={availableReviewers.length || reviewerOverride ? {
			reviewers: availableReviewers,
			selectedReviewer: reviewerOverride,
			effectiveReviewer,
			onSelectReviewer: chooseReviewer,
			models: effectiveReviewer ? (models?.models ?? []).map((model) => ({ id: model.id, label: model.label || model.id })) : [],
			modelTitle: models?.selectionMode === "mode" ? "Mode" : "Model",
			selectedModel: (models?.selectionMode === "mode" ? reviewerConfig.mode : reviewerConfig.model) ?? "",
			onSelectModel: chooseModel,
			busy: Boolean(busy),
		} : undefined}
		externalReviewers={externalReviewers.map((item) => ({
			id: item.reviewerId,
			detail: `${item.count} ${item.count === 1 ? "comment" : "comments"}`,
			busy: busy?.kind === "rerequest" && busy.id === item.reviewerId,
		}))}
		onRerequest={(id) => {
			const item = externalReviewers.find((reviewer) => reviewer.reviewerId === id);
			if (item) void rerequest(item);
		}}
		feedback={feedback}
		emptyFeedback={!pr && !error && !prMissing ? "No GitHub feedback is available for this pull request yet." : undefined}
	/>;
}

function uniqueReviewers(pr: SessionPRSummary | undefined, aoReviewIds: Set<string>): PRUnresolvedReviewer[] {
	const byId = new Map<string, PRUnresolvedReviewer>();
	for (const item of [...(pr?.review.unresolvedBy ?? []), ...(pr?.review.resolvedBy ?? [])]) {
		const links = item.links.filter((link) => !link.reviewId || !aoReviewIds.has(link.reviewId));
		if (item.reviewerId && item.reviewerId !== pr?.author && links.length && !byId.has(item.reviewerId)) byId.set(item.reviewerId, { ...item, count: links.length, links });
	}
	for (const item of pr?.review.reviews ?? []) {
		const isAOReview = [...aoReviewIds].some((id) => item.reviewUrl?.includes(`pullrequestreview-${id}`));
		if (!isAOReview && item.reviewerId && item.reviewerId !== pr?.author && !byId.has(item.reviewerId)) byId.set(item.reviewerId, { reviewerId: item.reviewerId, count: 0, links: [] });
	}
	return [...byId.values()];
}

type ReviewCommentItem = { reviewerId: string; comment: PRReviewCommentLink };

function reviewComments(pr: SessionPRSummary | undefined, resolved: boolean): ReviewCommentItem[] {
	const result: ReviewCommentItem[] = [];
	for (const reviewer of resolved ? pr?.review.resolvedBy ?? [] : pr?.review.unresolvedBy ?? []) {
		for (const comment of reviewer.links ?? []) result.push({ reviewerId: reviewer.reviewerId, comment });
	}
	return result;
}

function commentKey({ reviewerId, comment }: ReviewCommentItem): string {
	return comment.url || `${reviewerId}:${comment.file}`;
}

function sentenceCase(value: string): string {
	return value ? value[0].toUpperCase() + value.slice(1) : value;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgSurface },
});
