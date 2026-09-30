import { Feather } from "@expo/vector-icons";
import { useFocusEffect, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { ActivityIndicator, Alert, Pressable, RefreshControl, ScrollView, StyleSheet, Text, View } from "react-native";
import { cancelSessionReview, getSessionPR, getSessionReviews, killSessionReviewer, mergeSessionPR, restoreSessionReviewer, sendMessage, triggerSessionReview, type ReviewRun, type SessionPRSummary, type SessionReviews } from "../../lib/api";
import { ChatMarkdown } from "../../lib/chat/ChatMarkdown";
import { haptics } from "../../lib/haptics";
import { ItemActionsMenu } from "../../lib/item-actions-menu";
import type { ItemAction } from "../../lib/item-actions-menu.types";
import { openGitHub } from "../../lib/openGitHub";
import { mergeReadiness } from "../../lib/prMerge";
import { formatReviewSummaryMessage, reviewRunsForPullRequest, reviewRunUrl } from "../../lib/reviewFeedback";
import { latestAutoReviewFailure, pullRequestSummaryForURL, reviewBatchAction, reviewerControls, reviewerDestination, reviewForPullRequest, reviewPrimaryActionLabel, reviewRunMeta, reviewRunSendable, reviewStatusLabel, reviewStatusVisual, reviewVerdictLabel, shortCommit } from "../../lib/reviewView";
import { useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { Button, Card, EmptyState } from "../../lib/ui";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

export default function ReviewDetailScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const navigation = useNavigation();
	const router = useRouter();
	const { sessionId, prUrl, prNumber } = useLocalSearchParams<{ sessionId: string; prUrl?: string; prNumber?: string }>();
	const { config, sessions } = useApp();
	const autoReviewEnabled = sessions.find((session) => session.id === sessionId)?.autoReviewEnabled === true;
	const [data, setData] = useState<SessionReviews>();
	const [prs, setPRs] = useState<SessionPRSummary[]>([]);
	const [sentRuns, setSentRuns] = useState<ReadonlySet<string>>(new Set());
	const [error, setError] = useState("");
	const [reviewNotice, setReviewNotice] = useState("");
	const [dismissedAutoFailureId, setDismissedAutoFailureId] = useState<string>();
	const [refreshing, setRefreshing] = useState(false);
	const [mutation, setMutation] = useState<"review" | "restore" | string>();
	const latestLoad = useRef(0);

	const load = useCallback(async (quiet = false) => {
		if (!config || !sessionId) return;
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
			if (!quiet && request === latestLoad.current) setError(value instanceof Error ? value.message : "Could not load this review.");
		}
	}, [config, sessionId]);

	useFocusEffect(useCallback(() => { void load(); }, [load]));
	const review = reviewForPullRequest(data?.reviews ?? [], prUrl, Number(prNumber) || undefined);
	const autoReviewFailure = latestAutoReviewFailure(data?.reviews ?? [], autoReviewEnabled);
	const autoReviewFailureId = autoReviewFailure?.id;
	useEffect(() => {
		if (!autoReviewFailureId || autoReviewFailureId === dismissedAutoFailureId) return;
		const timer = setTimeout(() => setDismissedAutoFailureId(autoReviewFailureId), 10_000);
		return () => clearTimeout(timer);
	}, [autoReviewFailureId, dismissedAutoFailureId]);
	useLayoutEffect(() => navigation.setOptions({ title: review?.title || "Review" }), [navigation, review?.title]);
	useLayoutEffect(() => navigation.setOptions({
		headerRight: review?.prUrl ? () => <View style={styles.headerActions}>
			<Pressable accessibilityRole="link" accessibilityLabel={`Open pull request ${review.prNumber} in GitHub`} hitSlop={8} style={styles.headerAction} onPress={() => { haptics.tap(); void openGitHub(review.prUrl); }}><Feather name="external-link" size={19} color={t.textSecondary} /></Pressable>
			<Pressable accessibilityRole="button" accessibilityLabel="More review actions" hitSlop={8} style={styles.headerAction} onPress={() => { haptics.tap(); router.push({ pathname: "/sheets/review-actions", params: { sessionId, prUrl: review.prUrl, reviewer: data?.reviewerHarness ?? "" } }); }}><Feather name="more-horizontal" size={21} color={t.textSecondary} /></Pressable>
		</View> : undefined,
	}), [data?.reviewerHarness, navigation, review?.prNumber, review?.prUrl, router, sessionId, styles.headerAction, styles.headerActions, t.textSecondary]);
	useEffect(() => {
		if (review?.status !== "running" && !autoReviewEnabled) return;
		const timer = setInterval(() => void load(true), 2_000);
		return () => clearInterval(timer);
	}, [autoReviewEnabled, load, review?.status]);

	const refresh = async () => {
		haptics.tap();
		setRefreshing(true);
		await load();
		setRefreshing(false);
	};

	if (!data && !error) return <View style={styles.center}><ActivityIndicator color={t.blue} /></View>;
	if (!data || !review) return <EmptyState icon={error ? "alert-triangle" : "git-pull-request"} title={error ? "Could not load review" : "No review found"} message={error || "AO has no review state for this pull request yet."} action={<Button title="Try again" icon="refresh-cw" variant="ghost" onPress={() => void load()} />} />;
	const primaryAction = reviewBatchAction(review, data.reviews);
	const runs = reviewRunsForPullRequest([...(data.runs ?? []), ...(review.latestRun ? [review.latestRun] : []), ...(review.previousRun ? [review.previousRun] : [])], review.prUrl);
	const multiplePullRequests = data.reviews.length > 1;
	const openReviewer = () => {
		const destination = reviewerDestination(data, review, sessionId);
		if (!destination) return;
		haptics.tap();
		router.push(destination);
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
			setError(value instanceof Error ? value.message : "Could not restore the reviewer.");
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
				catch (value) { setError(value instanceof Error ? value.message : "Could not stop the reviewer session."); }
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
			setError(value instanceof Error ? value.message : "The review action failed.");
		} finally {
			setMutation(undefined);
		}
	};
	const sendRun = async (run: ReviewRun) => {
		if (!config || mutation) return;
		setMutation(`send:${run.id}`);
		setError("");
		try {
			await sendMessage(config, sessionId, formatReviewSummaryMessage(run));
			setSentRuns((current) => new Set(current).add(run.id));
			haptics.success();
		} catch (value) {
			setError(value instanceof Error ? value.message : "Could not send this review to the worker.");
		} finally {
			setMutation(undefined);
		}
	};

	const controls = reviewerControls(data, review, sessionId);
	const pr = pullRequestSummaryForURL(prs, review.prUrl);
	const merge = pr ? mergeReadiness(pr) : undefined;
	const confirmMerge = () => {
		if (!config || !pr || !merge?.canMerge || mutation) return;
		Alert.alert(`Merge PR #${pr.number}?`, `This will squash-merge PR #${pr.number} in the remote repository.`, [
			{ text: "Cancel", style: "cancel" },
			{ text: "Merge", onPress: () => void (async () => {
				setMutation("merge"); setError("");
				try { await mergeSessionPR(config, pr); haptics.success(); await load(); }
				catch (value) { setError(value instanceof Error ? value.message : `Could not merge PR #${pr.number}.`); }
				finally { setMutation(undefined); }
			})() },
		]);
	};

	return (
		<ScrollView style={styles.screen} contentContainerStyle={styles.content} refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} tintColor={t.blue} />}>
			<View style={styles.heading}>
				<View style={[styles.statusIcon, { backgroundColor: statusColor(t, reviewStatusVisual(review.status).tone, true) }]}>
					<Feather name={reviewStatusVisual(review.status).icon} size={20} color={statusColor(t, reviewStatusVisual(review.status).tone)} />
				</View>
				<View style={styles.headingCopy}>
					<Text style={styles.title}>{review.title}</Text>
					<Text style={styles.subtitle}>PR #{review.prNumber} · {reviewStatusLabel(review.status)}</Text>
				</View>
			</View>

			<Card style={styles.metaCard}>
				<Meta label="Reviewer" value={data.reviewerHarness || data.reviewerSurface?.harness || "Not selected"} />
				<Meta label="Commit" value={shortCommit(review.targetSha)} mono />
				{data.reviewerActivityState ? <Meta label="Activity" value={data.reviewerActivityState.replaceAll("_", " ")} /> : null}
			</Card>
			{autoReviewFailure && autoReviewFailure.id !== dismissedAutoFailureId ? <View accessibilityRole="alert" style={styles.autoReviewFailure}>
				<Feather name="alert-circle" size={16} color={t.red} />
				<View style={styles.failureCopy}><Text style={styles.failureTitle}>Automatic review failed</Text><Text style={styles.failureBody}>{autoReviewFailure.body.trim()}</Text></View>
				<Pressable accessibilityRole="button" accessibilityLabel="Dismiss automatic review failure" hitSlop={8} onPress={() => setDismissedAutoFailureId(autoReviewFailure.id)}><Feather name="x" size={17} color={t.red} /></Pressable>
			</View> : null}
			{reviewNotice ? <View style={styles.notice}><Feather name="check" size={15} color={t.green} /><Text style={styles.noticeText}>{reviewNotice}</Text></View> : null}
			{merge && pr ? <Card style={styles.mergeCard}>
				<View style={styles.mergeRow}>
					<View style={styles.mergeCopy}>
						<Text style={[styles.mergeLabel, { color: merge.canMerge ? t.green : pr.state === "merged" ? t.accent : t.textSecondary }]}>{merge.label}</Text>
						{merge.reason ? <Text style={styles.mergeReason}>{merge.reason}</Text> : null}
					</View>
					{merge.canMerge ? <Button title={mutation === "merge" ? "Merging…" : "Merge"} icon="git-merge" variant="success" loading={mutation === "merge"} disabled={Boolean(mutation)} onPress={confirmMerge} /> : null}
				</View>
			</Card> : null}
			{controls.open
				? <Button title={controls.open === "chat" ? "Open reviewer chat" : "Open reviewer terminal"} icon={controls.open === "chat" ? "message-circle" : "terminal"} variant="ghost" disabled={Boolean(mutation)} onPress={openReviewer} />
				: controls.restore ? <Button title="Restore reviewer" icon="refresh-cw" variant="ghost" loading={mutation === "restore"} disabled={Boolean(mutation)} onPress={() => void restoreReviewer()} /> : null}
			{controls.stop ? <><Button title="Stop reviewer session" icon="power" variant="ghost" loading={mutation === "kill"} disabled={Boolean(mutation) || autoReviewEnabled} onPress={confirmKillReviewer} />{autoReviewEnabled ? <Text style={styles.scopeNote}>Turn off automatic review before stopping its reviewer session.</Text> : null}</> : null}
			{data.reviewerSurface?.controllerError ? <Text accessibilityRole="alert" style={styles.error}>{data.reviewerSurface.controllerError}</Text> : null}
			{primaryAction !== "none" ? <Button title={reviewPrimaryActionLabel(primaryAction, multiplePullRequests)} icon={primaryAction === "cancel" ? "x" : "play"} variant={primaryAction === "cancel" ? "danger" : "primary"} loading={mutation === "review"} disabled={Boolean(mutation) || primaryAction !== "cancel" && autoReviewEnabled} onPress={() => void runPrimaryAction()} /> : null}
			{primaryAction !== "cancel" && autoReviewEnabled ? <Text style={styles.scopeNote}>Automatic review is watching for new commits. Turn it off in Review actions to run reviews manually.</Text> : null}
			{primaryAction !== "none" && multiplePullRequests ? <Text style={styles.scopeNote}>This action applies to every eligible pull request in this session.</Text> : null}
			{error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}

			<Text style={styles.sectionLabel}>AO REVIEW HISTORY</Text>
			{runs.length ? runs.map((run, index) => <RunCard key={run.id} run={run} previous={index > 0} sent={sentRuns.has(run.id)} sending={mutation === `send:${run.id}`} disabled={Boolean(mutation)} onSend={() => void sendRun(run)} />) : <Card><Text style={styles.emptyTitle}>No result for this pull request</Text><Text style={styles.body}>This pull request still needs a review.</Text></Card>}
		</ScrollView>
	);
}

function Meta({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.metaRow}><Text style={styles.metaLabel}>{label}</Text><Text style={[styles.metaValue, mono && styles.mono]}>{value}</Text></View>;
}

function RunCard({ run, previous = false, sent, sending, disabled, onSend }: { run: ReviewRun; previous?: boolean; sent: boolean; sending: boolean; disabled: boolean; onSend: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const requested = run.verdict === "changes_requested";
	const url = reviewRunUrl(run);
	// Desktop keeps these in a per-review "⋯" menu.
	const actions: ItemAction[] = [
		...(url ? [{ id: "open", label: "Open on GitHub", systemImage: "arrow.up.right.square", onPress: () => void openGitHub(url) }] : []),
		...(reviewRunSendable(run) && !sent ? [{ id: "send", label: "Send to worker", systemImage: "paperplane", onPress: onSend }] : []),
	];
	return <Card style={previous ? styles.previousCard : undefined}>
		<View style={styles.runHeader}><Feather name={requested ? "alert-circle" : run.verdict === "approved" ? "check-circle" : "clock"} size={17} color={requested ? t.amber : run.verdict === "approved" ? t.green : t.textSecondary} /><Text style={styles.runTitle}>{reviewVerdictLabel(run)}</Text><Text style={styles.sha}>{shortCommit(run.targetSha)}</Text><ItemActionsMenu accessibilityLabel={`Actions for the ${reviewVerdictLabel(run).toLowerCase()} review`} actions={actions} disabled={disabled} loading={sending} /></View>
		<Text style={styles.runBy}>{reviewRunMeta(run)}</Text>
		{run.autoInjectReview === false ? <View style={styles.notInjected}><Feather name="info" size={13} color={t.amber} /><Text style={styles.notInjectedText}>Not automatically sent to the worker</Text></View> : null}
		{run.body ? <View style={styles.markdown}><ChatMarkdown text={run.body} /></View> : <Text style={styles.bodyMuted}>{run.status === "running" ? "Findings appear here when the review finishes." : "No written findings."}</Text>}
		{sent ? <View style={styles.sentNote}><Feather name="check" size={13} color={t.green} /><Text style={styles.sentNoteText}>Sent to worker</Text></View> : null}
	</Card>;
}

function statusColor(t: Theme, tone: ReturnType<typeof reviewStatusVisual>["tone"], tint = false): string {
	if (tone === "amber") return tint ? t.tintAmber : t.amber;
	if (tone === "green") return tint ? t.tintGreen : t.green;
	if (tone === "blue") return tint ? t.tintBlue : t.blue;
	return tint ? t.bgSubtle : t.textTertiary;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	content: { padding: 16, paddingBottom: 40, gap: 12 },
	mergeCard: { paddingVertical: 12 },
	mergeRow: { flexDirection: "row", alignItems: "center", gap: 12 },
	mergeCopy: { flex: 1, gap: 3 },
	mergeLabel: { fontSize: 15, fontWeight: "700" },
	mergeReason: { color: t.textTertiary, fontSize: 12, lineHeight: 17 },
	sentNote: { flexDirection: "row", alignItems: "center", gap: 5, marginTop: 8 },
	sentNoteText: { color: t.green, fontSize: 12, fontWeight: "600" },
	center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	heading: { flexDirection: "row", alignItems: "center", gap: 12, paddingVertical: 4 },
	statusIcon: { width: 42, height: 42, borderRadius: 13, alignItems: "center", justifyContent: "center" },
	headingCopy: { flex: 1, gap: 3 },
	title: { color: t.textPrimary, fontSize: 20, lineHeight: 25, fontWeight: "700" },
	subtitle: { color: t.textSecondary, fontSize: 13 },
	metaCard: { gap: 10 },
	notice: { flexDirection: "row", alignItems: "center", gap: 8, backgroundColor: t.tintGreen, borderRadius: 10, paddingHorizontal: 12, paddingVertical: 10 },
	noticeText: { color: t.green, flex: 1, fontSize: 13 },
	autoReviewFailure: { flexDirection: "row", alignItems: "flex-start", gap: 9, borderWidth: StyleSheet.hairlineWidth, borderColor: t.red, backgroundColor: t.tintRed, borderRadius: 10, padding: 11 },
	failureCopy: { flex: 1, gap: 3 },
	failureTitle: { color: t.red, fontSize: 13, fontWeight: "700" },
	failureBody: { color: t.red, fontSize: 13, lineHeight: 18 },
	metaRow: { flexDirection: "row", alignItems: "center", gap: 12 },
	metaLabel: { width: 72, color: t.textTertiary, fontSize: 12, textTransform: "uppercase", letterSpacing: 0.5 },
	metaValue: { flex: 1, color: t.textPrimary, fontSize: 14, textTransform: "capitalize" },
	mono: { fontFamily: t.fontMono, textTransform: "none" },
	sectionLabel: { color: t.textTertiary, fontSize: 11, fontWeight: "700", letterSpacing: 0.8, marginTop: 8, marginLeft: 4 },
	runHeader: { flexDirection: "row", alignItems: "center", gap: 8 },
	runTitle: { flex: 1, color: t.textPrimary, fontSize: 16, fontWeight: "700" },
	sha: { color: t.textTertiary, fontSize: 11, fontFamily: t.fontMono },
	runBy: { color: t.textTertiary, fontSize: 12, marginTop: 6, textTransform: "capitalize" },
	body: { color: t.textSecondary, fontSize: 14, lineHeight: 21, marginTop: 12 },
	markdown: { marginTop: 12 },
	bodyMuted: { color: t.textTertiary, fontSize: 14, marginTop: 12, fontStyle: "italic" },
	emptyTitle: { color: t.textPrimary, fontSize: 15, fontWeight: "700" },
	error: { color: t.red, fontSize: 13, lineHeight: 18 },
	scopeNote: { color: t.textTertiary, fontSize: 12, lineHeight: 17, textAlign: "center", paddingHorizontal: 12 },
	previousCard: { opacity: 0.78 },
	notInjected: { flexDirection: "row", alignItems: "center", gap: 6, marginTop: 8 },
	notInjectedText: { color: t.amber, fontSize: 12 },
	headerActions: { flexDirection: "row", alignItems: "center", gap: 2 },
	headerAction: { width: 36, height: 36, alignItems: "center", justifyContent: "center" },
});
