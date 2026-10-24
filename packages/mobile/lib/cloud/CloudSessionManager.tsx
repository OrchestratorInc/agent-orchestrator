import type { CloudClient, Session } from "@aoagents/cloud-client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Alert, FlatList, Platform, StyleSheet, Text, useWindowDimensions, View } from "react-native";
import { agentLabel } from "../../../product-ui/src/agents";
import { AgentLogo } from "../AgentLogo";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { statusVisual, type Theme } from "../theme";
import { iconSize, space, touchTarget, type } from "../tokens";
import { Button, HeaderIconButton } from "../ui";
import { cloudRequestErrorCopy } from "./errorCopy";
import { cloudLifecycleStage, stageLabel } from "./lifecycle";
import { toDashboardSession } from "./mapping";

export type CloudSessionManagerProps = {
	client: CloudClient;
	orgId: string;
	scopeKey: string;
	isCurrent(): boolean;
	projectNames: Readonly<Record<string, string>>;
	onBack(): void;
	onChanged?(): void;
};

/** Embedded in the task sheet; the parent owns and retains the task draft. */
export function CloudSessionManager({ client, orgId, scopeKey, isCurrent, projectNames, onBack, onChanged }: CloudSessionManagerProps) {
	const theme = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { height } = useWindowDimensions();
	const [sessions, setSessions] = useState<Session[]>([]);
	const [loading, setLoading] = useState(true);
	const [loadError, setLoadError] = useState<string | null>(null);
	const [archiveErrors, setArchiveErrors] = useState<Record<string, string>>({});
	const [archiving, setArchiving] = useState<ReadonlySet<string>>(new Set());
	const [notice, setNotice] = useState<string | null>(null);
	const owner = useMemo(() => ({
		client, orgId, isCurrent, active: true, sessions: [] as Session[],
		load: null as AbortController | null,
		archives: new Map<string, AbortController>(),
	}), [client, orgId, scopeKey, isCurrent]);
	const ownerRef = useRef(owner);
	ownerRef.current = owner;
	const changedRef = useRef(onChanged);
	changedRef.current = onChanged;
	const ownsView = useCallback(() => owner.active && ownerRef.current === owner && owner.isCurrent(), [owner]);
	const stop = useCallback(() => {
		owner.active = false;
		owner.load?.abort();
		for (const request of owner.archives.values()) request.abort();
	}, [owner]);

	const refresh = useCallback(async () => {
		if (!ownsView()) return;
		owner.load?.abort();
		const request = new AbortController();
		owner.load = request;
		const isLive = () => ownsView() && owner.load === request && !request.signal.aborted;
		setLoading(true);
		setLoadError(null);
		try {
			const all = new Map<string, Session>();
			const cursors = new Set<string>();
			let cursor: string | undefined;
			while (isLive()) {
				const result = await owner.client.listSessions(owner.orgId, { cursor, signal: request.signal });
				if (!isLive()) return;
				for (const session of result.items) all.set(session.id, session);
				if (!result.page.hasMore) break;
				const next = result.page.nextCursor;
				if (!next || cursors.has(next)) throw new Error("Cloud could not load all sessions. Refresh and try again.");
				cursors.add(next);
				cursor = next;
			}
			if (!isLive()) return;
			owner.sessions = [...all.values()];
			setSessions(owner.sessions);
		} catch (cause) {
			if (isLive()) setLoadError(cloudRequestErrorCopy(cause, { includeReference: false }));
		} finally {
			if (isLive()) { owner.load = null; setLoading(false); }
		}
	}, [owner, ownsView]);

	useEffect(() => {
		owner.active = true;
		setSessions([]);
		setArchiveErrors({});
		setArchiving(new Set());
		setNotice(null);
		void refresh();
		return stop;
	}, [owner, refresh, stop]);

	const archive = (session: Session) => {
		const canArchive = () => ownsView() && !owner.archives.has(session.id)
			&& owner.sessions.some(item => item.id === session.id && !item.isTerminated && item.desiredState !== "deleted");
		if (!canArchive()) return;
		Alert.alert(`Archive “${sessionName(session)}”?`, "This stops the agent and retains its conversation. Cloud cleanup may take a moment.", [
			{ text: "Cancel", style: "cancel" },
			{ text: "Archive", style: "destructive", onPress: () => {
				if (!canArchive()) return;
				const request = new AbortController();
				owner.archives.set(session.id, request);
				setArchiving(new Set(owner.archives.keys()));
				setArchiveErrors(previous => { const next = { ...previous }; delete next[session.id]; return next; });
				void (async () => {
					try {
						await owner.client.deleteSession(owner.orgId, session.id, { signal: request.signal });
						if (!ownsView() || request.signal.aborted) return;
						setNotice("Cloud cleanup may take a moment. Refresh before trying your task again.");
						changedRef.current?.();
						await refresh();
					} catch (cause) {
						if (ownsView() && !request.signal.aborted) setArchiveErrors(previous => ({ ...previous, [session.id]: cloudRequestErrorCopy(cause, { includeReference: false }) }));
					} finally {
						owner.archives.delete(session.id);
						if (ownsView()) setArchiving(new Set(owner.archives.keys()));
					}
				})();
			} },
		]);
	};

	return (
		<View style={[styles.container, Platform.OS === "ios" && styles.fillSheet]}>
			<View style={styles.header}>
				<HeaderIconButton icon="back" label="Back to task draft" onPress={() => { stop(); onBack(); }} />
				<Text accessibilityRole="header" style={styles.heading}>Cloud sessions</Text>
				<Button title="Refresh" variant="text" loading={loading} onPress={() => { void refresh(); }} />
			</View>
			<Text style={styles.explanation}>Sessions across all projects. Paused sessions still count toward the Cloud session limit.</Text>
			{notice ? <Text selectable accessibilityLiveRegion="polite" style={styles.explanation}>{notice}</Text> : null}
			{loadError ? <View style={styles.feedback}>
				<Text selectable accessibilityRole="alert" style={styles.error}>{loadError}</Text>
				<Button title="Retry" variant="text" onPress={() => { void refresh(); }} />
			</View> : null}
			<FlatList
				data={sessions}
				keyExtractor={session => session.id}
				style={Platform.OS === "ios" ? styles.fillSheet : { height: Math.min(height * 0.55, touchTarget * 10) }}
				nestedScrollEnabled
				contentInsetAdjustmentBehavior="automatic"
				contentContainerStyle={styles.listContent}
				ItemSeparatorComponent={() => <View style={styles.separator} />}
				ListEmptyComponent={loading ? <ActivityIndicator accessibilityLabel="Loading Cloud sessions" color={theme.textSecondary} />
					: !loadError ? <Text style={styles.explanation}>No Cloud sessions.</Text> : null}
				renderItem={({ item }) => {
					const state = sessionState(item, theme);
					return <View style={styles.session}>
						<View style={styles.row}>
							<AgentLogo harness={item.harness} size={iconSize.xl} />
							<View style={styles.identity}>
								<Text selectable style={styles.title}>{sessionName(item)}</Text>
								<Text selectable style={styles.metadata}>{projectNames[item.projectId]?.trim() || "Unknown project"} · {item.kind === "orchestrator" ? "Orchestrator" : "Worker"}</Text>
								<Text selectable style={[styles.metadata, { color: state.color }]}>{state.label}</Text>
							</View>
							{!item.isTerminated && item.desiredState !== "deleted" ? <Button title="Archive" accessibilityLabel={`Archive ${sessionName(item)}`} variant="text" loading={archiving.has(item.id)} onPress={() => archive(item)} /> : null}
						</View>
						{archiveErrors[item.id] ? <Text selectable accessibilityRole="alert" style={styles.error}>{archiveErrors[item.id]}</Text> : null}
					</View>;
				}}
			/>
		</View>
	);
}

function sessionName(session: Session): string {
	return session.displayName.trim() || agentLabel(session.harness);
}

function sessionState(session: Session, theme: Theme): { label: string; color: string } {
	if (session.desiredState === "deleted" && session.observedState !== "deleted") return { label: "Cleanup pending", color: theme.textSecondary };
	if (session.isTerminated) return { label: "Archived", color: theme.textTertiary };
	const stage = cloudLifecycleStage(toDashboardSession(session));
	if (stage && stage !== "connected") return { label: stageLabel(stage), color: stage === "failed" ? theme.red : theme.textSecondary };
	return statusVisual(theme, session.status);
}

const makeStyles = (theme: Theme) => StyleSheet.create({
	container: { gap: space.md },
	fillSheet: { flex: 1, minHeight: 0 },
	header: { flexDirection: "row", alignItems: "center", gap: space.sm },
	heading: { ...type.headline, flex: 1, color: theme.textPrimary },
	explanation: { ...type.footnote, color: theme.textSecondary },
	feedback: { gap: space.xs, alignItems: "flex-start" },
	error: { ...type.footnote, color: theme.red },
	listContent: { paddingBottom: space.lg },
	separator: { height: StyleSheet.hairlineWidth, backgroundColor: theme.borderSubtle },
	session: { paddingVertical: space.md, gap: space.sm },
	row: { flexDirection: "row", alignItems: "center", gap: space.md },
	identity: { flex: 1, gap: space.xxs },
	title: { ...type.subheadline, color: theme.textPrimary },
	metadata: { ...type.footnote, color: theme.textSecondary },
});
