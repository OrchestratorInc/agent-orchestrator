import { Feather } from "../icons";
import { Host, Slider, Switch as NativeSwitch } from "@expo/ui";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { haptics } from "../haptics";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { SheetHeader } from "../ui";
import type { ChatConfigOption, ChatModel, ConversationSnapshot, TurnSettings } from "./types";
import { approvalLabel, effortChoiceLabel, effortSliderIndex, effortSliderWrite, fastControlEnabled, fastControlValue, followsAgentLabel, nativeModelLabel, NOT_REPORTED, orderedProviderControls, providerChoiceLabel, providerTurnControlKind } from "./turnSettingsModel";
import { can } from "./types";
import { type, space } from "../tokens";

// Labels come from approvalLabel, which names Codex's default differently.
const APPROVALS = [
	{ id: "default", description: "The worktree remains the safety boundary" },
	{ id: "accept-edits", description: "Edits here are allowed; anything else asks" },
	{ id: "auto", description: "The agent requests approval when it needs it" },
	{ id: "bypass-permissions", description: "No approval or sandbox prompts" },
] as const;

/** `tick` is the slider's short label, where "Use agent effort" does not fit. */
type Choice = { value: string; label: string; description?: string; tick?: string };
type OpenChoice = { title: string; value: string; items: Choice[]; onChange(value: string): void } | null;
type Props = {
	snapshot: ConversationSnapshot;
	models: ChatModel[];
	options: ChatConfigOption[];
	disabled?: boolean;
	refreshing?: boolean;
	error?: string;
	onRefresh(): void;
	/** Settles once the route has taken the answer or the error; the effort slider waits for it. */
	onSettings(settings: TurnSettings): Promise<void>;
	onOption(id: string, value: { value: string } | { enabled: boolean }): Promise<void>;
};

export function ChatSettingsSheet({ snapshot, models, options, disabled, refreshing, error, onRefresh, onSettings, onOption }: Props) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [openChoice, setOpenChoice] = useState<OpenChoice>(null);
	const selected = models.find((model) => model.id === snapshot.settings.model) ?? models.find((model) => model.default);
	const usesProviderOptions = can(snapshot, "config_options");
	const providerControls = useMemo(() => orderedProviderControls([...options]), [options]);
	const modelOption = providerControls.find((option) => providerTurnControlKind(option) === "model");
	const effortOption = providerControls.find((option) => providerTurnControlKind(option) === "effort");
	const fastOption = providerControls.find((option) => providerTurnControlKind(option) === "fast");
	const permissionOption = providerControls.find((option) => providerTurnControlKind(option) === "permissions");
	const advancedOptions = providerControls.filter((option) => providerTurnControlKind(option) === "other");
	const modelChoices = modelOption
		? modelOption.choices.map((model) => ({ value: model.value, label: model.name, description: model.description }))
		: models.map((model) => ({ value: model.id, label: model.displayName, description: model.description || undefined }));
	// Nothing reported means nothing selected: the first model or level is not
	// a stand-in for the one the provider did not name (#5834).
	const selectedModel = modelOption?.currentValue ?? selected?.id ?? "";
	const modelValue = modelOption ? providerChoiceLabel(modelOption) : nativeModelLabel(selected, snapshot.settings.model);
	const effortChoices = (effortOption
		? effortOption.choices.map((effort) => ({ value: effort.value, label: capitalize(effort.name) }))
		: (selected?.efforts ?? []).map((effort) => ({ value: effort, label: effortChoiceLabel(effort) }))
	).map((choice) => ({ ...choice, tick: followsAgentLabel(choice.label) ? "Agent" : undefined }));
	const selectedEffort = effortOption?.currentValue ?? snapshot.settings.reasoningEffort ?? selected?.defaultEffort ?? "";
	// What the slider says when the effort is none of its levels; the same text
	// the iOS turn-settings row shows for it.
	const effortValue = effortOption ? providerChoiceLabel(effortOption) : selectedEffort ? effortChoiceLabel(selectedEffort) : NOT_REPORTED;
	const permissionChoices = permissionOption
		? permissionOption.choices.map((choice) => ({ value: choice.value, label: choice.name, description: choice.description }))
		: APPROVALS.map((item) => ({ value: item.id, label: approvalLabel(item.id, snapshot.harness), description: item.description }));
	const selectedPermission = permissionOption?.currentValue ?? snapshot.settings.approvalMode ?? "default";
	const permissionDescription = permissionChoices.find((choice) => choice.value === selectedPermission)?.description;
	const choose = (title: string, value: string, items: Choice[], onChange: (value: string) => void) => {
		if (disabled) return;
		haptics.tap();
		setOpenChoice({ title, value, items, onChange });
	};

	// Choices open as a page inside this sheet, never as a second sheet over it:
	// Android stacked two grabbers and only the top one answered a swipe down.
	if (openChoice) return <ChoicePage choice={openChoice} onBack={() => setOpenChoice(null)} />;

	return <View style={[styles.screen, { backgroundColor: t.bgSurface }]}>
		<View style={styles.header}>
			<SheetHeader title="Turn settings" subtitle="Changes apply to the next message." right={<Pressable accessibilityRole="button" accessibilityLabel="Refresh turn settings" disabled={refreshing} onPress={() => { haptics.tap(); onRefresh(); }} style={styles.refresh}>
				{refreshing ? <ActivityIndicator size="small" color={t.accent} /> : <Feather name="refresh-cw" size={15} color={t.accent} />}
				<Text style={styles.refreshText}>Refresh</Text>
			</Pressable>} />
			{error ? <Notice color={t.red} background={t.tintRed} icon="alert-circle" text={error} /> : null}
			{snapshot.modelReroute ? <Notice color={t.amber} background={t.tintAmber} icon="shuffle" text={`Currently answered by ${snapshot.modelReroute.toModel}`} /> : null}
		</View>

		<ScrollView contentContainerStyle={styles.content} showsVerticalScrollIndicator={false}>
			{fastOption || modelChoices.length || effortChoices.length ? <SettingsGroup title="RESPONSE">
				{fastOption ? <FastModeRow option={fastOption} disabled={disabled} onOption={onOption} /> : null}
				{modelChoices.length ? <SettingRow icon="layers" label="Model" value={modelValue} description="Choose the model for the next message" disabled={disabled} onPress={() => choose("Model", selectedModel, modelChoices, (model) => {
					if (modelOption) onOption(modelOption.id, { value: model });
					else onSettings({ ...snapshot.settings, model, reasoningEffort: undefined });
				})} /> : null}
				{effortChoices.length ? <EffortSlider choices={effortChoices} selected={selectedEffort} unplaced={effortValue} disabled={disabled} onChange={(reasoningEffort) => effortOption
					? onOption(effortOption.id, { value: reasoningEffort })
					: onSettings({ ...snapshot.settings, reasoningEffort })} /> : null}
			</SettingsGroup> : null}

			{permissionChoices.length ? <SettingsGroup title="PERMISSIONS">
				<SettingRow icon="circle-dashed-check" label="Permission mode" value={permissionOption ? providerChoiceLabel(permissionOption) : approvalLabel(snapshot.settings.approvalMode ?? "default", snapshot.harness)} description={permissionDescription} disabled={disabled} onPress={() => choose("Permission mode", selectedPermission, permissionChoices, (value) => {
					if (permissionOption) onOption(permissionOption.id, { value });
					else onSettings({ ...snapshot.settings, approvalMode: value as TurnSettings["approvalMode"] });
				})} />
			</SettingsGroup> : null}

			{advancedOptions.length ? <SettingsGroup title="ADVANCED">
				{advancedOptions.map((option) => option.type === "boolean"
					? <ToggleRow key={option.id} label={option.name} description={option.description} value={Boolean(option.currentBoolean)} disabled={disabled} onChange={(enabled) => onOption(option.id, { enabled })} />
					: <SettingRow key={option.id} icon="sliders" label={option.name} value={providerChoiceLabel(option)} description={option.description} disabled={disabled} onPress={() => choose(option.name, option.currentValue ?? option.choices[0]?.value ?? "", option.choices.map((choice) => ({ value: choice.value, label: choice.groupName || choice.group ? `${choice.groupName || choice.group} · ${choice.name}` : choice.name, description: choice.description })), (value) => onOption(option.id, { value }))} />,
				)}
			</SettingsGroup> : null}

			{usesProviderOptions && options.length === 0 && models.length === 0 ? <Text style={styles.empty}>No turn controls are available for this provider.</Text> : null}
		</ScrollView>
	</View>;
}

function SettingsGroup({ title, children }: { title: string; children: ReactNode }) {
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.section}><Text style={styles.sectionTitle}>{title}</Text><View style={styles.group}>{children}</View></View>;
}

function SettingRow({ icon, label, value, description, disabled, onPress }: { icon?: keyof typeof Feather.glyphMap; label: string; value: string; description?: string; disabled?: boolean; onPress(): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <Pressable accessibilityRole="button" accessibilityState={{ disabled }} disabled={disabled} onPress={onPress} style={({ pressed }) => [styles.row, pressed && styles.rowPressed, disabled && styles.disabled]}>
		<View style={styles.rowIcon}>{icon ? <Feather name={icon} size={17} color={t.textSecondary} /> : null}</View>
		<View style={styles.rowCopy}><Text numberOfLines={1} style={styles.rowLabel}>{label}</Text>{description ? <Text numberOfLines={2} style={styles.rowDescription}>{description}</Text> : null}</View>
		<Text numberOfLines={1} style={styles.rowValue}>{value}</Text>
		<Feather name="chevron-right" size={17} color={t.textFaint} />
	</Pressable>;
}

function FastModeRow({ option, disabled, onOption }: { option: ChatConfigOption; disabled?: boolean; onOption(id: string, value: { value: string } | { enabled: boolean }): void }) {
	const enabled = fastControlEnabled(option);
	const selectValue = fastControlValue(option, !enabled);
	const toggleDisabled = disabled || (option.type === "select" && !selectValue);
	return <ToggleRow label={option.name} description={option.description || "Faster responses on supported models"} value={enabled} disabled={toggleDisabled} onChange={(next) => {
		if (option.type === "boolean") onOption(option.id, { enabled: next });
		else {
			const value = fastControlValue(option, next);
			if (value) onOption(option.id, { value });
		}
	}} />;
}

function ToggleRow({ label, description, value, disabled, onChange }: { label: string; description?: string; value: boolean; disabled?: boolean; onChange(value: boolean): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { scheme } = useThemeState();
	return <View style={[styles.row, disabled && styles.disabled]}><Feather name="zap" size={17} color={t.textSecondary} /><View style={styles.rowCopy}><Text style={styles.rowLabel}>{label}</Text>{description ? <Text numberOfLines={2} style={styles.rowDescription}>{description}</Text> : null}</View><Host style={styles.switchHost} colorScheme={scheme} seedColor={t.accent}><NativeSwitch value={value} disabled={disabled} onValueChange={(next) => { haptics.select(); onChange(next); }} /></Host></View>;
}

/** A level the user moved to that the sheet has not confirmed; `write` tells a late answer from the current one. */
type EffortDraft = { index: number; write: number };

export function EffortSlider({ choices, selected, unplaced, disabled, onChange }: { choices: Choice[]; selected: string; unplaced: string; disabled?: boolean; onChange(value: string): Promise<void> }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { scheme } = useThemeState();
	// -1 while the current effort is none of these levels. The thumb still needs
	// a place, but nothing is written until it moves. Clamping it to the first
	// level used to save that level about 180 ms after the sheet opened, a
	// change nobody made.
	const selectedIndex = effortSliderIndex(choices, selected);
	// The slider shows the confirmed selection, except for a move that is still
	// waiting to be sent or answered. A rejected write leaves `selected` where it
	// was, so dropping the draft when the write settles is what puts the slider
	// back on the saved level.
	const [draft, setDraft] = useState<EffortDraft | null>(null);
	const index = draft?.index ?? selectedIndex;
	const writes = useRef(0);
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const onChangeRef = useRef(onChange);
	const clearPending = useCallback(() => {
		if (timer.current !== null) clearTimeout(timer.current);
		timer.current = null;
	}, []);

	useEffect(() => { onChangeRef.current = onChange; }, [onChange]);
	useEffect(() => {
		setDraft(null);
		clearPending();
	}, [clearPending, selected, selectedIndex]);
	// A move not sent yet is dropped when the sheet disables the slider (a
	// refresh), so it must not stay on screen. A write already sent keeps its
	// draft until it settles.
	useEffect(() => {
		if (!disabled || timer.current === null) return;
		clearPending();
		setDraft(null);
	}, [clearPending, disabled]);
	useEffect(() => clearPending, [clearPending]);
	// Where the user left the native thumb, while that may differ from the level
	// shown. @expo/ui's Compose slider ignores a new value while a finger is on it
	// and does not read it again when the drag ends (SliderView.kt), so a write
	// rejected under a held finger would leave the thumb on the rejected level.
	// When a draft is dropped for another level, the native slider is rebuilt.
	const thumb = useRef<number | null>(null);
	const [nativeKey, setNativeKey] = useState(0);
	useEffect(() => {
		if (draft !== null || thumb.current === null) return;
		if (thumb.current !== Math.max(0, index)) setNativeKey((key) => key + 1);
		thumb.current = null;
	}, [draft, index]);
	// Only a slider move arms the timer. A failed write rerenders the sheet with
	// the old selected value, but must not silently retry the same write. A
	// disabled Slider drops onValueChange, so a move never arrives while disabled.
	const move = (value: number) => {
		const nextIndex = Math.round(value);
		clearPending();
		const next = effortSliderWrite(choices, selected, nextIndex);
		if (!next) {
			thumb.current = null;
			setDraft(null);
			return;
		}
		const write = ++writes.current;
		thumb.current = nextIndex;
		setDraft({ index: nextIndex, write });
		timer.current = setTimeout(() => {
			timer.current = null;
			haptics.select();
			const settle = () => setDraft((current) => current?.write === write ? null : current);
			onChangeRef.current(next).then(settle, settle);
		}, 180);
	};

	return <View style={[styles.effort, disabled && styles.disabled]}>
		<View style={styles.effortHeader}><Feather name="activity" size={17} color={t.textSecondary} /><View style={styles.rowCopy}><Text style={styles.rowLabel}>Reasoning effort</Text><Text style={styles.rowDescription}>More effort can improve harder tasks</Text></View><Text style={styles.effortValue} testID="turn-settings-effort-value">{index < 0 ? unplaced : choices[index]?.label}</Text></View>
		<Host key={nativeKey} style={styles.sliderHost} colorScheme={scheme} seedColor={t.accent}><Slider value={Math.max(0, index)} min={0} max={Math.max(0, choices.length - 1)} step={1} disabled={disabled} onValueChange={move} testID="turn-settings-effort" /></Host>
		<View style={styles.effortLabels}>{choices.map((choice, choiceIndex) => <Text key={choice.value} style={[styles.effortLabel, choiceIndex === index && { color: t.accent }]}>{choice.tick ?? choice.label}</Text>)}</View>
	</View>;
}

function ChoicePage({ choice, onBack }: { choice: NonNullable<OpenChoice>; onBack(): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <View style={[styles.screen, { backgroundColor: t.bgSurface }]}>
		<Pressable accessibilityRole="button" accessibilityLabel={`Back to turn settings`} onPress={() => { haptics.tap(); onBack(); }} style={({ pressed }) => [styles.choiceBack, pressed && styles.rowPressed]}>
			<Feather name="chevron-left" size={20} color={t.textSecondary} />
			<Text style={styles.choiceTitle}>{choice.title}</Text>
		</Pressable>
		<ScrollView contentContainerStyle={styles.choiceList} showsVerticalScrollIndicator={false}><View style={styles.choiceCard}>{choice.items.map((item, index) => {
			const selected = item.value === choice.value;
			return <Pressable key={item.value} accessibilityRole="button" accessibilityState={{ selected }} onPress={() => { haptics.select(); choice.onChange(item.value); onBack(); }} style={[styles.choiceRow, index > 0 && styles.choiceDivider, selected && styles.choiceSelected]}>
				<View style={styles.choiceCopy}><Text style={[styles.choiceLabel, selected && styles.choiceLabelSelected]}>{item.label}</Text>{item.description ? <Text style={styles.choiceDescription}>{item.description}</Text> : null}</View>
				{selected ? <Feather name="check" size={20} color={t.accent} style={styles.choiceCheck} /> : null}
			</Pressable>;
		})}</View></ScrollView>
	</View>;
}

function Notice({ color, background, icon, text }: { color: string; background: string; icon: keyof typeof Feather.glyphMap; text: string }) {
	const styles = useThemedStyles(makeStyles);
	return <View accessibilityRole="alert" style={[styles.notice, { backgroundColor: background }]}><Feather name={icon} size={15} color={color} /><Text style={[styles.noticeText, { color }]}>{text}</Text></View>;
}

function capitalize(value: string): string { return value ? value[0].toUpperCase() + value.slice(1) : value; }

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1 },
	header: { paddingHorizontal: space.lg, paddingTop: space.md, gap: space.sm },
	refresh: { minHeight: 36, flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.xxs },
	refreshText: { fontFamily: "Geist_600SemiBold", color: t.accent, fontSize: type.footnote.fontSize, fontWeight: "600" },
	notice: { minHeight: 38, flexDirection: "row", alignItems: "center", gap: space.sm, borderRadius: 12, paddingHorizontal: space.md, paddingVertical: space.sm },
	noticeText: { fontFamily: "Geist_400Regular", flex: 1, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
	content: { paddingHorizontal: space.lg, paddingTop: space.md, paddingBottom: space.xxl, gap: space.lg },
	section: { gap: space.xs },
	sectionTitle: { fontFamily: "Geist_600SemiBold", paddingHorizontal: space.xxs, color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, letterSpacing: 1.05, fontWeight: "600" },
	group: { overflow: "hidden", borderRadius: 16, borderCurve: "continuous", backgroundColor: t.bgElevated },
	row: { minHeight: 58, flexDirection: "row", alignItems: "center", gap: space.md, paddingHorizontal: space.md, paddingVertical: space.sm },
	rowIcon: { width: 17 },
	rowPressed: { backgroundColor: t.bgElevatedHover },
	disabled: { opacity: 0.45 },
	rowCopy: { flex: 1, minWidth: 0, gap: space.none },
	rowLabel: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
	rowDescription: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	rowValue: { fontFamily: "Geist_400Regular", maxWidth: "42%", color: t.textSecondary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	switchHost: { width: 54, height: 34 },
	effort: { paddingHorizontal: space.md, paddingTop: space.md, paddingBottom: space.sm, gap: space.hair },
	// Match the standard settings row's icon-to-copy spacing so the Model and
	// Reasoning effort labels share the same text column.
	effortHeader: { flexDirection: "row", alignItems: "center", gap: space.md },
	effortValue: { fontFamily: "Geist_600SemiBold", color: t.accent, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "600" },
	// Expo UI maps this Host to Compose on Android, where percentage widths are
	// not valid native layout values. Let the parent stretch it instead.
	sliderHost: { height: 36, alignSelf: "stretch" },
	effortLabels: { flexDirection: "row", justifyContent: "space-between", gap: space.xxs },
	effortLabel: { fontFamily: "Geist_400Regular", flex: 1, color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight, textAlign: "center" },
	empty: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingHorizontal: space.xxs },
	choiceBack: { minHeight: 52, flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.md },
	choiceTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title3.fontSize, lineHeight: type.title3.lineHeight, fontWeight: "600" },
	choiceList: { paddingHorizontal: space.lg, paddingBottom: space.xl },
	choiceCard: { borderRadius: 16, overflow: "hidden", backgroundColor: t.bgElevated },
	choiceRow: { minHeight: 52, flexDirection: "row", alignItems: "flex-start", gap: space.sm, paddingHorizontal: space.lg, paddingVertical: space.sm },
	choiceDivider: { borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle },
	choiceSelected: { backgroundColor: t.accentTint },
	choiceCopy: { flex: 1, minWidth: 0, gap: space.none },
	choiceLabel: { fontFamily: "Geist_400Regular", color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	choiceDescription: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	choiceCheck: { alignSelf: "center" },
	choiceLabelSelected: { fontFamily: "Geist_600SemiBold", color: t.accent, fontWeight: "600" },
});
