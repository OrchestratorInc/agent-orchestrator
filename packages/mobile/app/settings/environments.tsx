import { Stack, useFocusEffect, useRouter } from "expo-router";
import { useCallback, useMemo, useState } from "react";
import { RefreshControl, ScrollView, StyleSheet, View } from "react-native";
import { EnvironmentMachineList, type EnvironmentMachineSummary } from "../../lib/EnvironmentMachineRow";
import { takeEnvironmentNotice } from "../../lib/environmentNotice";
import { environmentStatus } from "../../lib/environmentView";
import { activeHost, loadHosts, type Host as PairedHost } from "../../lib/hosts";
import { refreshControlColors } from "../../lib/refreshControlColors";
import { Spinner } from "../../lib/Spinner";
import { useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { ToastHost } from "../../lib/Toast";
import { toolbarIcon } from "../../lib/toolbarIcon";
import { Button, EmptyState } from "../../lib/ui";
import { useToast } from "../../lib/useToast";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

const LIST_FOOTER = "Machines available for new workers and reconnects.";

/** The paired machines. Each row opens the machine's own page. */
export default function EnvironmentsScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const toast = useToast();
	const { show: showToast } = toast;
	const { hostStates, configForHost, refreshAll } = useApp();
	const [hosts, setHosts] = useState<PairedHost[]>([]);
	const [defaultHostId, setDefaultHostId] = useState<string | null>(null);
	const [loading, setLoading] = useState(true);
	const [refreshing, setRefreshing] = useState(false);
	const [loadError, setLoadError] = useState<string>();
	const headerItems = useMemo(() => (
		<Stack.Toolbar placement="right">
			<Stack.Toolbar.Button icon={toolbarIcon("add")} accessibilityLabel="Add machine" onPress={() => router.push("/pair")} />
		</Stack.Toolbar>
	), [router]);

	const reloadHosts = useCallback(async () => {
		try {
			const [saved, selected] = await Promise.all([loadHosts(), activeHost()]);
			setHosts(saved);
			setDefaultHostId(selected?.id ?? null);
			setLoadError(undefined);
		} catch {
			setLoadError("The saved environments couldn't be loaded.");
		} finally {
			setLoading(false);
		}
	}, []);

	// Read again on every return: a machine may have been renamed, made the
	// default or forgotten on its own page, which leaves word of a forget here.
	useFocusEffect(useCallback(() => {
		void reloadHosts();
		const notice = takeEnvironmentNotice();
		if (notice) showToast(notice);
	}, [reloadHosts, showToast]));

	const refresh = useCallback(async () => {
		setRefreshing(true);
		try {
			await Promise.all([refreshAll(), reloadHosts()]);
		} finally {
			setRefreshing(false);
		}
	}, [refreshAll, reloadHosts]);

	const openMachine = useCallback((id: string) => {
		router.push({ pathname: "/settings/machine/[id]", params: { id } });
	}, [router]);

	const machines: EnvironmentMachineSummary[] = hosts.map((host) => {
		const snapshot = hostStates.find((item) => item.hostId === host.id);
		return {
			host,
			activeConfig: snapshot?.config ?? configForHost(host.id) ?? undefined,
			isDefault: host.id === defaultHostId,
			status: environmentStatus(snapshot),
		};
	});

	return (
		<View style={styles.screen}>
			{headerItems}
			{loading ? (
				<View style={styles.center}><Spinner size="large" accessibilityLabel="Loading machines" /></View>
			) : machines.length > 0 ? (
				<EnvironmentMachineList
					machines={machines}
					footer={LIST_FOOTER}
					error={loadError}
					refreshing={refreshing}
					onRefresh={refresh}
					onOpen={openMachine}
				/>
			) : (
				<ScrollView
					contentInsetAdjustmentBehavior="automatic"
					contentContainerStyle={styles.emptyContent}
					refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void refresh()} {...refreshControlColors(t)} />}
				>
					{loadError ? (
						<View accessibilityRole="alert" style={styles.empty}>
							<EmptyState
								icon="warning"
								iconColor={t.red}
								title="Machines unavailable"
								message={loadError}
								action={<Button title="Try again" icon="refresh" onPress={() => void refresh()} />}
							/>
						</View>
					) : (
						<EmptyState
							icon="devices"
							title="No saved machines"
							message="Connect a machine to view its workers."
							action={<Button title="Add a machine" icon="add" onPress={() => router.push("/pair")} />}
						/>
					)}
				</ScrollView>
			)}
			<ToastHost toast={toast} />
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center" },
	emptyContent: { flexGrow: 1 },
	empty: { flex: 1 },
});
