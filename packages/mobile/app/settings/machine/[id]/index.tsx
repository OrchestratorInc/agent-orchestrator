import { Stack, useFocusEffect, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, StyleSheet, View } from "react-native";
import { forgetEnvironmentMachine } from "../../../../lib/environmentActions";
import { postEnvironmentNotice } from "../../../../lib/environmentNotice";
import { environmentStatus, FORGET_MACHINE_MESSAGE } from "../../../../lib/environmentView";
import { haptics } from "../../../../lib/haptics";
import { activeHost, loadHosts, type Host } from "../../../../lib/hosts";
import { MachineDetail } from "../../../../lib/MachineDetail";
import { Spinner } from "../../../../lib/Spinner";
import { useApp } from "../../../../lib/store";
import { useTheme } from "../../../../lib/ThemeProvider";
import { EmptyState } from "../../../../lib/ui";

export { RouteErrorBoundary as ErrorBoundary } from "../../../../lib/RouteErrorBoundary";

/** One paired machine: its status and routes, and what can be done with it. */
export default function MachineDetailScreen() {
	const { id } = useLocalSearchParams<{ id: string }>();
	const router = useRouter();
	const navigation = useNavigation();
	const t = useTheme();
	const { hostStates, configForHost, switchHost, reloadConfig, reloadHostMetadata } = useApp();
	const [host, setHost] = useState<Host | null | undefined>(undefined);
	const [defaultHostId, setDefaultHostId] = useState<string | null>(null);
	const [switching, setSwitching] = useState(false);
	const [forgetting, setForgetting] = useState(false);

	// Read again on every return, so a save from the editor shows at once.
	const reload = useCallback(async () => {
		try {
			const [saved, selected] = await Promise.all([loadHosts(), activeHost()]);
			setHost(saved.find((candidate) => candidate.id === id) ?? null);
			setDefaultHostId(selected?.id ?? null);
		} catch {
			setHost((current) => current ?? null);
		}
	}, [id]);
	useFocusEffect(useCallback(() => { void reload(); }, [reload]));

	// A forget can't be called back, so the page stays until it lands: no swipe
	// back from it, nor from Settings around it.
	useEffect(() => {
		if (!forgetting) return;
		const held = [navigation, navigation.getParent()];
		for (const screen of held) screen?.setOptions({ gestureEnabled: false });
		return () => { for (const screen of held) screen?.setOptions({ gestureEnabled: true }); };
	}, [forgetting, navigation]);

	const edit = useCallback(() => {
		router.push({ pathname: "/settings/machine/[id]/edit", params: { id } });
	}, [id, router]);

	// Titled for the machine. Memoized on the name, so a fleet poll that
	// re-renders the page doesn't set the header's options again.
	const machineName = host?.name;
	const titleScreen = useMemo(
		() => machineName === undefined ? null : <Stack.Screen options={{ title: machineName }} />,
		[machineName],
	);

	if (host === undefined) {
		return <View style={[styles.screen, styles.center, { backgroundColor: t.bgBase }]}><Spinner size="large" accessibilityLabel="Loading machine" /></View>;
	}
	if (host === null) {
		return (
			<View style={[styles.screen, { backgroundColor: t.bgBase }]}>
				<EmptyState icon="disconnected" title="Machine not found" message="This machine is no longer paired with this phone." />
			</View>
		);
	}
	const machine = host;
	const snapshot = hostStates.find((item) => item.hostId === machine.id);
	const isDefault = machine.id === defaultHostId;

	async function makeDefault() {
		if (isDefault || switching || forgetting) return;
		haptics.select();
		setSwitching(true);
		try {
			await switchHost(machine.id);
			setDefaultHostId(machine.id);
		} catch {
			haptics.error();
			Alert.alert("Could not change default machine", "Try again from Machines.");
		} finally {
			setSwitching(false);
		}
	}

	function requestForget() {
		if (switching || forgetting) return;
		haptics.warning();
		Alert.alert(`Forget ${machine.name}?`, FORGET_MACHINE_MESSAGE, [
			{ text: "Cancel", style: "cancel" },
			{ text: "Forget", style: "destructive", onPress: () => void forget() },
		]);
	}

	async function forget() {
		setForgetting(true);
		try {
			const result = await forgetEnvironmentMachine(machine, defaultHostId);
			haptics.success();
			postEnvironmentNotice(result.notice);
			// Refresh fleet metadata for the other machines too, but never hold the
			// page behind another machine's connection.
			void reloadHostMetadata().catch(() => {});
			if (result.wasDefault) void reloadConfig().catch(() => {});
			// Left while it ran (Android's back still works): navigating now would
			// act on whatever is on top. The notice waits for the list instead.
			if (!navigation.isFocused()) return;
			// Back to the list, which says what happened. A page opened by a link
			// has no list under it, so the list takes its place instead.
			router.dismissTo("/settings/environments");
		} catch (error) {
			haptics.error();
			Alert.alert("Couldn't forget machine", error instanceof Error ? error.message : "Try again.");
		} finally {
			setForgetting(false);
		}
	}

	return (
		<View style={[styles.screen, { backgroundColor: t.bgBase }]}>
			{titleScreen}
			<MachineDetail
				host={machine}
				activeConfig={snapshot?.config ?? configForHost(machine.id) ?? undefined}
				isDefault={isDefault}
				status={environmentStatus(snapshot)}
				switching={switching}
				forgetting={forgetting}
				onMakeDefault={() => void makeDefault()}
				onReconnect={() => router.push({ pathname: "/pair", params: { repairHostId: machine.id } })}
				onForget={requestForget}
				onEdit={edit}
			/>
		</View>
	);
}

const styles = StyleSheet.create({
	screen: { flex: 1 },
	center: { alignItems: "center", justifyContent: "center" },
});
