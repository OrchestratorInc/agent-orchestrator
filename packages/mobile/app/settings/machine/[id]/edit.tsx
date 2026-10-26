import { Stack, useLocalSearchParams, useNavigation, usePreventRemove, useRouter } from "expo-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { Alert, Platform, StyleSheet, View } from "react-native";
import { backOr } from "../../../../lib/backNavigation";
import { saveEnvironmentMachine } from "../../../../lib/environmentActions";
import { activeHost, findHost, type Host } from "../../../../lib/hosts";
import { MachineEditorForm } from "../../../../lib/MachineEditorForm";
import { Spinner } from "../../../../lib/Spinner";
import { useApp } from "../../../../lib/store";
import { useTheme } from "../../../../lib/ThemeProvider";
import { toolbarIcon } from "../../../../lib/toolbarIcon";
import { EmptyState } from "../../../../lib/ui";
import { useMachineEditor } from "../../../../lib/useMachineEditor";

export { RouteErrorBoundary as ErrorBoundary } from "../../../../lib/RouteErrorBoundary";

/** Edits a paired machine's name, icon, and the route the phone reaches it by. */
export default function MachineEditScreen() {
	const { id } = useLocalSearchParams<{ id: string }>();
	const t = useTheme();
	const [machine, setMachine] = useState<{ host: Host; isDefault: boolean } | null | undefined>(undefined);
	useEffect(() => {
		let current = true;
		void Promise.all([findHost(id), activeHost()])
			.then(([host, selected]) => { if (current) setMachine(host ? { host, isDefault: selected?.id === host.id } : null); })
			.catch(() => { if (current) setMachine(null); });
		return () => { current = false; };
	}, [id]);

	return (
		<View style={[styles.screen, { backgroundColor: t.bgBase }]}>
			{machine === undefined ? (
				<View style={[styles.screen, styles.center]}><Spinner size="large" accessibilityLabel="Loading machine" /></View>
			) : machine === null ? (
				<EmptyState icon="disconnected" title="Machine not found" message="This machine is no longer paired with this phone." />
			) : (
				<MachineEditor host={machine.host} isDefault={machine.isDefault} />
			)}
		</View>
	);
}

function MachineEditor({ host, isDefault }: { host: Host; isDefault: boolean }) {
	const router = useRouter();
	const navigation = useNavigation();
	const { hostStates, configForHost, reloadConfig, reloadHostMetadata } = useApp();
	const snapshot = hostStates.find((item) => item.hostId === host.id);
	const editor = useMachineEditor(host, snapshot?.config ?? configForHost(host.id) ?? undefined, async (draft) => {
		const { routeChanged } = await saveEnvironmentMachine(host, draft, Platform.OS);
		await reloadHostMetadata();
		// The default machine reconnects over the route it was just given.
		if (routeChanged && isDefault) void reloadConfig().catch(() => {});
	});
	const [saved, setSaved] = useState(false);

	// Save is the bar's trailing item: text on iOS, a check on Android, whose bar
	// draws icons only. Memoized on what it shows, so typing does not register
	// the header again on every key; the item saves the latest draft.
	const save = async () => { if (await editor.save()) setSaved(true); };
	const saveRef = useRef(save);
	saveRef.current = save;
	const { saving, canSave } = editor;
	const headerItems = useMemo(() => (
		<Stack.Toolbar placement="right">
			{saving ? (
				<Stack.Toolbar.View hidesSharedBackground>
					<Spinner accessibilityLabel="Saving changes" />
				</Stack.Toolbar.View>
			) : Platform.OS === "ios" ? (
				<Stack.Toolbar.Button accessibilityLabel="Save machine changes" disabled={!canSave} style={{ fontWeight: "600" }} onPress={() => void saveRef.current()}>Save</Stack.Toolbar.Button>
			) : (
				<Stack.Toolbar.Button icon={toolbarIcon("checkmark")} accessibilityLabel="Save machine changes" disabled={!canSave} onPress={() => void saveRef.current()} />
			)}
		</Stack.Toolbar>
	), [canSave, saving]);

	// Unsaved edits hold the page: the Settings sheet can't be swiped away, the
	// way iOS keeps any sheet with unsaved edits in place, and going back asks
	// first. A save in flight can't be called back, so it holds too.
	const holding = !saved && (editor.dirty || saving);
	useEffect(() => {
		if (!holding) return;
		const settings = navigation.getParent();
		settings?.setOptions({ gestureEnabled: false });
		return () => settings?.setOptions({ gestureEnabled: true });
	}, [holding, navigation]);
	usePreventRemove(holding, ({ data }) => {
		if (saving) return;
		Alert.alert("Discard changes?", "Your changes to this machine haven't been saved.", [
			{ text: "Keep editing", style: "cancel" },
			{ text: "Discard", style: "destructive", onPress: () => navigation.dispatch(data.action) },
		]);
	});
	// Leave once the save has landed. After the hold above, so it has let go.
	useEffect(() => { if (saved) backOr(router, `/settings/machine/${host.id}`); }, [host.id, router, saved]);

	return (
		<>
			{headerItems}
			<MachineEditorForm host={host} editor={editor} />
		</>
	);
}

const styles = StyleSheet.create({
	screen: { flex: 1 },
	center: { alignItems: "center", justifyContent: "center" },
});
