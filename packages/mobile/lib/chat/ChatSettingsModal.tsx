import { Column, FieldGroup, Host, Slider, Switch } from "@expo/ui";
import { Column as ComposeColumn, DropdownMenu, DropdownMenuItem, RadioButton, Row as ComposeRow, Text as ComposeText } from "@expo/ui/jetpack-compose";
import { alpha, clickable, fillMaxWidth, testID } from "@expo/ui/jetpack-compose/modifiers";
import { Picker as SwiftUIPicker, Text as SwiftUIText, VStack } from "@expo/ui/swift-ui";
import { disabled as disabledModifier, font, foregroundStyle, labelsHidden, pickerStyle, refreshable, scrollContentBackground, tag } from "@expo/ui/swift-ui/modifiers";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Platform, RefreshControl, ScrollView, StyleSheet, Text, View } from "react-native";
import { AppSymbol } from "../AppSymbol";
import { haptics } from "../haptics";
import { refreshControlColors } from "../refreshControlColors";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { SheetHeader } from "../ui";
import type { ChatConfigOption, ChatModel, ConversationSnapshot, TurnSettings } from "./types";
import { can } from "./types";
import { approvalLabel, effortChoiceLabel, effortSliderIndex, effortSliderWrite, fastControlEnabled, fastControlValue, followsAgentLabel, nativeModelLabel, NOT_REPORTED, orderedProviderControls, providerChoiceLabel, providerTurnControlKind } from "./turnSettingsModel";
import { iconSize, radius, space, stateOpacity, type } from "../tokens";

// Labels come from approvalLabel, which names Codex's default differently.
const APPROVALS = [
	{ id: "default", description: "The worktree remains the safety boundary" },
	{ id: "accept-edits", description: "Edits here are allowed; anything else asks" },
	{ id: "auto", description: "The agent requests approval when it needs it" },
	{ id: "bypass-permissions", description: "No approval or sandbox prompts" },
] as const;

/** `tick` is the slider's short label, where "Use agent effort" does not fit. */
type Choice = { value: string; label: string; description?: string; tick?: string };
type Props = {
	snapshot: ConversationSnapshot;
	models: ChatModel[];
	options: ChatConfigOption[];
	disabled?: boolean;
	refreshing?: boolean;
	error?: string;
	/** Settles once the catalog has been fetched again; iOS holds its spinner until then. */
	onRefresh(): Promise<void>;
	/** Settles once the route has taken the answer or the error; the effort slider waits for it. */
	onSettings(settings: TurnSettings): Promise<void>;
	onOption(id: string, value: { value: string } | { enabled: boolean }): Promise<void>;
};

/**
 * The turn-settings sheet as the platform's own form: a SwiftUI Form of inline
 * pickers and toggles on iOS, and Material's grouped list with menus, switches
 * and the effort slider on Android. Pull down to fetch the catalog again.
 */
export function ChatSettingsSheet({ snapshot, models, options, disabled, refreshing, error, onRefresh, onSettings, onOption }: Props) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { scheme } = useThemeState();
	const ios = Platform.OS === "ios";
	const selected = models.find((model) => model.id === snapshot.settings.model) ?? models.find((model) => model.default);
	const usesProviderOptions = can(snapshot, "config_options");
	const providerControls = useMemo(() => orderedProviderControls([...options]), [options]);
	const modelOption = providerControls.find((option) => providerTurnControlKind(option) === "model");
	const effortOption = providerControls.find((option) => providerTurnControlKind(option) === "effort");
	const fastOption = providerControls.find((option) => providerTurnControlKind(option) === "fast");
	const permissionOption = providerControls.find((option) => providerTurnControlKind(option) === "permissions");
	const advancedOptions = providerControls.filter((option) => providerTurnControlKind(option) === "other");
	const modelChoices: Choice[] = modelOption
		? modelOption.choices.map((model) => ({ value: model.value, label: model.name, description: model.description }))
		: models.map((model) => ({ value: model.id, label: model.displayName, description: model.description || undefined }));
	// Nothing reported means nothing selected: the first model or level is not
	// a stand-in for the one the provider did not name (#5834).
	const selectedModel = modelOption?.currentValue ?? selected?.id ?? "";
	const modelValue = modelOption ? providerChoiceLabel(modelOption) : nativeModelLabel(selected, snapshot.settings.model);
	const effortChoices: Choice[] = (effortOption
		? effortOption.choices.map((effort) => ({ value: effort.value, label: capitalize(effort.name) }))
		: (selected?.efforts ?? []).map((effort) => ({ value: effort, label: effortChoiceLabel(effort) }))
	).map((choice) => ({ ...choice, tick: followsAgentLabel(choice.label) ? "Agent" : undefined }));
	const selectedEffort = effortOption?.currentValue ?? snapshot.settings.reasoningEffort ?? selected?.defaultEffort ?? "";
	// What the effort control says when the effort is none of its levels; the
	// same text the turn-settings menu shows for it.
	const effortValue = effortOption ? providerChoiceLabel(effortOption) : selectedEffort ? effortChoiceLabel(selectedEffort) : NOT_REPORTED;
	const permissionChoices: Choice[] = permissionOption
		? permissionOption.choices.map((choice) => ({ value: choice.value, label: choice.name, description: choice.description }))
		: APPROVALS.map((item) => ({ value: item.id, label: approvalLabel(item.id, snapshot.harness), description: item.description }));
	const selectedPermission = permissionOption?.currentValue ?? snapshot.settings.approvalMode ?? "default";
	const permissionValue = permissionOption ? providerChoiceLabel(permissionOption) : approvalLabel(snapshot.settings.approvalMode ?? "default", snapshot.harness);
	// iOS lists each choice with its description; an Android menu shows names
	// only, so the current choice's description goes in the section's footer.
	const modelHint = ios ? undefined : hintFor(modelChoices, selectedModel) ?? "Choose the model for the next message";
	const permissionHint = ios ? undefined : hintFor(permissionChoices, selectedPermission);
	const empty = usesProviderOptions && options.length === 0 && models.length === 0;

	// A SwiftUI picker keeps the row it was tapped to even when the save fails
	// and the setting stays put. The route disables the sheet while it saves, so
	// each time it comes back the pickers are rebuilt on what was actually saved.
	const [revision, setRevision] = useState(0);
	const wasDisabled = useRef(disabled);
	useEffect(() => {
		if (wasDisabled.current && !disabled) setRevision((value) => value + 1);
		wasDisabled.current = disabled;
	}, [disabled]);

	const sections = <>
		{fastOption ? <FieldGroup.Section>
			<FastModeSwitch option={fastOption} disabled={disabled} onOption={onOption} />
			<FieldGroup.SectionFooter><Hint>{fastOption.description || "Faster responses on supported models"}</Hint></FieldGroup.SectionFooter>
		</FieldGroup.Section> : null}

		{modelChoices.length ? <FieldGroup.Section title="Model">
			<ChoicePicker key={`model:${revision}`} label="Model" value={modelValue} choices={modelChoices} selected={selectedModel} disabled={disabled} onChange={(model) => {
				if (modelOption) void onOption(modelOption.id, { value: model });
				else void onSettings({ ...snapshot.settings, model, reasoningEffort: undefined });
			}} />
			{modelHint ? <FieldGroup.SectionFooter><Hint>{modelHint}</Hint></FieldGroup.SectionFooter> : null}
		</FieldGroup.Section> : null}

		{effortChoices.length ? <FieldGroup.Section title="Reasoning effort">
			{ios
				? <ChoicePicker key={`effort:${revision}`} label="Reasoning effort" value={effortValue} choices={effortChoices} selected={selectedEffort} disabled={disabled} onChange={(reasoningEffort) => {
					if (effortOption) void onOption(effortOption.id, { value: reasoningEffort });
					else void onSettings({ ...snapshot.settings, reasoningEffort });
				}} />
				: <EffortSlider choices={effortChoices} selected={selectedEffort} unplaced={effortValue} disabled={disabled} onChange={(reasoningEffort) => effortOption
					? onOption(effortOption.id, { value: reasoningEffort })
					: onSettings({ ...snapshot.settings, reasoningEffort })} />}
			<FieldGroup.SectionFooter><Hint>More effort can improve harder tasks</Hint></FieldGroup.SectionFooter>
		</FieldGroup.Section> : null}

		{permissionChoices.length ? <FieldGroup.Section title="Permissions">
			<ChoicePicker key={`permissions:${revision}`} label="Permission mode" value={permissionValue} choices={permissionChoices} selected={selectedPermission} disabled={disabled} onChange={(value) => {
				if (permissionOption) void onOption(permissionOption.id, { value });
				else void onSettings({ ...snapshot.settings, approvalMode: value as TurnSettings["approvalMode"] });
			}} />
			{permissionHint ? <FieldGroup.SectionFooter><Hint>{permissionHint}</Hint></FieldGroup.SectionFooter> : null}
		</FieldGroup.Section> : null}

		{advancedOptions.map((option) => <FieldGroup.Section key={option.id} title={option.type === "boolean" ? undefined : option.name}>
			{option.type === "boolean"
				? <Switch label={option.name} value={Boolean(option.currentBoolean)} disabled={disabled} onValueChange={(enabled) => { haptics.select(); void onOption(option.id, { enabled }); }} />
				: <ChoicePicker key={`${option.id}:${revision}`} label={option.name} value={providerChoiceLabel(option)} choices={groupedChoices(option)} selected={option.currentValue ?? ""} disabled={disabled} onChange={(value) => void onOption(option.id, { value })} />}
			{option.description ? <FieldGroup.SectionFooter><Hint>{option.description}</Hint></FieldGroup.SectionFooter> : null}
		</FieldGroup.Section>)}

		{empty ? <FieldGroup.Section>
			<Hint>No turn controls are available for this provider.</Hint>
		</FieldGroup.Section> : null}
	</>;

	return <View style={styles.screen}>
		<View style={styles.header}>
			<SheetHeader title="Turn settings" subtitle="Changes apply to the next message." />
			{error ? <Notice color={t.red} background={t.tintRed} icon="error" text={error} /> : null}
			{snapshot.modelReroute ? <Notice color={t.amber} background={t.tintAmber} icon="warning" text={`Currently answered by ${snapshot.modelReroute.toModel}`} detail={rerouteDetail(snapshot.modelReroute)} /> : null}
		</View>
		{ios ? (
			<Host style={styles.form} colorScheme={scheme}>
				<FieldGroup modifiers={[scrollContentBackground("hidden"), refreshable(onRefresh)]}>{sections}</FieldGroup>
			</Host>
		) : (
			// Material's sheet hands a drag to the scroll view it can find, and a
			// Compose list is not one, so the rows sit in a React Native scroll view.
			<ScrollView
				nestedScrollEnabled
				contentContainerStyle={styles.content}
				refreshControl={<RefreshControl refreshing={Boolean(refreshing)} onRefresh={() => void onRefresh()} {...refreshControlColors(t)} />}
			>
				<Host matchContents={{ vertical: true }} style={styles.sections} colorScheme={scheme}>
					<Column spacing={space.xxl}>{sections}</Column>
				</Host>
			</ScrollView>
		)}
	</View>;
}

/**
 * One choice among several. iOS lists them inline in the form, each with its
 * description, and ticks the current one; Android shows the current value in
 * the row and the choices in a menu, the current one's radio filled.
 */
function ChoicePicker({ label, value, choices, selected, disabled, onChange }: {
	label: string;
	/** The current value by name, for the Android row, including one no choice matches. */
	value: string;
	choices: Choice[];
	selected: string;
	disabled?: boolean;
	onChange(value: string): void;
}) {
	const t = useTheme();
	const [expanded, setExpanded] = useState(false);
	const choose = (next: string) => {
		setExpanded(false);
		if (next === selected) return;
		haptics.select();
		onChange(next);
	};
	if (Platform.OS === "ios") {
		return (
			<SwiftUIPicker
				label={label}
				selection={selected || null}
				onSelectionChange={(next: string | null) => { if (next !== null) choose(next); }}
				modifiers={[pickerStyle("inline"), labelsHidden(), disabledModifier(Boolean(disabled))]}
			>
				{choices.map((choice) => (
					<VStack key={choice.value} alignment="leading" spacing={space.hair} modifiers={[tag(choice.value)]}>
						<SwiftUIText>{choice.label}</SwiftUIText>
						{choice.description ? <SwiftUIText modifiers={[font({ textStyle: "footnote" }), foregroundStyle({ type: "hierarchical", style: "secondary" })]}>{choice.description}</SwiftUIText> : null}
					</VStack>
				))}
			</SwiftUIPicker>
		);
	}
	return (
		<DropdownMenu expanded={expanded} onDismissRequest={() => setExpanded(false)}>
			<DropdownMenu.Trigger>
				<ComposeColumn modifiers={[fillMaxWidth(), ...(disabled ? [alpha(stateOpacity.disabled)] : [clickable(() => setExpanded(true))])]}>
					<ComposeText style={{ typography: "bodyLarge" }}>{label}</ComposeText>
					<ComposeText color={t.textSecondary} style={{ typography: "bodyMedium" }}>{value}</ComposeText>
				</ComposeColumn>
			</DropdownMenu.Trigger>
			<DropdownMenu.Items>
				{choices.map((choice) => (
					<DropdownMenuItem key={choice.value} onClick={() => choose(choice.value)}>
						<DropdownMenuItem.LeadingIcon><RadioButton selected={choice.value === selected} /></DropdownMenuItem.LeadingIcon>
						<DropdownMenuItem.Text><ComposeText>{choice.label}</ComposeText></DropdownMenuItem.Text>
					</DropdownMenuItem>
				))}
			</DropdownMenu.Items>
		</DropdownMenu>
	);
}

function FastModeSwitch({ option, disabled, onOption }: { option: ChatConfigOption; disabled?: boolean; onOption(id: string, value: { value: string } | { enabled: boolean }): void }) {
	const enabled = fastControlEnabled(option);
	const selectValue = fastControlValue(option, !enabled);
	return <Switch label={option.name} value={enabled} disabled={disabled || (option.type === "select" && !selectValue)} onValueChange={(next) => {
		haptics.select();
		if (option.type === "boolean") onOption(option.id, { enabled: next });
		else {
			const value = fastControlValue(option, next);
			if (value) onOption(option.id, { value });
		}
	}} />;
}

/** A footer's text. A Form styles its own on iOS; Compose's takes Material's supporting text. */
function Hint({ children }: { children: string }) {
	const t = useTheme();
	if (Platform.OS === "ios") return <SwiftUIText>{children}</SwiftUIText>;
	return <ComposeText color={t.textSecondary} style={{ typography: "bodyMedium" }}>{children}</ComposeText>;
}

/** A level the user moved to that the sheet has not confirmed; `write` tells a late answer from the current one. */
type EffortDraft = { index: number; write: number };

/** Android's effort control: Material's stepped slider, with the level named above it and each step below. */
export function EffortSlider({ choices, selected, unplaced, disabled, onChange }: { choices: Choice[]; selected: string; unplaced: string; disabled?: boolean; onChange(value: string): Promise<void> }) {
	const t = useTheme();
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

	return <ComposeColumn verticalArrangement={{ spacedBy: space.xxs }} modifiers={[fillMaxWidth(), ...(disabled ? [alpha(stateOpacity.disabled)] : [])]}>
		<ComposeText color={t.accent} style={{ typography: "labelLarge" }} modifiers={[testID("turn-settings-effort-value")]}>{index < 0 ? unplaced : choices[index]?.label}</ComposeText>
		<Slider key={nativeKey} value={Math.max(0, index)} min={0} max={Math.max(0, choices.length - 1)} step={1} disabled={disabled} onValueChange={move} testID="turn-settings-effort" />
		<ComposeRow horizontalArrangement="spaceBetween" modifiers={[fillMaxWidth()]}>
			{choices.map((choice, choiceIndex) => <ComposeText key={choice.value} color={choiceIndex === index ? t.accent : t.textSecondary} style={{ typography: "bodyMedium" }}>{choice.tick ?? choice.label}</ComposeText>)}
		</ComposeRow>
	</ComposeColumn>;
}

/** `detail` is a second, quieter line; the notice is read as one element. */
function Notice({ color, background, icon, text, detail }: { color: string; background: string; icon: "error" | "warning"; text: string; detail?: string }) {
	const styles = useThemedStyles(makeStyles);
	return <View accessible accessibilityRole="alert" style={[styles.notice, { backgroundColor: background }]}>
		<AppSymbol name={icon} size={iconSize.sm} color={color} />
		<View style={styles.noticeCopy}>
			<Text style={[styles.noticeText, { color }]}>{text}</Text>
			{detail ? <Text style={styles.noticeDetail}>{detail}</Text> : null}
		</View>
	</View>;
}

/** Which model was asked for and why the provider answered with another. */
function rerouteDetail(reroute: NonNullable<ConversationSnapshot["modelReroute"]>): string {
	const requested = reroute.fromModel ? `${reroute.fromModel} was requested. ` : "";
	return `${requested}${reroute.reason || "The provider selected a fallback model for this conversation."}`;
}

/** The description of the current choice. */
function hintFor(choices: Choice[], selected: string): string | undefined {
	return choices.find((choice) => choice.value === selected)?.description;
}

/** A provider's choices, each named with its group when it has one. */
function groupedChoices(option: ChatConfigOption): Choice[] {
	return option.choices.map((choice) => {
		const group = choice.groupName || choice.group;
		return { value: choice.value, label: group ? `${group} · ${choice.name}` : choice.name, description: choice.description };
	});
}

function capitalize(value: string): string { return value ? value[0].toUpperCase() + value.slice(1) : value; }

const makeStyles = (t: Theme) => StyleSheet.create({
	// iOS sets the form's rows on the grouped background they are drawn for.
	screen: { flex: 1, backgroundColor: Platform.OS === "ios" ? t.bgBase : t.bgSurface },
	header: { paddingHorizontal: space.lg, paddingTop: space.md, gap: space.sm },
	form: { flex: 1 },
	content: { padding: space.lg, paddingBottom: space.xxl },
	// Stretched by the scroll view, not given a percentage width, which a
	// Compose host cannot take as a layout value.
	sections: { alignSelf: "stretch" },
	notice: { minHeight: 38, flexDirection: "row", alignItems: "center", gap: space.sm, borderRadius: radius.md, borderCurve: "continuous", paddingHorizontal: space.md, paddingVertical: space.sm },
	noticeCopy: { flex: 1, gap: space.hair },
	noticeText: { fontFamily: type.footnote.fontFamily, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	noticeDetail: { fontFamily: type.footnote.fontFamily, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});
