import { useLocalSearchParams, useRouter } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, AppState, StyleSheet, View } from "react-native";
import { shouldPoll } from "../../lib/appStatePoll";
import { ChatSessionScreen } from "../../lib/chat/ChatSessionScreen";
import { machineIdentity } from "../../lib/config";
import { resourceKey, type SourceRef } from "../../lib/environment/scopedBoard";
import { lookUpSession } from "../../lib/session/sessionLookup";
import { CloudTerminalSessionScreen } from "../../lib/session/CloudTerminalSessionScreen";
import TerminalSessionScreen from "../../lib/session/TerminalSessionScreen";
import {
	currentSessionLookup,
	sessionLookupDue,
	sessionLookupKey,
	sessionDisplaySurface,
	sessionRouteView,
	resolveSessionRouteSource,
	cloudSessionListState,
	type KeyedSessionLookup,
} from "../../lib/session/sessionRoute";
import { useApp } from "../../lib/store";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import type { Theme } from "../../lib/theme";
import { Button, EmptyState } from "../../lib/ui";

/**
 * The committed session mode is daemon-authoritative, including after an
 * explicit controller handoff, for every session the board lists: the lists are
 * refreshed on every poll. A missing row is looked up rather than guessing
 * Terminal and briefly attaching a nonexistent PTY. What each outcome shows is
 * `sessionRouteView`, and when the route asks is `sessionLookupDue`.
 */
export default function MobileSessionRoute() {
	const { id: rawId, view: requestedView, source: sourceParam, sourceId } = useLocalSearchParams<{ id: string; view?: string; source?: string; sourceId?: string }>();
	const id = String(rawId ?? "");
	const router = useRouter();
	const { scopedBoard, sourceFor, refreshSource, config, connection } = useApp();
	const route = resolveSessionRouteSource({ id, source: sourceParam, sourceId }, [...scopedBoard.sessions, ...scopedBoard.orchestrators]);
	const source: SourceRef | null = route.kind === "found" ? route.source : null;
	const currentSource = source ? sourceFor(source) : undefined;
	const listed = source ? [...scopedBoard.sessions, ...scopedBoard.orchestrators]
		.find((entry) => resourceKey(entry.source, entry.value.id) === resourceKey(source, id))?.value : undefined;
	const isListed = Boolean(listed);
	const status = source ? scopedBoard.sources[source.kind] : undefined;
	const configured = source?.kind === "local"
		? status?.resolved ? !!currentSource : null
		: source?.kind === "cloud" ? !!currentSource : false;
	const routeConnection = source?.kind === "cloud" ? currentSource ? "open" : "closed" : connection;
	const machine = source?.kind === "local" && config ? machineIdentity(config) : "";
	const key = sessionLookupKey(machine, id);
	const [stored, setStored] = useState<KeyedSessionLookup | null>(null);
	const [attempt, setAttempt] = useState(0);
	const lookup = currentSessionLookup(stored, key);
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);

	// Read by the lookup effect, not dependencies of it. An answer landing must not
	// itself cause another request, or a persistent failure would loop; and the
	// config object changes identity when a re-race lands on a different endpoint
	// for the same machine, which restarts the store's poll and so re-runs the
	// effect through `connection` anyway. Declared first, so it has run by the time
	// that effect does.
	const latest = useRef({ config, configured, lookup });
	useEffect(() => {
		latest.current = { config, configured, lookup };
	});
	const machineRef = useRef(machine);

	// A notification can deep-link into a session before the board's next poll has
	// populated it, and the board never lists an orchestrator it dropped. Ask the
	// daemon directly instead of reading the miss as "not found".
	useEffect(() => {
		const machineChanged = machineRef.current !== machine;
		machineRef.current = machine;
		if (isListed || source?.kind !== "local") {
			// Drop the last answer so a later miss starts from a fresh question
			// instead of briefly showing what the lookup said before it was listed.
			setStored(null);
			return;
		}
		const now = latest.current;
		if (
			!now.config ||
			!sessionLookupDue({
				listed: isListed,
				configured: now.configured,
				connection,
				// Read, not a dependency: a foreground restarts the store's poll, which
				// re-runs this effect through `connection` once it opens.
				appActive: shouldPoll(AppState.currentState),
				machineChanged,
				lookup: now.lookup,
			})
		) {
			return;
		}
		// A failure from before the reconnect would otherwise stay on screen, Retry
		// and all, while this request is in flight.
		setStored(null);
		let cancelled = false;
		void lookUpSession(now.config, id).then((answer) => {
			if (!cancelled) setStored({ key, lookup: answer });
		});
		return () => {
			cancelled = true;
		};
		// `attempt` is read only through this list: bumping it is how Retry asks
		// again. `connection` turning "open" is how a lookup that failed, or was
		// rejected, gets asked again once the board has reconnected.
	}, [attempt, connection, id, isListed, key, machine, source?.kind]);

	const retry = useCallback(() => {
		// Clearing first swaps the button for the spinner, so a second tap cannot
		// land while the retry is in flight.
		setStored(null);
		setAttempt((n) => n + 1);
	}, []);

	if (route.kind === "ambiguous" || route.kind === "invalid" || route.kind === "missing") return (
		<View style={styles.center}>
			<EmptyState icon="search" title={route.kind === "ambiguous" ? "Choose a worker from the board" : "Session source unavailable"}
				message="This link cannot safely identify its Cloud or desktop session."
				action={<Button title="Open Workers" onPress={() => router.navigate("/")} />} />
		</View>
	);
	if (source && !currentSource && status?.resolved) return (
		<View style={styles.center}>
			<EmptyState icon="wifi-off" title={source.kind === "cloud" ? "Cloud session unavailable" : "Desktop session unavailable"}
				message="This session belongs to a source that is no longer connected."
				action={<Button title={source.kind === "cloud" ? "Sign in to Cloud" : "Pair desktop"}
					onPress={() => router.push(source.kind === "cloud" ? "/sheets/cloud-signin" : "/pair")} />} />
		</View>
	);
	if (source?.kind === "cloud" && currentSource) {
		const cloudRef = source;
		const cloudList = cloudSessionListState({ listed: isListed, loading: !!status?.loading, error: status?.error ?? null });
		if (cloudList === "missing" || cloudList === "failed") return (
			<View style={styles.center}>
				<EmptyState icon={cloudList === "failed" ? "wifi-off" : "search"}
					title={cloudList === "failed" ? "Couldn't load Cloud session" : "Session not found"}
					message={cloudList === "failed" ? status?.error ?? undefined : "It may have been removed from Cloud."}
					action={<Button title="Retry" onPress={() => void refreshSource(cloudRef)} />} />
			</View>
		);
	}
	const view = sessionRouteView({ listed, configured, connection: routeConnection, loading: !!status?.loading, lookup });

	switch (view.kind) {
		case "screen": {
			const surface = sessionDisplaySurface({ environment: source?.kind ?? null, sessionMode: view.session.mode, requestedView });
			if (surface === "cloud-terminal" && source) return <CloudTerminalSessionScreen session={view.session} source={source} />;
			if (surface === "local-terminal") return <TerminalSessionScreen session={view.session} />;
			if (source) return <ChatSessionScreen session={view.session} source={source} />;
			return null;
		}
		case "loading":
			return (
				<View style={styles.center}>
					<ActivityIndicator color={t.accent} />
				</View>
			);
		case "unpaired":
			// The board's own unpaired state, word for word.
			return (
				<View style={styles.center}>
					<EmptyState
						icon="monitor-smartphone"
						title="No desktop paired"
						action={<Button title="Scan pairing code" icon="maximize" onPress={() => router.push("/pair")} />}
					/>
				</View>
			);
		case "offline":
			return (
				<View style={styles.center}>
					<EmptyState
						icon="unplug"
						title="Not connected to your desktop"
						action={<Button title="Open board" icon="activity" variant="ghost" onPress={() => router.navigate("/")} />}
					/>
				</View>
			);
		case "ended":
			return (
				<View style={styles.center}>
					<EmptyState
						icon="archive"
						title="This session has ended"
						action={<Button title="Open board" icon="activity" variant="ghost" onPress={() => router.navigate("/")} />}
					/>
				</View>
			);
		case "missing":
			return (
				<View style={styles.center}>
					<EmptyState
						icon="search"
						title="Session not found"
						message="It may have been deleted on your desktop."
						action={<Button title="Open board" icon="activity" variant="ghost" onPress={() => router.navigate("/")} />}
					/>
				</View>
			);
		case "failed":
			return (
				<View style={styles.center}>
					<EmptyState
						icon="alert-circle"
						title="Couldn't load this session"
						action={<Button title="Retry" icon="refresh-cw" variant="ghost" onPress={retry} />}
					/>
				</View>
			);
	}
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	});

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
