import { Stack, useFocusEffect, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import GitCommitHorizontal from "lucide-react-native/icons/git-commit-horizontal";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Alert, Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from "react-native";
import { cancelSessionReview, getSession, getSessionPR, getSessionReviews, killSessionReviewer, mergeSessionPR, restoreSessionReviewer, sendMessage, sessionTitle, triggerSessionReview, type DashboardSession, type ReviewRun, type SessionPRSummary, type SessionReviews } from "../../lib/api";
import { ChatMarkdown } from "../../lib/chat/ChatMarkdown";
import { userFacingError } from "../../lib/connectionError";
import { AppSymbol } from "../../lib/AppSymbol";
import { haptics } from "../../lib/haptics";
import { Feather } from "../../lib/icons";
import { hostRouteMatches } from "../../lib/hostRoute";
import { ItemActionsMenu } from "../../lib/item-actions-menu";
import type { ItemAction } from "../../lib/item-actions-menu.types";
import { openGitHub } from "../../lib/openGitHub";
import { useOpenPage } from "../../lib/pageNavigation";
import { mergeReadiness, type MergeTone } from "../../lib/prMerge";
import { usePressFeedback } from "../../lib/pressFeedback";
import { refreshControlColors } from "../../lib/refreshControlColors";
import { ReviewActionButton } from "../../lib/review-action-button";
import { formatReviewSummaryMessage, reviewRunsForPullRequest, reviewRunUrl } from "../../lib/reviewFeedback";
import { latestAutoReviewFailure, previousRouteIsWorker, pullRequestSummaryForURL, reviewBatchAction, reviewerControls, reviewerDestination, reviewForPullRequest, reviewPrimaryActionLabel, reviewRunMeta, reviewRunSendable, reviewStatusLabel, reviewVerdictLabel, shortCommit, workerRouteForSession } from "../../lib/reviewView";
import { HostScope, useApp } from "../../lib/store";
import { statusVisual, type Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { AgentLogo } from "../../lib/AgentLogo";
import { rowDividerWidth } from "../../lib/divider";
import { Spinner } from "../../lib/Spinner";
import { SwipeToMerge } from "../../lib/SwipeToMerge";
import { symbols } from "../../lib/symbols";
import { toolbarIcon } from "../../lib/toolbarIcon";
import { iconSize, space, touchTarget, type } from "../../lib/tokens";
import { Button, Dot, EmptyState, ListSectionHeader } from "../../lib/ui";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

export default function ReviewDetailScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><ReviewDetailContent /></HostScope> : <ReviewDetailContent />;
}

function ReviewDetailContent() {
	const t = useTheme();
	const insets = useSafeAreaInsets();
	const styles = useThemedStyles(makeStyles);
	const navigation = useNavigation();
	const router = useRouter();
	const openPage = useOpenPage();
	const { sessionId, prUrl, prNumber, hostId: routeHostId } = useLocalSearchParams<{ sessionId: string; prUrl?: string; prNumber?: string; hostId?: string }>();
	const { config, sessions, currentHostId, restore } = useApp();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const listedWorker = sessions.find((session) => session.id === sessionId);
	// The board poll holds live workers only on a daemon that pages its archive,
	// so an archived worker is read from its machine. The listed copy wins, so the
	// row flips to Open once a restore refreshes the host. The read is fresh each
	// time the worker leaves the poll, not readSession's cached answer: a worker
	// first read while live and later archived would otherwise keep its old status.
	const [readWorker, setReadWorker] = useState<DashboardSession | null>();
	const listed = Boolean(listedWorker);
	useEffect(() => {
		if (listed) { setReadWorker(undefined); return; }
		if (!hostMatches || !config || !sessionId) return;
		let current = true;
		void getSession(config, sessionId).then((read) => read, () => null).then((read) => { if (current) setReadWorker(read); });
		return () => { current = false; };
	}, [config, hostMatches, listed, sessionId]);
	const worker = listedWorker ?? readWorker ?? undefined;
	const autoReviewEnabled = worker?.autoReviewEnabled === true;
	const [data, setData] = useState<SessionReviews>();
	const [prs, setPRs] = useState<SessionPRSummary[]>([]);
	const [sentRuns, setSentRuns] = useState<ReadonlySet<string>>(new Set());
	// The daemon learns a merge from its next GitHub observation, seconds after
	// the merge call returns. Show the merge at once and poll until it agrees.
	const [mergedURL, setMergedURL] = useState<string>();
	const [error, setError] = useState("");
	// Kept apart from `error`: a quiet poll clears that, and it renders far below the row.
	const [workerError, setWorkerError] = useState("");
	const [reviewNotice, setReviewNotice] = useState("");
	const [dismissedAutoFailureId, setDismissedAutoFailureId] = useState<string>();
	const [refreshing, setRefreshing] = useState(false);
	const [mutation, setMutation] = useState<"review" | "restore" | string>();
	// The reviewer row keeps clear of the button and menu laid over its end,
	// whose width follows the label and the text size.
	const [trailingWidth, setTrailingWidth] = useState<number>();
	const rowFeedback = usePressFeedback("row");
	const dismissFeedback = usePressFeedback("icon");
	const latestLoad = useRef(0);

	const load = useCallback(async (quiet = false) => {
		if (!hostMatches || !config || !sessionId) return;
		const request = ++latestLoad.current;
		if (!quiet) setError("");
		try {
			const [next, nextPRs] = await Promise.all([getSessionReviews(config, sessionId), getSessionPR(config, sessionId).catch(() => undefined)]);
			if (request === latestLoad.current) {
				setData(next);
				if (nextPRs) setPRs(nextPRs);
				setError("");
			}
		} catch (value) {
			if (!quiet && request === latestLoad.current) setError(userFacingError(value, "Could not load this review."));
		}
	}, [config, hostMatches, sessionId]);

	useFocusEffect(useCallback(() => { void load(); }, [load]));
	const review = reviewForPullRequest(data?.reviews ?? [], prUrl, Number(prNumber) || undefined);
	const autoReviewFailure = latestAutoReviewFailure(data?.reviews ?? [], autoReviewEnabled);
	const autoReviewFailureId = autoReviewFailure?.id;
	useEffect(() => {
		if (!autoReviewFailureId || autoReviewFailureId === dismissedAutoFailureId) return;
		const timer = setTimeout(() => setDismissedAutoFailureId(autoReviewFailureId), 10_000);
		return () => clearTimeout(timer);
	}, [autoReviewFailureId, dismissedAutoFailureId]);
	useLayoutEffect(() => navigation.setOptions({
		title: review?.title || "Pull request",
		headerTitleStyle: { color: t.textPrimary, fontFamily: "DMSans_600SemiBold", fontWeight: "600" },
	}), [navigation, review?.title, t.textPrimary]);
	// Memoized: each new element re-registers the header's items, and the page
	// re-renders on every poll while a review runs.
	const reviewPRUrl = review?.prUrl;
	const reviewPRNumber = review?.prNumber;
	const harness = data?.reviewerHarness;
	const headerItems = useMemo(() => reviewPRUrl ? (
		<Stack.Toolbar placement="right">
			<Stack.Toolbar.Button icon={toolbarIcon("openExternal")} accessibilityLabel={`Open pull request ${reviewPRNumber} in GitHub`} onPress={() => void openGitHub(reviewPRUrl)} />
			<Stack.Toolbar.Button icon={toolbarIcon("more")} accessibilityLabel="More review actions" onPress={() => router.push({ pathname: "/sheets/review-actions", params: { sessionId, prUrl: reviewPRUrl, reviewer: harness ?? "", hostId: routeHostId } })} />
		</Stack.Toolbar>
	) : null, [harness, reviewPRNumber, reviewPRUrl, routeHostId, router, sessionId]);
	const observedPR = review ? pullRequestSummaryForURL(prs, review.prUrl) : undefined;
	const awaitingMerge = Boolean(mergedURL && observedPR && observedPR.url === mergedURL && observedPR.state !== "merged");
	useEffect(() => {
		if (!awaitingMerge) return;
		const timer = setInterval(() => void load(true), 2_000);
		const giveUp = setTimeout(() => setMergedURL(undefined), 60_000);
		return () => { clearInterval(timer); clearTimeout(giveUp); };
	}, [awaitingMerge, load]);
	useEffect(() => {
		if (review?.status !== "running" && !autoReviewEnabled) return;
		const timer = setInterval(() => void load(true), 2_000);
		return () => clearInterval(timer);
	}, [autoReviewEnabled, load, review?.status]);

	const refresh = async () => {
		setRefreshing(true);
		await load();
		setRefreshing(false);
	};

	if (!hostMatches) return <View style={styles.center}><EmptyState icon="git-pull-request" title="Review belongs to another machine" message="Open it from that machine's session." /></View>;
	if (!data && !error) return <View style={styles.center}><Spinner size="large" accessibilityLabel="Loading review" /></View>;
	if (!data || !review) return <EmptyState icon={error ? "warning" : "git-pull-request"} iconColor={error ? t.red : undefined} title={error ? "Could not load review" : "No review found"} message={error || "Orchestrator has no review state for this pull request yet."} action={<Button title="Try again" icon="refresh" onPress={() => void load()} />} />;
	const primaryAction = reviewBatchAction(review, data.reviews);
	const runs = reviewRunsForPullRequest([...(data.runs ?? []), ...(review.latestRun ? [review.latestRun] : []), ...(review.previousRun ? [review.previousRun] : [])], review.prUrl);
	const multiplePullRequests = data.reviews.length > 1;
	const openReviewer = () => {
		const destination = reviewerDestination(data, review, sessionId, routeHostId);
		if (!destination) return;
		openPage(destination);
	};
	const restoreReviewer = async () => {
		if (!config || mutation) return;
		haptics.tap();
		setMutation("restore");
		setError("");
		try {
			await restoreSessionReviewer(config, sessionId);
			await load();
		} catch (value) {
			setError(userFacingError(value, "Could not restore the reviewer."));
		} finally {
			setMutation(undefined);
		}
	};
	const openWorker = () => {
		if (!worker) return;
		const state = navigation.getState();
		if (state && previousRouteIsWorker(state.routes, state.index, worker.id) && router.canGoBack()) router.back();
		else openPage(workerRouteForSession(worker, routeHostId ?? currentHostId ?? undefined));
	};
	const restoreWorker = async () => {
		if (!worker || mutation) return;
		setMutation("restoreWorker");
		setWorkerError("");
		try {
			await restore(worker.id, routeHostId ?? currentHostId ?? undefined);
		} catch (value) {
			setWorkerError(userFacingError(value, "Could not restore the worker."));
		} finally {
			setMutation(undefined);
		}
	};
	const confirmKillReviewer = () => {
		if (!config || mutation || !data.reviewerHandleId || autoReviewEnabled) return;
		Alert.alert("Stop reviewer session?", "This closes the persistent reviewer and cancels any review it is currently running. Review history is preserved.", [
			{ text: "Keep reviewer", style: "cancel" },
			{ text: "Stop reviewer", style: "destructive", onPress: () => void (async () => {
				setMutation("kill"); setError("");
				try { setData(await killSessionReviewer(config, sessionId)); haptics.success(); }
				catch (value) { setError(userFacingError(value, "Could not stop the reviewer session.")); }
				finally { setMutation(undefined); }
			})() },
		]);
	};
	const runPrimaryAction = async () => {
		if (!config || primaryAction === "none" || mutation) return;
		haptics.tap();
		setMutation("review");
		setError("");
		setReviewNotice("");
		try {
			if (primaryAction === "cancel") await cancelSessionReview(config, sessionId);
			else {
				const result = await triggerSessionReview(config, sessionId);
				if (!result.created) setReviewNotice("This commit has already been reviewed. Push a new commit to run another review.");
			}
			await load();
		} catch (value) {
			setError(userFacingError(value, "The review action failed."));
		} finally {
			setMutation(undefined);
		}
	};
	const sendRun = async (run: ReviewRun) => {
		if (!config || mutation) return;
		setMutation(`send:${run.id}`);
		setError("");
		try {
			await sendMessage(config, sessionId, formatReviewSummaryMessage(run), { userAuthored: true });
			setSentRuns((current) => new Set(current).add(run.id));
			haptics.success();
		} catch (value) {
			setError(userFacingError(value, "Could not send this review to the worker."));
		} finally {
			setMutation(undefined);
		}
	};

	const controls = reviewerControls(data, review, sessionId);
	const pr = observedPR && awaitingMerge ? { ...observedPR, state: "merged" as const } : observedPR;
	const merge = pr ? mergeReadiness(pr) : undefined;
	const closedPR = pr?.state === "merged" || pr?.state === "closed";
	const merging = mutation === "merge";
	// The swipe itself is the confirmation; only the screen reader path asks again.
	const runMerge = async (): Promise<boolean> => {
		if (!config || !pr || !merge?.canMerge || (mutation && !merging)) return false;
		setMutation("merge"); setError("");
		try { await mergeSessionPR(config, pr); setMergedURL(pr.url); haptics.success(); await load(true); return true; }
		catch (value) { setError(userFacingError(value, `Could not merge PR #${pr.number}.`)); return false; }
		finally { setMutation(undefined); }
	};
	const confirmMerge = () => {
		if (!config || !pr || !merge?.canMerge || mutation) return;
		Alert.alert(`Merge PR #${pr.number}?`, `This will squash-merge PR #${pr.number} in the remote repository.`, [
			{ text: "Cancel", style: "cancel" },
			{ text: "Merge", onPress: () => void runMerge() },
		]);
	};

	const reviewerHarness = data.reviewerSurface?.harness || data.reviewerHarness || "";
	const activity = data.reviewerActivityState;
	const activityColor = activity === "active" ? t.orange : activity === "exited" ? statusVisual(t, "exited").color : t.textTertiary;
	const stopActions: ItemAction[] = controls.stop && !autoReviewEnabled
		? [{ id: "stop", label: "Stop reviewer session", icon: "stop", destructive: true, onPress: confirmKillReviewer }]
		: [];
	// The review row describes the AO review itself; merge state lives in the PR row.
	const latestRun = review.latestRun ?? review.previousRun;
	const reviewLabel = review.status === "running" ? reviewStatusLabel("running") : latestRun ? reviewVerdictLabel(latestRun) : reviewStatusLabel(review.status);
	const reviewColor = review.status === "running" ? t.orange : latestRun?.verdict === "approved" ? t.green : latestRun?.verdict === "changes_requested" ? t.amber : latestRun?.status === "failed" ? t.red : t.textSecondary;
	const reviewDetail = closedPR ? `No new reviews: this pull request is ${pr?.state}.` : reviewStatusLabel(review.status);
	const reviewNote = primaryAction !== "cancel" && autoReviewEnabled
		? "Automatic review is watching for new commits. Turn it off in Review actions to run reviews manually."
		: primaryAction !== "none" && multiplePullRequests ? "Applies to every eligible pull request in this session." : undefined;

	return (
		<View style={styles.screen}>
		{headerItems}
		<ScrollView style={styles.scroll} contentContainerStyle={styles.content} refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} {...refreshControlColors(t)} />}>
			{/* Flat rows under section labels, like the worker board and project pages. */}
			<ListSectionHeader label="Pull request" />
			<View style={styles.row}>
				<View style={styles.main}>
					<View style={styles.eyebrow}>
						<Feather name="git-pull-request" size={iconSize.sm} color={t.textSecondary} />
						<Text style={styles.eyebrowText} numberOfLines={1}>PR #{review.prNumber}{pr?.targetBranch ? ` into ${pr.targetBranch}` : ""}</Text>
					</View>
					<Text style={styles.title} numberOfLines={2}>{review.title}</Text>
					{merge ? <View style={styles.statusLine}>
						<Dot color={toneColor(t, merge.tone)} size={6} />
						<Text style={[styles.statusStrong, { color: toneColor(t, merge.tone) }]}>{merge.label}</Text>
					</View> : null}
					{merge?.reason ? <Text style={styles.detail}>{merge.reason}</Text> : null}
				</View>
			</View>

			{worker ? <>
				<ListSectionHeader label="Worker" />
				<WorkerRow worker={worker} restoring={mutation === "restoreWorker"} disabled={Boolean(mutation)} onOpen={openWorker} onRestore={() => void restoreWorker()} />
				{workerError ? <Text accessibilityRole="alert" style={[styles.note, styles.noteError]}>{workerError}</Text> : null}
			</> : null}

			<ListSectionHeader label="Reviewer" />
			{/* The row opens the reviewer; its button and menu are laid over the row
			    rather than inside its Pressable (as on the project page's orchestrator
			    row), so a tap on "⋯" never also opens the reviewer. Its Open is outlined
			    when a Worker row is shown, so the worker's filled Open stays the main way out. */}
			<View>
			<Pressable accessibilityRole={controls.open ? "button" : undefined} accessibilityLabel={controls.open ? `Open reviewer ${controls.open}` : undefined} disabled={!controls.open || Boolean(mutation)} onPress={openReviewer} android_ripple={rowFeedback.android_ripple} style={({ pressed }) => [styles.row, { paddingRight: trailingWidth === undefined ? OVERLAY_RESERVE : trailingWidth + OVERLAY_INSET + space.md }, pressed && rowFeedback.pressed]}>
				<View style={styles.main}>
					<View style={styles.eyebrow}>
						{reviewerHarness ? <AgentLogo harness={reviewerHarness} size={iconSize.sm} /> : <AppSymbol name="account" size={iconSize.sm} />}
						<Text style={styles.eyebrowText} numberOfLines={1}>Orchestrator reviewer</Text>
					</View>
					<Text style={styles.title} numberOfLines={1}>{reviewerName(reviewerHarness)}</Text>
					<View style={styles.statusLine}>
						<Dot color={activityColor} size={6} breathing={activity === "active"} />
						<Text style={[styles.statusStrong, { color: activityColor }]}>{activity ? capitalize(activity.replaceAll("_", " ")) : "Not started"}</Text>
						<Text style={styles.statusDetail} numberOfLines={1}>{controls.open === "chat" ? " · chat" : controls.open === "terminal" ? " · terminal" : ""}</Text>
					</View>
				</View>
			</Pressable>
				<View style={styles.overlaySlot} pointerEvents="box-none">
				<View style={styles.trailing} onLayout={(event) => setTrailingWidth(event.nativeEvent.layout.width)}>
					{controls.open
						? <ReviewActionButton label="Open" accessibilityLabel="Open reviewer" icon={controls.open === "chat" ? symbols.chat : symbols.terminal} tone={worker ? "outlined" : "filled"} disabled={Boolean(mutation)} onPress={openReviewer} />
						: controls.restore ? <ReviewActionButton label="Restore" accessibilityLabel="Restore reviewer" icon={symbols.refresh} tone="outlined" busy={mutation === "restore"} disabled={Boolean(mutation)} onPress={() => void restoreReviewer()} /> : null}
					<ItemActionsMenu accessibilityLabel="Reviewer session actions" actions={stopActions} loading={mutation === "kill"} disabled={Boolean(mutation)} />
				</View>
				</View>
			</View>
			{controls.stop && autoReviewEnabled ? <Text style={styles.note}>Turn off automatic review before stopping its reviewer session.</Text> : null}
			{data.reviewerSurface?.controllerError ? <Text accessibilityRole="alert" style={[styles.note, styles.noteError]}>{data.reviewerSurface.controllerError}</Text> : null}

			<ListSectionHeader label="Orchestrator review" />
			<View style={styles.row}>
				<View style={styles.main}>
					<View style={styles.eyebrow}>
						<GitCommitHorizontal size={iconSize.sm} color={t.textSecondary} strokeWidth={2} />
						<Text style={[styles.eyebrowText, styles.mono]} numberOfLines={1}>{shortCommit(review.targetSha)}</Text>
					</View>
					<Text style={styles.title} numberOfLines={1}>{reviewLabel}</Text>
					<View style={styles.statusLine}>
						<Dot color={reviewColor} size={6} breathing={review.status === "running"} />
						<Text style={styles.detail} numberOfLines={2}>{reviewDetail}</Text>
					</View>
					{reviewNote ? <Text style={styles.detail}>{reviewNote}</Text> : null}
				</View>
				{primaryAction !== "none" ? <ReviewActionButton label={reviewPrimaryActionLabel(primaryAction, multiplePullRequests)} icon={primaryAction === "cancel" ? symbols.close : PLAY} tone={primaryAction === "cancel" ? "destructive" : primaryAction === "start" ? "filled" : "outlined"} busy={mutation === "review"} disabled={Boolean(mutation) || primaryAction !== "cancel" && autoReviewEnabled} onPress={() => void runPrimaryAction()} /> : null}
			</View>
			{reviewNotice ? <View style={styles.inlineNote}><AppSymbol name="checkmark" size={iconSize.sm} color={t.green} /><Text style={[styles.inlineNoteText, { color: t.green }]}>{reviewNotice}</Text></View> : null}
			{autoReviewFailure && autoReviewFailure.id !== dismissedAutoFailureId ? <View accessibilityRole="alert" style={styles.inlineNote}>
				<AppSymbol name="error" size={iconSize.sm} color={t.red} />
				<Text style={[styles.inlineNoteText, { color: t.red }]}><Text style={styles.inlineNoteStrong}>Automatic review failed. </Text>{autoReviewFailure.body.trim()}</Text>
				<Pressable accessibilityRole="button" accessibilityLabel="Dismiss automatic review failure" android_ripple={dismissFeedback.android_ripple} onPress={() => setDismissedAutoFailureId(autoReviewFailure.id)} style={({ pressed }) => [styles.dismiss, pressed && dismissFeedback.pressed]}><AppSymbol name="close" size={iconSize.md} color={t.red} /></Pressable>
			</View> : null}
			{error ? <Text accessibilityRole="alert" style={[styles.note, styles.noteError]}>{error}</Text> : null}

			<ListSectionHeader label="History" count={runs.length} />
			{runs.length
				? runs.map((run) => <RunRow key={run.id} run={run} sent={sentRuns.has(run.id)} sending={mutation === `send:${run.id}`} disabled={Boolean(mutation)} onSend={() => void sendRun(run)} />)
				: <Text style={styles.note}>No Orchestrator review for this pull request yet.</Text>}
		</ScrollView>
		{/* Docked under the scroll, within thumb reach, like a checkout's slide-to-pay.
		    It stays after the merge as a purple "Merged" bar rather than vanishing. */}
		{pr && (merge?.canMerge || pr.state === "merged") ? <View style={[styles.mergeFooter, { paddingBottom: Math.max(insets.bottom, space.md) }]}>
			<SwipeToMerge label={`Swipe to merge PR #${pr.number}`} busyLabel="Merging…" merged={pr.state === "merged"} mergedLabel={`Merged PR #${pr.number}`} busy={merging} disabled={Boolean(mutation) && !merging} onMerge={runMerge} onAccessibilityActivate={confirmMerge} />
		</View> : null}
		</View>
	);
}

/**
 * The worker that owns this pull request, so the PR page leads back to its chat
 * the way the chat leads to the PR page. Built like the reviewer row below it.
 */
function WorkerRow({ worker, restoring, disabled, onOpen, onRestore }: { worker: DashboardSession; restoring: boolean; disabled: boolean; onOpen: () => void; onRestore: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const rowFeedback = usePressFeedback("row");
	// The row keeps clear of the button laid over its end.
	const [trailingWidth, setTrailingWidth] = useState<number>();
	const title = sessionTitle(worker);
	const terminated = worker.isTerminated === true || worker.status === "terminated";
	const visual = statusVisual(t, worker.status);
	// The daemon's phrase ("Fixing CI failures") when it says more than the status.
	const phrase = worker.displayStatus?.trim();
	const detail = phrase && phrase.toLowerCase() !== visual.label.toLowerCase() ? ` · ${phrase}` : "";
	return <View>
		<Pressable accessibilityRole="button" accessibilityLabel={`Open worker ${title}. ${visual.label}.`} disabled={disabled} onPress={onOpen} android_ripple={rowFeedback.android_ripple} style={({ pressed }) => [styles.row, { paddingRight: trailingWidth === undefined ? WORKER_RESERVE : trailingWidth + OVERLAY_INSET + space.md }, pressed && rowFeedback.pressed]}>
			<View style={styles.main}>
				<View style={styles.eyebrow}>
					{worker.harness ? <AgentLogo harness={worker.harness} size={iconSize.sm} /> : <AppSymbol name="account" size={iconSize.sm} />}
					<Text style={styles.eyebrowText} numberOfLines={1}>{worker.harness ? reviewerName(worker.harness) : "Worker"}</Text>
				</View>
				<Text style={styles.title} numberOfLines={1}>{title}</Text>
				<View style={styles.statusLine}>
					<Dot color={visual.color} size={6} breathing={visual.breathing} />
					<Text style={[styles.statusStrong, { color: visual.color }]}>{visual.label}</Text>
					<Text style={styles.statusDetail} numberOfLines={1}>{detail}</Text>
				</View>
			</View>
		</Pressable>
		{/* Laid over the row, like the reviewer's, so the button is its own target. */}
		<View style={styles.overlaySlot} pointerEvents="box-none">
			<View onLayout={(event) => setTrailingWidth(event.nativeEvent.layout.width)}>
				{terminated
					? <ReviewActionButton label="Restore" accessibilityLabel="Restore worker" icon={symbols.refresh} tone="outlined" busy={restoring} disabled={disabled} onPress={onRestore} />
					: <ReviewActionButton label="Open" accessibilityLabel="Open worker" icon={symbols.chat} tone="filled" disabled={disabled} onPress={onOpen} />}
			</View>
		</View>
	</View>;
}

function RunRow({ run, sent, sending, disabled, onSend }: { run: ReviewRun; sent: boolean; sending: boolean; disabled: boolean; onSend: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const requested = run.verdict === "changes_requested";
	const color = requested ? t.amber : run.verdict === "approved" ? t.green : run.status === "failed" ? t.red : t.textSecondary;
	const url = reviewRunUrl(run);
	// Desktop keeps these in a per-review "⋯" menu.
	const actions: ItemAction[] = [
		...(url ? [{ id: "open", label: "Open on GitHub", icon: "openExternal" as const, onPress: () => void openGitHub(url) }] : []),
		...(reviewRunSendable(run) && !sent ? [{ id: "send", label: "Send to worker", icon: "send" as const, onPress: onSend }] : []),
	];
	return <View style={styles.runRow}>
		<View style={styles.runHead}>
			<Dot color={color} size={6} breathing={run.status === "running"} />
			<Text style={[styles.statusStrong, styles.runVerdict, { color }]} numberOfLines={1}>{reviewVerdictLabel(run)}</Text>
			<Text style={[styles.detail, styles.mono]}>{shortCommit(run.targetSha)}</Text>
			<ItemActionsMenu accessibilityLabel={`Actions for the ${reviewVerdictLabel(run).toLowerCase()} review`} actions={actions} disabled={disabled} loading={sending} />
		</View>
		<Text style={styles.detail}>{reviewRunMeta({ ...run, harness: reviewerName(run.harness) }).split(" · ").map(capitalize).join(" · ")}</Text>
		{run.autoInjectReview === false ? <Text style={[styles.detail, { color: t.amber }]}>Not automatically sent to the worker</Text> : null}
		{run.body ? <View style={styles.markdown}><ChatMarkdown text={run.body} /></View> : <Text style={styles.detail}>{run.status === "running" ? "Findings appear here when the review finishes." : "No written findings."}</Text>}
		{sent ? <View style={styles.statusLine}><AppSymbol name="checkmark" size={iconSize.sm} color={t.green} /><Text style={[styles.statusStrong, { color: t.green }]}>Sent to worker</Text></View> : null}
	</View>;
}

// The review row's run glyph, in each platform's set.
const PLAY = { ios: "play.fill", android: "play_arrow" } as const;

// Where the reviewer row's button and menu sit, and the room the row keeps for
// them until their width is known.
const OVERLAY_INSET = space.lg - space.xxs;
const OVERLAY_RESERVE = 150;
// The worker row has its button only, no menu.
const WORKER_RESERVE = 110;

function reviewerName(harness: string): string {
	if (!harness) return "Not selected";
	return harness.split("-").map(capitalize).join(" ");
}

function capitalize(value: string): string {
	return value ? value[0].toUpperCase() + value.slice(1) : value;
}

function toneColor(t: Theme, tone: MergeTone, tint = false): string {
	switch (tone) {
		case "green": return tint ? t.tintGreen : t.green;
		case "purple": return tint ? t.tintPurple : t.purple;
		case "amber": return tint ? t.tintAmber : t.amber;
		case "red": return tint ? t.tintRed : t.red;
		default: return tint ? t.bgSubtle : t.textSecondary;
	}
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	scroll: { flex: 1 },
	content: { paddingBottom: space.huge },
	center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	// A worker row's metrics: eyebrow, title, status line, divider underneath.
	row: { minHeight: 76, flexDirection: "row", alignItems: "center", gap: space.md, paddingHorizontal: space.lg, paddingVertical: space.sm, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle, backgroundColor: t.bgBase },
	mergeFooter: { paddingHorizontal: space.lg, paddingTop: space.md, backgroundColor: t.bgBase, borderTopWidth: rowDividerWidth, borderTopColor: t.borderDefault },
	overlaySlot: { position: "absolute", right: OVERLAY_INSET, top: 0, bottom: 0, justifyContent: "center" },
	main: { flex: 1, minWidth: 0, gap: space.hair },
	trailing: { flexDirection: "row", alignItems: "center", gap: space.xxs },
	eyebrow: { flexDirection: "row", alignItems: "center", gap: space.xs, minHeight: 17 },
	eyebrowText: { fontFamily: "DMSans_500Medium", flex: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
	title: { fontFamily: "DMSans_600SemiBold", color: t.textPrimary, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight, fontWeight: "600", letterSpacing: -0.15 },
	statusLine: { flexDirection: "row", alignItems: "center", gap: space.xs, minWidth: 0 },
	statusStrong: { fontFamily: "DMSans_600SemiBold", fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
	statusDetail: { fontFamily: "DMSans_400Regular", flexShrink: 1, marginLeft: -space.xs, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	detail: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	mono: { fontFamily: t.fontMono },
	note: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingHorizontal: space.lg, paddingVertical: space.sm },
	noteError: { color: t.red },
	inlineNote: { flexDirection: "row", alignItems: "flex-start", gap: space.xs, paddingHorizontal: space.lg, paddingVertical: space.sm, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle },
	inlineNoteText: { flex: 1, fontFamily: "DMSans_400Regular", fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	// The close glyph keeps its place at the row's end; its touch target spills past it.
	dismiss: { width: touchTarget, height: touchTarget, marginVertical: -space.md, marginRight: -space.md, alignItems: "center", justifyContent: "center" },
	inlineNoteStrong: { fontFamily: "DMSans_600SemiBold", fontWeight: "600" },
	runRow: { paddingHorizontal: space.lg, paddingVertical: space.md, gap: space.xxs, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle },
	runHead: { flexDirection: "row", alignItems: "center", gap: space.xs, minHeight: touchTarget },
	runVerdict: { flex: 1, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	markdown: { marginTop: space.xs },
});
