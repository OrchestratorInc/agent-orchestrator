import { useFocusEffect, useRouter } from "expo-router";
import { useCallback, useState } from "react";
import {
	ActivityIndicator,
	Alert,
	Platform,
	Pressable,
	RefreshControl,
	StyleSheet,
	Text,
	View,
} from "react-native";
import { KeyboardAvoidingView, KeyboardAwareScrollView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { ApiError, pingServer } from "../../lib/api";
import { classifyConnectionFailure, describeConnectionFailure } from "../../lib/connectionError";
import { probeEndpoint } from "../../lib/connectRuntime";
import { forgetPairedHost, forgetServer } from "../../lib/disconnect";
import { EnvironmentEditorActions, EnvironmentMachineRow, type EnvironmentEditorState, type EnvironmentMachineSave } from "../../lib/EnvironmentMachineRow";
import { haptics } from "../../lib/haptics";
import { activeHost, activeHostMetadata, loadHosts, renameHost, saveHost, updateHostAppearance, type Host as PairedHost } from "../../lib/hosts";
import { Feather } from "../../lib/icons";
import { AccountManagedHostError, DifferentMachineError, persistManualHostEdit } from "../../lib/manualConnect";
import { machineAppearance } from "../../lib/machineAppearance";
import { NativeHeaderButton } from "../../lib/native-header-button";
import { IncompatibleHostVersionError } from "../../lib/race";
import { useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { iconSize, space, touchTarget, type } from "../../lib/tokens";
import { backOr } from "../../lib/backNavigation";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

export default function EnvironmentsScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { hostStates, configForHost, switchHost, refreshAll, reloadConfig, reloadHostMetadata } = useApp();
	const [hosts, setHosts] = useState<PairedHost[]>([]);
	const [defaultHostId, setDefaultHostId] = useState<string | null>(null);
	const [expandedHostId, setExpandedHostId] = useState<string | null | undefined>(undefined);
	const [switchingHostId, setSwitchingHostId] = useState<string | null>(null);
	const [deletingHostId, setDeletingHostId] = useState<string | null>(null);
	const [loading, setLoading] = useState(true);
	const [refreshing, setRefreshing] = useState(false);
	const [loadError, setLoadError] = useState<string>();
	const [notice, setNotice] = useState<string>();
	const [editor, setEditor] = useState<EnvironmentEditorState | null>(null);
	const [footerHeight, setFooterHeight] = useState(touchTarget + space.md * 2 + insets.bottom);
	const activeEditor = editor?.hostId === expandedHostId ? editor : null;
	const onEditorStateChange = useCallback((next: EnvironmentEditorState | null, hostId: string) => {
		// A collapsing previous row must not clear the new machine's footer.
		setEditor((current) => next ?? (current?.hostId === hostId ? null : current));
	}, []);

	const reloadHosts = useCallback(async () => {
		try {
			const [saved, selected] = await Promise.all([loadHosts(), activeHost()]);
			setHosts(saved);
			setDefaultHostId(selected?.id ?? null);
			setExpandedHostId((current) => {
				if (current === undefined) return selected?.id ?? null;
				if (current === null || saved.some((host) => host.id === current)) return current;
				return selected?.id ?? null;
			});
			setLoadError(undefined);
		} catch {
			setLoadError("The saved environments couldn't be loaded.");
		} finally {
			setLoading(false);
		}
	}, []);

	useFocusEffect(useCallback(() => {
		void reloadHosts();
	}, [reloadHosts]));

	async function makeDefault(host: PairedHost) {
		if (host.id === defaultHostId || switchingHostId) return;
		haptics.select();
		setSwitchingHostId(host.id);
		try {
			await switchHost(host.id);
			setDefaultHostId(host.id);
		} catch {
			haptics.error();
			Alert.alert("Could not change default machine", "Try again from Environments.");
		} finally {
			setSwitchingHostId(null);
		}
	}

	async function refresh() {
		setRefreshing(true);
		try {
			await Promise.all([refreshAll(), reloadHosts()]);
		} finally {
			setRefreshing(false);
		}
	}

	async function saveEnvironment(host: PairedHost, draft: EnvironmentMachineSave) {
		const before = host.endpoints[draft.endpointIndex];
		const appearanceChanged = machineAppearance(host).symbol !== draft.appearance.symbol;
		try {
			await persistManualHostEdit(host, draft.config, draft.machineName, draft.endpointIndex, {
				probe: (endpoint) => probeEndpoint(endpoint, new AbortController().signal),
				ping: pingServer,
				saveHost,
				renameHost,
			});
			if (appearanceChanged) await updateHostAppearance(host.id, draft.appearance);
		} catch (error) {
			if (error instanceof DifferentMachineError || error instanceof AccountManagedHostError) throw error;
			const status = error instanceof IncompatibleHostVersionError ? 426 : error instanceof ApiError ? error.status : undefined;
			const copy = describeConnectionFailure(classifyConnectionFailure(status), {
				host: draft.config.host,
				port: draft.config.httpPort,
				platform: Platform.OS,
			});
			throw new Error(classifyConnectionFailure(status) === "auth"
				? "That password was rejected. Check it and try again."
				: copy.message);
		}

		await Promise.all([reloadHosts(), reloadHostMetadata()]);
		const routeChanged = !before || before.host !== draft.config.host.trim() || before.port !== Number(draft.config.httpPort) ||
			before.secure !== Boolean(draft.config.secure) || host.token !== draft.config.password;
		if (routeChanged && host.id === defaultHostId) void reloadConfig().catch(() => {});
	}

	async function forgetEnvironment(host: PairedHost) {
		if (deletingHostId !== null || switchingHostId !== null) return;
		setDeletingHostId(host.id);
		setNotice(undefined);
		try {
			// defaultHostId is only refreshed on focus; account sync may have moved
			// the selection since. Decide from storage, and always forget this row.
			let selectedId = defaultHostId;
			let failed = false;
			try {
				selectedId = (await activeHostMetadata())?.id ?? null;
				if (selectedId === host.id) await forgetServer(host);
				else await forgetPairedHost(host);
			} catch {
				failed = true;
			}
			let remaining: PairedHost[];
			try {
				remaining = await loadHosts();
			} catch {
				haptics.error();
				Alert.alert("Couldn't forget machine", "The saved machines couldn't be read. Try again.");
				return;
			}
			if (failed && remaining.some((candidate) => candidate.id === host.id)) {
				haptics.error();
				Alert.alert("Couldn't forget machine", "This machine's saved connection couldn't be removed. Try again.");
				return;
			}
			setHosts(remaining);
			const deletingDefault = selectedId === host.id;
			// removeHost clears the selected pointer; loadHosts is recency-sorted,
			// matching activeHost's fallback without another keychain/network wait.
			const nextDefault = remaining.find((candidate) => candidate.id === selectedId) ?? remaining[0];
			setDefaultHostId(nextDefault?.id ?? null);
			setExpandedHostId((current) => current === host.id ? null : current);
			setEditor((current) => current?.hostId === host.id ? null : current);
			setNotice(`${host.name} forgotten.${deletingDefault && nextDefault ? ` ${nextDefault.name} is now the default.` : ""}`);
			haptics.success();
			// Refresh fleet metadata for non-defaults too, but never hold the cards
			// or last-machine empty state behind another machine's connection.
			void reloadHostMetadata().catch(() => {});
			if (deletingDefault) void reloadConfig().catch(() => {});
		} finally {
			setDeletingHostId(null);
		}
	}

	return (
		<View style={styles.screen}>
			<View style={styles.header}>
				<View style={[styles.headerButton, styles.backButton]}>
					<NativeHeaderButton icon="back" label="Back to Settings" onPress={() => backOr(router, "/settings")} />
				</View>
				<Text style={styles.headerTitle}>Environments</Text>
				<View style={[styles.headerButton, styles.addButton]}>
					<NativeHeaderButton icon="add" label="Add machine" onPress={() => router.push("/pair")} />
				</View>
			</View>
			{notice ? (
				<View style={styles.notice}>
					<Text testID="environment-forgotten-notice" accessibilityRole="alert" accessibilityLiveRegion="polite" style={styles.noticeText}>{notice}</Text>
					<Pressable accessibilityRole="button" accessibilityLabel="Dismiss confirmation" onPress={() => setNotice(undefined)} style={styles.noticeDismiss}>
						<Feather name="x" size={iconSize.sm} color={t.textSecondary} />
					</Pressable>
				</View>
			) : null}

			<KeyboardAvoidingView behavior="padding" automaticOffset style={styles.keyboardArea}>
			<View style={styles.scrollViewport}>
				{loading ? (
					<View style={styles.center}><ActivityIndicator color={t.accent} /></View>
				) : (
					<KeyboardAwareScrollView
					style={styles.list}
					contentInsetAdjustmentBehavior="never"
					automaticallyAdjustKeyboardInsets={false}
					bottomOffset={activeEditor ? footerHeight + space.sm : space.md}
					disableScrollOnKeyboardHide
					keyboardDismissMode="interactive"
					keyboardShouldPersistTaps="handled"
					showsVerticalScrollIndicator={false}
					contentContainerStyle={hosts.length === 0 ? styles.emptyContent : styles.content}
					refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void refresh()} tintColor={t.accent} />}
				>
					<View style={styles.intro}>
						<Text style={styles.introCopy}>Machines available for new workers and reconnects.</Text>
						{loadError ? <Text selectable style={styles.errorText}>{loadError}</Text> : null}
					</View>
					{hosts.map((host) => {
						const snapshot = hostStates.find((item) => item.hostId === host.id);
						const activeConfig = snapshot?.config ?? configForHost(host.id);
						const isDefault = host.id === defaultHostId;
						const status = snapshot?.connection === "open"
							? { label: "Connected", color: t.green, tint: t.tintGreen }
							: snapshot?.connection === "connecting" || snapshot?.loading
								? { label: "Connecting", color: t.textSecondary, tint: t.bgSubtle }
								: { label: "Offline", color: t.red, tint: t.tintRed };
						const switching = switchingHostId === host.id;
						return <EnvironmentMachineRow
							key={host.id}
							host={host}
							activeConfig={activeConfig ?? undefined}
							isDefault={isDefault}
							expanded={expandedHostId === host.id}
							onExpandedChange={(expanded) => setExpandedHostId((current) => expanded ? host.id : current === host.id ? null : current)}
							status={status}
							switching={switching}
							switchingDisabled={Boolean(switchingHostId) || Boolean(deletingHostId)}
							forgetting={deletingHostId === host.id}
							forgetDisabled={Boolean(deletingHostId) || Boolean(switchingHostId)}
							onMakeDefault={() => void makeDefault(host)}
							onReconnect={() => { haptics.tap(); router.push({ pathname: "/pair", params: { repairHostId: host.id } }); }}
							onForget={() => forgetEnvironment(host)}
							onSave={(draft) => saveEnvironment(host, draft)}
							onEditorStateChange={onEditorStateChange}
						/>;
					})}
					{hosts.length === 0 && !loadError ? (
						<View style={styles.emptyCard}>
							<View style={styles.emptyIcon}><Feather name="share-2" size={iconSize.xl} color={t.textTertiary} /></View>
							<Text style={styles.emptyTitle}>No saved machines</Text>
							<Text style={styles.emptyCopy}>Connect a machine to view its workers.</Text>
							<Pressable testID="environment-add-machine" accessibilityRole="button" onPress={() => { haptics.tap(); router.push("/pair"); }} style={({ pressed }) => [styles.addMachine, pressed && { opacity: 0.8 }]}>
								<Text style={styles.addMachineText}>Add a machine</Text>
							</Pressable>
						</View>
					) : null}
					</KeyboardAwareScrollView>
				)}
			</View>
			{activeEditor ? (
				<View testID="environment-editor-footer" onLayout={(event) => setFooterHeight(event.nativeEvent.layout.height)} style={[styles.editorFooter, { paddingBottom: Math.max(insets.bottom, space.md) }]}>
					<EnvironmentEditorActions editor={activeEditor} />
				</View>
			) : null}
			</KeyboardAvoidingView>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	keyboardArea: { flex: 1 },
	notice: { flexDirection: "row", alignItems: "center", marginHorizontal: space.lg, marginBottom: space.sm, paddingLeft: space.md, borderRadius: 12, backgroundColor: t.bgSubtle },
	noticeText: { flex: 1, paddingVertical: space.sm, fontFamily: "Geist_500Medium", color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	noticeDismiss: { width: touchTarget, minHeight: touchTarget, alignItems: "center", justifyContent: "center" },
	editorFooter: { paddingHorizontal: space.lg, paddingTop: space.sm, backgroundColor: t.bgBase, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderDefault },
	scrollViewport: { flex: 1, overflow: "hidden" },
	list: { flex: 1 },
	header: { height: 88, alignItems: "center", justifyContent: "center", paddingHorizontal: space.lg, backgroundColor: t.bgBase, zIndex: 1 },
	headerTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title3.fontSize, lineHeight: type.title3.lineHeight, fontWeight: "600", letterSpacing: -0.3 },
	headerButton: { position: "absolute", top: 22, width: 44, height: 44, borderRadius: 22, overflow: "hidden" },
	backButton: { left: 14 },
	addButton: { right: 14 },
	center: { flex: 1, alignItems: "center", justifyContent: "center" },
	content: { paddingBottom: space.xxxl },
	emptyContent: { flexGrow: 1, paddingBottom: space.xxxl },
	intro: { gap: space.xxs, paddingHorizontal: space.lg, paddingVertical: space.md },
	introCopy: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	errorText: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingTop: space.xs },
	emptyCard: { flex: 1, minHeight: 260, alignItems: "center", justifyContent: "center", gap: space.sm, marginHorizontal: space.lg, padding: space.xl, borderRadius: 24, borderCurve: "continuous", backgroundColor: t.bgSubtle },
	emptyIcon: { width: 58, height: 58, borderRadius: 18, borderCurve: "continuous", alignItems: "center", justifyContent: "center", backgroundColor: t.bgSubtle, marginBottom: space.xs },
	emptyTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.headline.fontSize, lineHeight: type.headline.lineHeight, fontWeight: "600", textAlign: "center" },
	emptyCopy: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, textAlign: "center" },
	addMachine: { minHeight: touchTarget, justifyContent: "center", paddingHorizontal: space.lg, marginTop: space.sm, backgroundColor: t.textPrimary, borderRadius: 12, borderCurve: "continuous" },
	addMachineText: { fontFamily: "Geist_600SemiBold", color: t.bgBase, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
});
