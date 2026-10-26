import { Stack, useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useMemo, useState } from "react";
import { ActivityIndicator, Platform, Text, View } from "react-native";
import { ManualConnectSheet } from "../../lib/ManualConnectSheet";
import { activeHost, findHost, type Host } from "../../lib/hosts";
import { loadAccount } from "../../lib/account";
import { syncAccountHosts, unignoreAccountHost } from "../../lib/accountHosts";
import { releaseSheetResult, takeSheetResult } from "../../lib/sheetResult";
import { backOr } from "../../lib/backNavigation";
import { useApp } from "../../lib/store";
import { useTheme } from "../../lib/ThemeProvider";

// Manual pairing and editing use the same native connection form.
export default function ConnectSheetRoute() {
	const router = useRouter();
	const { resultKey, hostId } = useLocalSearchParams<{ resultKey?: string; hostId?: string }>();
	const { config, currentHostId, reloadConfig } = useApp();
	const theme = useTheme();
	const [editingHost, setEditingHost] = useState<Host | null | undefined>(hostId === undefined ? null : undefined);
	// iOS: the sheet's native bar carries the title, known from the link before
	// the machine loads, and Cancel in every state. The form adds its own
	// confirm on the right.
	const header = useMemo(() => Platform.OS === "ios" ? (
		<>
			<Stack.Screen options={{ title: hostId === undefined ? "Connect manually" : "Edit connection" }} />
			<Stack.Toolbar placement="left">
				<Stack.Toolbar.Button onPress={() => backOr(router)}>Cancel</Stack.Toolbar.Button>
			</Stack.Toolbar>
		</>
	) : null, [hostId, router]);

	useEffect(() => () => releaseSheetResult(resultKey), [resultKey]);
	useEffect(() => {
		if (hostId === undefined) return;
		let current = true;
		void findHost(hostId).then((host) => { if (current) setEditingHost(host); }).catch(() => { if (current) setEditingHost(null); });
		return () => { current = false; };
	}, [hostId]);

	if (editingHost === undefined) return <>{header}<ActivityIndicator style={{ flex: 1 }} color={theme.accent} /></>;
	if (hostId !== undefined && !editingHost) return <>{header}<View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}><Text style={{ color: theme.textPrimary }}>This machine is no longer paired.</Text></View></>;
	const activeEndpointIndex = editingHost && editingHost.id === currentHostId
		? editingHost.endpoints.findIndex((endpoint) => endpoint.host === config?.host && endpoint.port === Number(config.httpPort) && endpoint.secure === !!config.secure)
		: -1;

	return (
		<>
			{header}
			<ManualConnectSheet
				editingHost={editingHost ?? undefined}
				editingEndpointIndex={activeEndpointIndex < 0 ? 0 : activeEndpointIndex}
				onConnected={() => {
					void loadAccount().then(async (account) => {
						if (account) {
							const pairedId = editingHost?.id ?? (await activeHost())?.id;
							if (pairedId) await unignoreAccountHost(account.id, pairedId);
							await syncAccountHosts(account);
						}
					}).catch(() => {});
					const done = takeSheetResult<void>(resultKey);
					if (editingHost) void reloadConfig().catch(() => {});
					backOr(router);
					done?.();
				}}
			/>
		</>
	);
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
