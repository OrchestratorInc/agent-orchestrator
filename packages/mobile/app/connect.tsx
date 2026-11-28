import { Stack, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useEffect, useMemo, useState } from "react";
import { StyleSheet, View } from "react-native";
import { ManualConnectSheet } from "../lib/ManualConnectSheet";
import { activeHost, findHost, type Host } from "../lib/hosts";
import { loadAccount } from "../lib/account";
import { syncAccountHosts, unignoreAccountHost } from "../lib/accountHosts";
import { releaseSheetResult, takeSheetResult } from "../lib/sheetResult";
import { backOr } from "../lib/backNavigation";
import { Spinner } from "../lib/Spinner";
import { useApp } from "../lib/store";
import { MOBILE_EVENTS } from "../lib/telemetry/events";
import { mobileTelemetry } from "../lib/telemetry/runtime";
import { EmptyState } from "../lib/ui";

// Manual pairing and editing use the same native connection form, on a page
// pushed over Pair a machine (or a machine's settings) with the stack's back.
export default function ConnectRoute() {
	const router = useRouter();
	const navigation = useNavigation();
	const { resultKey, hostId, from } = useLocalSearchParams<{ resultKey?: string; hostId?: string; from?: string }>();
	const { config, currentHostId, reloadConfig } = useApp();
	const [editingHost, setEditingHost] = useState<Host | null | undefined>(hostId === undefined ? null : undefined);
	const [busy, setBusy] = useState(false);
	// The page's bar carries the title, known from the link before the machine
	// loads, and no back button: the way back is the form's own "Connect with
	// QR code", which says where it goes. iOS's form adds its confirm on the
	// right. A connect that has started can't be called back, so while one runs
	// the edge swipe is held, and the form disables its way back.
	const header = useMemo(() => (
		<Stack.Screen options={{ title: hostId === undefined ? "Connect manually" : "Edit connection", headerBackVisible: false }} />
	), [hostId]);
	useEffect(() => {
		if (!busy) return;
		navigation.setOptions({ gestureEnabled: false });
		return () => navigation.setOptions({ gestureEnabled: true });
	}, [busy, navigation]);

	useEffect(() => () => releaseSheetResult(resultKey), [resultKey]);
	useEffect(() => {
		if (hostId === undefined) return;
		let current = true;
		void findHost(hostId).then((host) => { if (current) setEditingHost(host); }).catch(() => { if (current) setEditingHost(null); });
		return () => { current = false; };
	}, [hostId]);

	if (editingHost === undefined) return <>{header}<View style={styles.center}><Spinner size="large" accessibilityLabel="Loading machine" /></View></>;
	if (hostId !== undefined && !editingHost) return <>{header}<EmptyState icon="disconnected" title="Machine not found" message="This machine is no longer paired." /></>;
	const activeEndpointIndex = editingHost && editingHost.id === currentHostId
		? editingHost.endpoints.findIndex((endpoint) => endpoint.host === config?.host && endpoint.port === Number(config.httpPort) && endpoint.secure === !!config.secure)
		: -1;

	return (
		<>
			{header}
			<ManualConnectSheet
				editingHost={editingHost ?? undefined}
				editingEndpointIndex={activeEndpointIndex < 0 ? 0 : activeEndpointIndex}
				onBusyChange={setBusy}
				onScanInstead={() => backOr(router)}
				onConnected={() => {
					// Reported here rather than by the pair screen's callback, which is
					// gone if the person left this page while it connected (below).
					// Opened from Pair a machine (it parks a result), a new pairing,
					// a re-pair or an edit, the same as a scan.
					if (resultKey) {
						const fromOnboarding = from === "onboarding";
						mobileTelemetry()?.capture(MOBILE_EVENTS.paired, { method: "manual", from_onboarding: fromOnboarding });
						if (fromOnboarding) mobileTelemetry()?.capture(MOBILE_EVENTS.onboardingCompleted);
					}
					void loadAccount().then(async (account) => {
						if (account) {
							const pairedId = editingHost?.id ?? (await activeHost())?.id;
							if (pairedId) await unignoreAccountHost(account.id, pairedId);
							await syncAccountHosts(account);
						}
					}).catch(() => {});
					const done = takeSheetResult<void>(resultKey);
					if (editingHost) void reloadConfig().catch(() => {});
					// Left while it connected (Android's system back still works):
					// going back again would pop the screen under it, and the
					// opener's callback was already released. The form lets go of
					// the page itself once this returns.
					if (!navigation.isFocused()) return;
					backOr(router);
					done?.();
				}}
			/>
		</>
	);
}

const styles = StyleSheet.create({
	center: { flex: 1, alignItems: "center", justifyContent: "center" },
});

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";
