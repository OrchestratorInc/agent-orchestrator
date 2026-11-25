import { useLocalSearchParams, useNavigation } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useState } from "react";
import { AppState, View } from "react-native";
import { getAccountAccessToken } from "../../lib/account";
import { shouldPoll } from "../../lib/appStatePoll";
import { CloudBrowserTransport } from "../../lib/cloud/cloudBrowserTransport";
import { useCloud } from "../../lib/cloud/CloudProvider";
import { CloudSignIn } from "../../lib/cloud/components";
import { CLOUD_API_BASE_URL } from "../../lib/cloud/config";
import { BrowserLivePane } from "../../lib/preview/BrowserLivePane";
import { Spinner } from "../../lib/Spinner";
import { useTheme } from "../../lib/ThemeProvider";
import { Button, EmptyState } from "../../lib/ui";
import { useGlassBarInset } from "../../lib/useHeaderOverlap";

const PRESENCE_MS = 60_000;

const subscribeAppState = (listener: (state: string) => void) => {
	const subscription = AppState.addEventListener("change", listener);
	return () => subscription.remove();
};

/**
 * A cloud session's agent browser, opened from the session's actions menu. It
 * streams the sandbox's browser through the control plane's relay into the
 * same pane a desktop's browser shows in. The sandbox starts its browser on the
 * first view, and the stream closes in the background so it can idle out.
 */
export default function CloudBrowserRoute() {
	const { sessionId: rawId } = useLocalSearchParams<{ sessionId: string }>();
	const sessionId = String(rawId ?? "");
	const navigation = useNavigation();
	const t = useTheme();
	const glassInset = useGlassBarInset();
	const { client, org, status, error, refresh } = useCloud();
	const orgId = org?.id;
	const [pageTitle, setPageTitle] = useState<string>();

	useLayoutEffect(() => {
		navigation.setOptions({ title: pageTitle ?? "Browser" });
	}, [navigation, pageTitle]);

	useEffect(() => {
		if (!orgId || !sessionId || status !== "ready") return;
		const touch = () => {
			if (shouldPoll(AppState.currentState)) void client.touchPresence(orgId, sessionId).catch(() => {});
		};
		touch();
		const timer = setInterval(touch, PRESENCE_MS);
		return () => clearInterval(timer);
	}, [client, orgId, sessionId, status]);

	const transport = useMemo(() => orgId
		? () => new CloudBrowserTransport({ baseUrl: CLOUD_API_BASE_URL, orgId, sessionId, getToken: getAccountAccessToken, subscribeAppState })
		: undefined, [orgId, sessionId]);
	const onTitle = useCallback((title: string | undefined) => setPageTitle(title), []);

	if (status === "signed-out") return <View style={{ flex: 1, backgroundColor: t.bgBase }}><CloudSignIn /></View>;
	if (!transport) {
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
			<BrowserLivePane sessionID={sessionId} transport={transport} browserName="agent browser" onTitle={onTitle} />
		</View>
	);
}

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
