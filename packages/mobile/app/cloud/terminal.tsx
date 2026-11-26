import { useLocalSearchParams, useNavigation } from "expo-router";
import { useEffect, useLayoutEffect } from "react";
import { AppState, View } from "react-native";
import { shouldPoll } from "../../lib/appStatePoll";
import { useCloud } from "../../lib/cloud/CloudProvider";
import { CloudTerminalView } from "../../lib/cloud/CloudTerminalView";
import { CloudSignIn } from "../../lib/cloud/components";
import type { CloudTerminalKind } from "../../lib/cloud/types";
import { Spinner } from "../../lib/Spinner";
import { useTheme } from "../../lib/ThemeProvider";
import { Button, EmptyState } from "../../lib/ui";
import { useGlassBarInset } from "../../lib/useHeaderOverlap";

const PRESENCE_MS = 60_000;

/**
 * A cloud session's sandbox shell (or its agent's terminal), opened from the
 * session's actions menu as a local session opens its worktree shell
 * (app/shell/[handleId]), and drawn as that page is. It keeps the sandbox awake
 * while it is open: the conversation underneath stops its own heartbeat once
 * this screen covers it.
 */
export default function CloudTerminalRoute() {
	const { sessionId: rawId, kind } = useLocalSearchParams<{ sessionId: string; kind?: string }>();
	const sessionId = String(rawId ?? "");
	const terminalKind: CloudTerminalKind = kind === "agent" ? "agent" : "workspace";
	const navigation = useNavigation();
	const t = useTheme();
	const glassInset = useGlassBarInset();
	const { client, org, status, error, sessions, refresh } = useCloud();
	const session = sessions.find((item) => item.id === sessionId);
	const orgId = org?.id;

	useLayoutEffect(() => {
		navigation.setOptions({ title: terminalKind === "agent" ? "Agent terminal" : "Shell" });
	}, [navigation, terminalKind]);

	useEffect(() => {
		if (!orgId || !sessionId || status !== "ready") return;
		const touch = () => {
			if (shouldPoll(AppState.currentState)) void client.touchPresence(orgId, sessionId).catch(() => {});
		};
		touch();
		const timer = setInterval(touch, PRESENCE_MS);
		return () => clearInterval(timer);
	}, [client, orgId, sessionId, status]);

	if (status === "signed-out") return <View style={{ flex: 1, backgroundColor: t.bgBase }}><CloudSignIn /></View>;
	if (!orgId) {
		return (
			<View style={{ flex: 1, justifyContent: "center", backgroundColor: t.bgBase }}>
				{status === "error" ? (
					<EmptyState
						icon="offline"
						title="Can't reach Orchestrator Cloud"
						message={error ?? undefined}
						action={<Button title="Try again" icon="refresh" onPress={() => void refresh().catch(() => {})} />}
					/>
				) : (
					<Spinner accessibilityLabel="Loading" />
				)}
			</View>
		);
	}
	return (
		<View style={{ flex: 1, paddingTop: glassInset, backgroundColor: t.bgBase }}>
			<CloudTerminalView client={client} orgId={orgId} sessionId={sessionId} kind={terminalKind} workerEpoch={session?.workerEpoch} headerMenu />
		</View>
	);
}

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
