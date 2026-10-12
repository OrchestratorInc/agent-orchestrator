import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { ActivityIndicator, Alert, Animated as NativeAnimated, Keyboard, Pressable, StyleSheet, Switch, Text, TextInput, View } from "react-native";
import Swipeable from "react-native-gesture-handler/Swipeable";
import Animated, { Easing, FadeIn, FadeOut, LinearTransition } from "react-native-reanimated";
import type { ServerConfig } from "./config";
import { rowDividerWidth } from "./divider";
import { environmentEndpointRows, environmentPlatformLabel } from "./environmentView";
import { haptics } from "./haptics";
import type { Host } from "./hosts";
import { Feather } from "./icons";
import { MachineAvatar, MachineIdentity } from "./MachineIdentity";
import { machineAppearance, machineAppearancePalette, machineSymbolLabels, type MachineAppearance, type MachineSymbol } from "./machineAppearance";
import { LAYOUT_MS, ROW_ENTER_MS } from "./motion";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { easingCurve, iconSize, radius, space, touchTarget, type } from "./tokens";
import { useReducedMotion } from "./useReducedMotion";

export type EnvironmentMachineSave = {
	machineName: string;
	appearance: MachineAppearance;
	config: ServerConfig;
	endpointIndex: number;
};

export type EnvironmentEditorState = {
	hostId: string;
	canSave: boolean;
	saving: boolean;
	error?: string;
	onSave: () => void;
	onCancel: () => void;
};

type Props = {
	host: Host;
	activeConfig?: ServerConfig;
	isDefault: boolean;
	expanded: boolean;
	onExpandedChange: (expanded: boolean) => void;
	status: { label: string; color: string; tint: string };
	switching: boolean;
	switchingDisabled: boolean;
	onMakeDefault: () => void;
	onReconnect: () => void;
	onSave: (draft: EnvironmentMachineSave) => Promise<void>;
	forgetDisabled: boolean;
	forgetting: boolean;
	onForget: () => Promise<void>;
	onEditorStateChange?: (editor: EnvironmentEditorState | null, hostId: string) => void;
};

function editableEndpointIndex(host: Host, activeConfig?: ServerConfig): number {
	const activeIndex = activeConfig?.hostId === host.id
		? host.endpoints.findIndex((endpoint) => endpoint.host === activeConfig.host && endpoint.port === Number(activeConfig.httpPort) && endpoint.secure === Boolean(activeConfig.secure))
		: -1;
	return activeIndex < 0 ? 0 : activeIndex;
}

function editConfig(host: Host, endpointIndex: number): ServerConfig {
	const endpoint = host.endpoints[endpointIndex];
	return endpoint ? {
		host: endpoint.host,
		httpPort: String(endpoint.port),
		muxPort: "14801",
		secure: endpoint.secure,
		password: host.token,
		hostId: host.id,
		endpointKind: endpoint.kind,
	} : {
		host: "",
		httpPort: "3011",
		muxPort: "14801",
		secure: false,
		password: host.token,
		hostId: host.id,
	};
}

function endpointDisplayAddress(address: string): { host: string; authority: string } {
	try {
		const parsed = new URL(address);
		return { host: parsed.hostname, authority: parsed.host };
	} catch {
		const authority = address.replace(/^[a-z][a-z\d+.-]*:\/\//i, "").replace(/\/$/, "");
		return { host: authority.replace(/:\d+$/, ""), authority };
	}
}

export function EnvironmentMachineRow({ host, activeConfig, isDefault, expanded, onExpandedChange, status, switching, switchingDisabled, onMakeDefault, onReconnect, onSave, forgetDisabled, forgetting, onForget, onEditorStateChange }: Props) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const reduceMotion = useReducedMotion();
	const swipeableRef = useRef<Swipeable>(null);
	const layoutTransition = reduceMotion
		? undefined
		: LinearTransition.duration(LAYOUT_MS).easing(Easing.bezier(...easingCurve.standard));
	const detailEntering = reduceMotion ? undefined : FadeIn.duration(ROW_ENTER_MS);
	const detailExiting = reduceMotion ? undefined : FadeOut.duration(ROW_ENTER_MS);
	const endpoints = environmentEndpointRows(host, activeConfig);
	const primaryEndpoint = endpoints.find((endpoint) => endpoint.active) ?? endpoints[0];
	const primaryDisplay = primaryEndpoint ? endpointDisplayAddress(primaryEndpoint.address) : undefined;
	const otherEndpoints = endpoints.filter((endpoint) => endpoint.key !== primaryEndpoint?.key);
	const [showOtherRoutes, setShowOtherRoutes] = useState(false);
	const [editing, setEditing] = useState(false);
	const [appearanceOpen, setAppearanceOpen] = useState(false);
	const [endpointIndex, setEndpointIndex] = useState(0);
	const [machineName, setMachineName] = useState(host.name);
	const [appearance, setAppearance] = useState<MachineAppearance>(() => machineAppearance(host));
	const [config, setConfig] = useState<ServerConfig>(() => editConfig(host, 0));
	const [saving, setSaving] = useState(false);
	const [error, setError] = useState<string>();
	const [showPassword, setShowPassword] = useState(false);

	const accountManaged = Boolean(host.accountUserId);
	const canForget = !editing && !saving && !forgetDisabled && !forgetting;
	const swipeEnabled = !expanded && canForget;
	useEffect(() => {
		if (!swipeEnabled) swipeableRef.current?.close();
	}, [swipeEnabled]);
	useEffect(() => {
		if (expanded) return;
		setEditing(false);
		setAppearanceOpen(false);
		setShowOtherRoutes(false);
		setError(undefined);
		setShowPassword(false);
	}, [expanded]);

	function requestForget() {
		swipeableRef.current?.close();
		if (!canForget) return;
		haptics.warning();
		Alert.alert(
			`Forget ${host.name}?`,
			"Remove this machine's saved connection from this phone? Its desktop workers and files won't be deleted. You can pair it again later.\n\nNotification cleanup will finish when the machine is reachable.",
			[
				{ text: "Cancel", style: "cancel" },
				{ text: "Forget", style: "destructive", onPress: () => void onForget() },
			],
		);
	}

	function beginEditing() {
		if (editing) {
			cancelEditing();
			return;
		}
		const nextEndpointIndex = editableEndpointIndex(host, activeConfig);
		setEndpointIndex(nextEndpointIndex);
		setMachineName(host.name);
		setAppearance(machineAppearance(host));
		setAppearanceOpen(false);
		setConfig(editConfig(host, nextEndpointIndex));
		setError(undefined);
		setShowPassword(false);
		setEditing(true);
		haptics.tap();
	}

	function cancelEditing() {
		Keyboard.dismiss();
		setEditing(false);
		setAppearanceOpen(false);
		setError(undefined);
		haptics.tap();
	}

	const set = (key: keyof ServerConfig) => (value: string) => {
		setConfig((current) => ({ ...current, [key]: value }));
		setError(undefined);
	};
	const validPort = /^\d+$/.test(config.httpPort) && Number(config.httpPort) >= 1 && Number(config.httpPort) <= 65535;
	const canSave = Boolean(machineName.trim() && config.host.trim() && validPort) && !saving;
	// The fixed footer lives outside this scrolling card. Its callbacks must
	// read the latest draft even when a valid edit leaves canSave unchanged.
	const editorCommands = useRef({ save, cancelEditing });
	editorCommands.current = { save, cancelEditing };
	useLayoutEffect(() => {
		onEditorStateChange?.(editing && expanded ? {
			hostId: host.id, canSave, saving, error,
			onSave: () => { void editorCommands.current.save(); },
			onCancel: () => editorCommands.current.cancelEditing(),
		} : null, host.id);
	}, [editing, expanded, host.id, canSave, saving, error, onEditorStateChange]);
	useEffect(() => () => onEditorStateChange?.(null, host.id), [onEditorStateChange, host.id]);

	async function save() {
		if (!canSave) return;
		setSaving(true);
		setError(undefined);
		try {
			await onSave({ machineName, appearance, config, endpointIndex });
			Keyboard.dismiss();
			setEditing(false);
			setAppearanceOpen(false);
			haptics.success();
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : "The machine could not be updated. Try again.");
			haptics.warning();
		} finally {
			setSaving(false);
		}
	}

	function toggleExpanded() {
		if (expanded) {
			setEditing(false);
			setAppearanceOpen(false);
			setShowOtherRoutes(false);
			setError(undefined);
		}
		onExpandedChange(!expanded);
		haptics.tap();
	}

	return (
		<Animated.View
			testID={`environment-layout-${host.id}`}
			layout={layoutTransition}
			style={styles.layoutShell}
		>
		<Swipeable
			ref={swipeableRef}
			enabled={swipeEnabled}
			containerStyle={styles.swipeShell}
			dragOffsetFromRightEdge={16}
			activeOffsetX={-16}
			failOffsetY={[-12, 12]}
			rightThreshold={72}
			overshootRight={false}
			renderRightActions={(progress) => (
				<>
					{/* Fill behind the moving rounded card, but never tint its resting
					    border. Swipeable supplies native-driven progress on both OSes. */}
					<NativeAnimated.View
						testID={`environment-forget-underlay-${host.id}`}
						pointerEvents="none"
						style={[styles.forgetUnderlay, { opacity: progress.interpolate({
							inputRange: [0, 0.01], outputRange: [0, 1], extrapolate: "clamp",
						}) }]}
					/>
					<Pressable
						testID={`environment-forget-${host.id}`}
						accessibilityRole="button"
						accessibilityLabel={`Forget ${host.name}`}
						accessibilityState={{ disabled: !swipeEnabled, busy: forgetting }}
						disabled={!swipeEnabled}
						onPress={requestForget}
						style={({ pressed }) => [styles.forgetRail, pressed && styles.forgetRailPressed]}
					>
						{forgetting ? <ActivityIndicator size="small" color="#ffffff" /> : <Feather name="trash-2" size={iconSize.lg} color="#ffffff" />}
						<Text style={styles.forgetText}>Forget</Text>
					</Pressable>
				</>
			)}
		>
		<View testID={`environment-machine-${host.id}`} style={[styles.row, forgetting && styles.disabled]}>
			<Pressable
				testID={`environment-summary-${host.id}`}
				accessibilityRole="button"
				accessibilityLabel={`${expanded ? "Collapse" : "Expand"} ${host.name}`}
				accessibilityState={{ expanded }}
				onPress={toggleExpanded}
				style={({ pressed }) => [styles.header, pressed && styles.pressed]}
			>
				{/* The draft only previews inside the editor; Cancel must leave the saved icon. */}
				<MachineAvatar appearance={editing ? appearance : machineAppearance(host)} size={touchTarget} />
				<View style={styles.machineCopy}>
					<View style={styles.machineNameRow}>
						<Text style={styles.machineName} numberOfLines={1}>{host.name}</Text>
						{isDefault ? <Text style={styles.defaultPill}>Default</Text> : null}
					</View>
					<Text testID={`environment-route-summary-${host.id}`} selectable style={styles.routeSummary} numberOfLines={1}>
						{primaryEndpoint && primaryDisplay ? `${primaryEndpoint.label} · ${primaryDisplay.host}` : environmentPlatformLabel(host.platform)}
					</Text>
				</View>
				<View style={styles.statusPill}>
					<View style={[styles.statusDot, { backgroundColor: status.color }]} />
					<Text style={[styles.statusText, { color: status.color }]}>{status.label}</Text>
				</View>
				<Feather name={expanded ? "chevron-up" : "chevron-down"} size={iconSize.sm} color={t.textTertiary} />
			</Pressable>

			{expanded ? (
				<Animated.View testID={`environment-details-${host.id}`} entering={detailEntering} exiting={detailExiting}>
					{primaryEndpoint && primaryDisplay ? (
						<View style={styles.activeRoute}>
							<View style={styles.activeRouteLabel}>
								<Feather name="sliders" size={iconSize.sm} color={t.textSecondary} />
								<Text style={styles.activeRouteTitle}>Active route</Text>
							</View>
							<View style={styles.activeAddressSurface}>
								<Text testID={`environment-active-address-${host.id}`} selectable style={styles.activeAddress} numberOfLines={1}>{primaryDisplay.authority}</Text>
							</View>
						</View>
					) : <Text style={styles.noRoutes}>No saved connection routes.</Text>}

					{otherEndpoints.length > 0 ? (
						<View style={styles.otherRoutesSection}>
							<Pressable
								testID={`environment-other-routes-${host.id}`}
								accessibilityRole="button"
								accessibilityLabel={`${showOtherRoutes ? "Hide" : "Show"} other routes for ${host.name}`}
								accessibilityState={{ expanded: showOtherRoutes }}
								onPress={() => { setShowOtherRoutes((current) => !current); haptics.tap(); }}
								style={({ pressed }) => [styles.otherRoutesToggle, pressed && styles.pressed]}
							>
								<View style={styles.otherRoutesCopy}>
									<Text style={styles.otherRoutesTitle}>Other routes</Text>
									<Text style={styles.otherRoutesCount}>{otherEndpoints.length} available</Text>
								</View>
								<Feather name={showOtherRoutes ? "chevron-down" : "chevron-right"} size={iconSize.sm} color={t.textTertiary} />
							</Pressable>
							{showOtherRoutes ? (
								<View style={styles.otherRoutesList}>
									{otherEndpoints.map((endpoint) => {
										const display = endpointDisplayAddress(endpoint.address);
										return (
											<View key={endpoint.key} testID={`environment-other-route-${endpoint.key}`} style={styles.otherRouteRow}>
												<Text style={styles.otherRouteLabel}>{endpoint.label}</Text>
												<Text selectable style={styles.otherRouteAddress} numberOfLines={1}>{display.authority}</Text>
											</View>
										);
									})}
								</View>
							) : null}
						</View>
					) : null}

					<View style={styles.actionArea}>
						{!isDefault ? (
							<Pressable accessibilityRole="button" accessibilityLabel={`Make ${host.name} default`} disabled={switchingDisabled} onPress={onMakeDefault} style={({ pressed }) => [styles.defaultAction, pressed && styles.pressed, switchingDisabled && styles.disabled]}>
								{switching ? <ActivityIndicator size="small" color={t.textSecondary} /> : <Feather name="check-circle" size={iconSize.sm} color={t.textSecondary} />}
								<Text style={styles.actionText}>Make default</Text>
							</Pressable>
						) : null}
						<View style={styles.actions}>
							<Pressable testID={`environment-edit-${host.id}`} accessibilityRole="button" accessibilityLabel={`${editing ? "Close editor for" : "Edit"} ${host.name}`} accessibilityState={{ expanded: editing }} disabled={saving} onPress={beginEditing} style={({ pressed }) => [styles.action, pressed && styles.pressed, saving && styles.disabled]}>
								<Feather name="edit-2" size={iconSize.sm} color={t.textSecondary} />
								<Text style={styles.actionText}>Edit</Text>
								<Feather name={editing ? "chevron-up" : "chevron-down"} size={iconSize.sm} color={t.textTertiary} />
							</Pressable>
							<View style={styles.actionDivider} />
							<Pressable testID={`environment-reconnect-${host.id}`} accessibilityRole="button" accessibilityLabel={`Reconnect ${host.name}`} disabled={saving} onPress={onReconnect} style={({ pressed }) => [styles.action, pressed && styles.pressed, saving && styles.disabled]}>
								<Feather name="refresh-cw" size={iconSize.sm} color={t.textSecondary} />
								<Text style={styles.actionText}>Reconnect</Text>
							</Pressable>
						</View>
						{!editing ? (
							<Pressable testID={`environment-forget-action-${host.id}`} accessibilityRole="button" accessibilityLabel={`Forget ${host.name}`} accessibilityState={{ disabled: !canForget, busy: forgetting }} disabled={!canForget} onPress={requestForget} style={({ pressed }) => [styles.forgetAction, pressed && styles.pressed, !canForget && styles.disabled]}>
								{forgetting ? <ActivityIndicator size="small" color={t.red} /> : <Feather name="trash-2" size={iconSize.sm} color={t.red} />}
								<Text style={styles.forgetActionText}>Forget machine</Text>
							</Pressable>
						) : null}
					</View>
				</Animated.View>
			) : null}

			{editing ? (
				<View testID={`environment-editor-${host.id}`} style={styles.editor}>
					<EditField label="Machine name" testID={`environment-name-${host.id}`} value={machineName} onChangeText={(value) => { setMachineName(value); setError(undefined); }} maxLength={48} editable={!saving} />
					<View style={styles.appearanceField}>
						<Text style={styles.fieldLabel}>Appearance</Text>
						<Pressable
							testID={`environment-appearance-toggle-${host.id}`}
							accessibilityRole="button"
							accessibilityLabel={`Choose appearance for ${machineName || host.name}`}
							accessibilityState={{ expanded: appearanceOpen }}
							disabled={saving}
							onPress={() => { setAppearanceOpen((open) => !open); haptics.tap(); }}
							style={({ pressed }) => [styles.appearanceToggle, pressed && styles.pressed, saving && styles.disabled]}
						>
							<Text style={styles.appearanceToggleLabel}>Worker badge</Text>
							<MachineIdentity name={machineName.trim() || host.name} appearance={appearance} style={styles.appearancePreview} />
							<Feather name={appearanceOpen ? "chevron-up" : "chevron-down"} size={iconSize.sm} color={t.textTertiary} />
						</Pressable>
						{appearanceOpen ? (
							<View style={styles.appearancePicker}>
								<Text style={styles.appearancePickerLabel}>Symbol</Text>
								<View style={styles.appearanceChoices}>
									{machineAppearancePalette.symbols.map((symbol) => (
										<AppearanceChoice
											key={symbol}
											testID={`environment-symbol-${host.id}-${symbol}`}
											label={machineSymbolLabels[symbol]}
											appearance={{ ...appearance, symbol }}
											selected={appearance.symbol === symbol}
											onPress={() => setAppearance((current) => ({ ...current, symbol: symbol as MachineSymbol }))}
										/>
									))}
								</View>
							</View>
						) : null}
					</View>
					{accountManaged ? (
						// Account sync rewrites the route and credential from AO Cloud, so an
						// edit here would silently revert. Name and icon stay phone-local.
						<Text testID={`environment-account-managed-${host.id}`} style={styles.accountManagedNote}>
							This machine's address and password come from your AO Cloud account and update automatically. To connect another way, use Reconnect to scan a new code.
						</Text>
					) : <>
						<EditField label="Host" testID={`environment-host-${host.id}`} value={config.host} onChangeText={set("host")} autoCapitalize="none" autoCorrect={false} keyboardType="url" editable={!saving} />
						<View style={styles.editorPair}>
							<View style={styles.portField}>
								<EditField label="API port" testID={`environment-port-${host.id}`} value={config.httpPort} onChangeText={set("httpPort")} keyboardType="number-pad" editable={!saving} />
							</View>
							<View style={styles.tlsField}>
								<Text style={styles.fieldLabel}>Use TLS</Text>
								<Switch accessibilityLabel="Use TLS" value={Boolean(config.secure)} disabled={saving} onValueChange={(secure) => { setConfig((current) => ({ ...current, secure })); setError(undefined); }} trackColor={{ true: t.green, false: t.borderStrong }} />
							</View>
						</View>
						<View>
							<Text style={styles.fieldLabel}>Password</Text>
							<View style={styles.passwordRow}>
								<TextInput accessibilityLabel="Password" value={config.password} onChangeText={set("password")} autoCapitalize="none" autoCorrect={false} secureTextEntry={!showPassword} textContentType="password" editable={!saving} placeholder="Connection password" placeholderTextColor={t.textFaint} selectionColor={t.accent} style={[styles.input, styles.passwordInput]} />
								<Pressable accessibilityRole="button" accessibilityLabel={showPassword ? "Hide password" : "Show password"} disabled={saving} onPress={() => { haptics.tap(); setShowPassword((visible) => !visible); }} style={({ pressed }) => [styles.passwordToggle, pressed && styles.pressed]}>
									<Feather name={showPassword ? "eye-off" : "eye"} size={iconSize.lg} color={t.textSecondary} />
								</Pressable>
							</View>
						</View>
					</>}
					{!onEditorStateChange ? <EnvironmentEditorActions editor={{ hostId: host.id, canSave, saving, error, onSave: () => void save(), onCancel: cancelEditing }} /> : null}
				</View>
			) : null}
		</View>
		</Swipeable>
		</Animated.View>
	);
}

export function EnvironmentEditorActions({ editor }: { editor: EnvironmentEditorState }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.editorFooter}>
		{editor.error ? <Text accessibilityRole="alert" testID={`environment-error-${editor.hostId}`} style={styles.errorText}>{editor.error}</Text> : null}
		<View style={styles.editorActions}>
			<Pressable accessibilityRole="button" accessibilityLabel="Cancel editing machine" disabled={editor.saving} onPress={editor.onCancel} style={({ pressed }) => [styles.editorButton, styles.cancelButton, pressed && styles.pressed, editor.saving && styles.disabled]}>
				<Text style={styles.cancelText}>Cancel</Text>
			</Pressable>
			<Pressable testID={`environment-save-${editor.hostId}`} accessibilityRole="button" accessibilityLabel="Save machine changes" accessibilityState={{ disabled: !editor.canSave, busy: editor.saving }} disabled={!editor.canSave} onPress={editor.onSave} style={({ pressed }) => [styles.editorButton, styles.saveButton, pressed && styles.pressed, !editor.canSave && styles.disabled]}>
				{editor.saving ? <ActivityIndicator size="small" color={t.onAccent} /> : <Text style={styles.saveText}>Save changes</Text>}
			</Pressable>
		</View>
	</View>;
}

function AppearanceChoice({ testID, label, appearance, selected, onPress }: { testID: string; label: string; appearance: MachineAppearance; selected: boolean; onPress(): void }) {
	const styles = useThemedStyles(makeStyles);
	return (
		<Pressable
			testID={testID}
			accessibilityRole="radio"
			accessibilityLabel={label}
			accessibilityState={{ selected }}
			onPress={() => { haptics.select(); onPress(); }}
			style={({ pressed }) => [styles.appearanceChoice, selected && styles.appearanceChoiceSelected, pressed && styles.pressed]}
		>
			<MachineAvatar appearance={appearance} size={32} />
		</Pressable>
	);
}

function EditField({ label, ...input }: { label: string } & React.ComponentProps<typeof TextInput>) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={styles.field}>
			<Text style={styles.fieldLabel}>{label}</Text>
			<TextInput {...input} accessibilityLabel={input.accessibilityLabel ?? label} placeholderTextColor={t.textFaint} selectionColor={t.accent} style={styles.input} />
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	layoutShell: { marginHorizontal: space.lg, marginBottom: space.sm, borderRadius: radius.lg, borderCurve: "continuous", overflow: "hidden" },
	swipeShell: { borderRadius: radius.lg, borderCurve: "continuous", overflow: "hidden" },
	forgetUnderlay: { position: "absolute", top: 0, bottom: 0, left: 0, right: 0, backgroundColor: t.red },
	forgetRail: { width: 104, alignItems: "center", justifyContent: "center", gap: space.xxs, backgroundColor: t.red },
	forgetRailPressed: { opacity: 0.82 },
	forgetText: { fontFamily: "Geist_600SemiBold", color: "#ffffff", fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
	forgetAction: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.sm, borderTopWidth: rowDividerWidth, borderTopColor: t.borderDefault },
	forgetActionText: { fontFamily: "Geist_600SemiBold", color: t.red, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
	row: { backgroundColor: t.bgElevated, borderWidth: rowDividerWidth, borderColor: t.borderDefault, borderRadius: radius.lg, borderCurve: "continuous", overflow: "hidden" },
	header: { minHeight: 76, flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.md, paddingVertical: space.md },
	machineCopy: { flex: 1, minWidth: 0, gap: space.hair },
	machineNameRow: { flexDirection: "row", alignItems: "center", gap: space.xs },
	machineName: { flexShrink: 1, fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.headline.fontSize, lineHeight: type.headline.lineHeight, fontWeight: "600" },
	routeSummary: { fontFamily: t.fontMono, color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	defaultPill: { overflow: "hidden", fontFamily: "Geist_600SemiBold", color: t.textSecondary, backgroundColor: t.bgSubtle, fontSize: type.caption2.fontSize, lineHeight: 18, fontWeight: "600", paddingHorizontal: space.xs, borderRadius: radius.pill },
	statusPill: { flexDirection: "row", alignItems: "center", gap: space.xs, minHeight: 24 },
	statusDot: { width: space.xs, height: space.xs, borderRadius: radius.pill },
	statusText: { fontFamily: "Geist_600SemiBold", fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, fontWeight: "600" },
	activeRoute: { gap: space.sm, padding: space.md, borderTopWidth: rowDividerWidth, borderTopColor: t.borderDefault },
	activeRouteLabel: { flexDirection: "row", alignItems: "center", gap: space.sm },
	activeRouteTitle: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
	activeAddressSurface: { minHeight: 36, justifyContent: "center", paddingHorizontal: space.sm, backgroundColor: t.bgSubtle, borderRadius: radius.sm, borderCurve: "continuous" },
	activeAddress: { fontFamily: t.fontMono, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	accountManagedNote: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	noRoutes: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, padding: space.md, borderTopWidth: rowDividerWidth, borderTopColor: t.borderDefault },
	otherRoutesSection: { borderTopWidth: rowDividerWidth, borderTopColor: t.borderDefault },
	otherRoutesToggle: { minHeight: 62, flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.md, paddingVertical: space.sm },
	otherRoutesCopy: { flex: 1, gap: space.xxs },
	otherRoutesTitle: { fontFamily: "Geist_500Medium", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "500" },
	otherRoutesCount: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
	otherRoutesList: { borderTopWidth: rowDividerWidth, borderTopColor: t.borderSubtle, paddingHorizontal: space.md },
	otherRouteRow: { minHeight: 48, justifyContent: "center", gap: space.xxs, paddingVertical: space.sm, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle },
	otherRouteLabel: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, fontWeight: "600", textTransform: "uppercase", letterSpacing: 0.4 },
	otherRouteAddress: { fontFamily: t.fontMono, color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	actionArea: { borderTopWidth: rowDividerWidth, borderTopColor: t.borderDefault, backgroundColor: t.bgSubtle },
	defaultAction: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.xs, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderDefault, paddingHorizontal: space.md },
	actions: { minHeight: touchTarget, flexDirection: "row" },
	action: { flex: 1, minHeight: touchTarget, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.xs, paddingHorizontal: space.sm },
	actionDivider: { width: rowDividerWidth, backgroundColor: t.borderDefault },
	actionText: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
	editor: { padding: space.md, gap: space.md, borderTopWidth: rowDividerWidth, borderTopColor: t.borderDefault, backgroundColor: t.bgElevated },
	appearanceField: { gap: space.xxs },
	appearanceToggle: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.md, backgroundColor: t.bgElevated, borderWidth: rowDividerWidth, borderColor: t.borderDefault, borderRadius: radius.sm, borderCurve: "continuous" },
	appearanceToggleLabel: { flex: 1, color: t.textPrimary, fontFamily: "Geist_500Medium", fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "500" },
	appearancePreview: { maxWidth: "52%" },
	appearancePicker: { gap: space.sm, padding: space.md, backgroundColor: t.bgSubtle, borderRadius: radius.md, borderCurve: "continuous" },
	appearancePickerLabel: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, fontWeight: "600", letterSpacing: 0.7, textTransform: "uppercase" },
	appearanceChoices: { flexDirection: "row", flexWrap: "wrap", gap: space.sm },
	appearanceChoice: { width: 44, height: 44, alignItems: "center", justifyContent: "center", borderWidth: rowDividerWidth, borderColor: "transparent", borderRadius: radius.md, borderCurve: "continuous" },
	appearanceChoiceSelected: { borderColor: t.accentBorder, backgroundColor: t.accentTint },
	field: { flex: 1, gap: space.xxs },
	fieldLabel: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, fontWeight: "600", letterSpacing: 0.7, textTransform: "uppercase" },
	input: { minHeight: touchTarget, fontFamily: "Geist_400Regular", backgroundColor: t.bgElevated, borderWidth: rowDividerWidth, borderColor: t.borderDefault, borderRadius: radius.sm, borderCurve: "continuous", paddingHorizontal: space.md, paddingVertical: space.sm, color: t.textPrimary, fontSize: type.subheadline.fontSize },
	editorPair: { flexDirection: "row", alignItems: "flex-end", gap: space.md },
	portField: { flex: 1 },
	tlsField: { minWidth: 108, gap: space.xxs },
	passwordRow: { flexDirection: "row", alignItems: "center", backgroundColor: t.bgElevated, borderWidth: rowDividerWidth, borderColor: t.borderDefault, borderRadius: radius.sm, borderCurve: "continuous" },
	passwordInput: { flex: 1, borderWidth: 0, backgroundColor: "transparent", paddingRight: space.none },
	passwordToggle: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	errorText: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	editorActions: { flexDirection: "row", gap: space.sm, paddingTop: space.xxs },
	editorFooter: { gap: space.xs },
	editorButton: { flex: 1, minHeight: touchTarget, alignItems: "center", justifyContent: "center", paddingHorizontal: space.md, borderWidth: rowDividerWidth, borderRadius: radius.md, borderCurve: "continuous" },
	cancelButton: { backgroundColor: t.bgSubtle, borderColor: t.borderDefault },
	saveButton: { backgroundColor: t.accent, borderColor: t.accent },
	cancelText: { fontFamily: "Geist_600SemiBold", color: t.textSecondary, fontSize: type.subheadline.fontSize, fontWeight: "600" },
	saveText: { fontFamily: "Geist_600SemiBold", color: t.onAccent, fontSize: type.subheadline.fontSize, fontWeight: "600" },
	pressed: { opacity: 0.65 },
	disabled: { opacity: 0.45 },
});
