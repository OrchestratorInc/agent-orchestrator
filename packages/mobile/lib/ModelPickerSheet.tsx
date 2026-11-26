import { FieldGroup, Host } from "@expo/ui";
import { Button as ComposeButton, Column as ComposeColumn, OutlinedTextField, Row as ComposeRow, Text as ComposeText, useNativeState } from "@expo/ui/jetpack-compose";
import { fillMaxWidth, weight } from "@expo/ui/jetpack-compose/modifiers";
import { Button as SwiftUIButton, HStack, TextField } from "@expo/ui/swift-ui";
import { autocorrectionDisabled, buttonStyle, disabled as disabledModifier, onSubmit, submitLabel, textInputAutocapitalization, tint } from "@expo/ui/swift-ui/modifiers";
import { useState } from "react";
import { Platform, StyleSheet, Text } from "react-native";
import type { AgentModelCatalog } from "./api";
import { haptics } from "./haptics";
import { ComposerSearchField } from "./chat/ComposerSearchField";
import { NativePickerList, type PickerOption } from "./NativePickerList";
import { Spinner } from "./Spinner";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles, useThemeState } from "./ThemeProvider";
import { SheetHeader } from "./ui";
import { space, type } from "./tokens";

export function ModelPickerSheet({ catalog, selected, loading, refreshing, error, onClose, onSelect, onRefresh }: {
	catalog?: AgentModelCatalog;
	selected: string;
	loading?: boolean;
	refreshing?: boolean;
	error?: string;
	onClose(): void;
	onSelect(value: string): void;
	/** Settles once the catalog has been fetched again. */
	onRefresh(): Promise<void>;
}) {
	const styles = useThemedStyles(makeStyles);
	const [query, setQuery] = useState("");
	const choose = (value: string) => { haptics.select(); onClose(); onSelect(value); };
	const models = catalog?.models ?? [];
	const needle = query.trim().toLowerCase();
	const matches = needle ? models.filter((model) => model.label.toLowerCase().includes(needle) || model.id.toLowerCase().includes(needle)) : models;
	const options: PickerOption[] = [
		// Not "Auto", which reads as the approval mode.
		...(needle ? [] : [{ id: "", title: "Default model", detail: models.find((model) => model.isDefault)?.label ?? "The agent's own default" }]),
		// The list is built row by row, and some catalogs hold thousands, so it
		// shows the first matches and the search narrows the rest.
		...matches.slice(0, MAX_ROWS).map((model) => ({ id: model.id, title: model.label })),
	];
	const hidden = matches.length - Math.min(matches.length, MAX_ROWS);
	const notice = error || catalog?.warning;
	const custom = catalog && (catalog.selectionMode === "text" || catalog.allowCustom)
		? <CustomModel initial={selected && !catalog.models.some((item) => item.id === selected) ? selected : ""} onUse={choose} />
		: null;
	return (
		<NativePickerList
			header={<>
				{/* iOS titles the sheet in its native bar. */}
				<SheetHeader title={Platform.OS === "ios" ? undefined : "Model"} subtitle="Choose how this agent runs the task." />
				{notice ? <Text accessibilityRole="alert" style={styles.notice}>{notice}</Text> : null}
				{loading ? <Spinner size="large" accessibilityLabel="Loading models" style={styles.loading} /> : null}
				{models.length > SEARCH_FROM ? <ComposerSearchField initialQuery="" placeholder={`Search ${models.length.toLocaleString()} models`} onQuery={setQuery} /> : null}
				{hidden > 0 ? <Text style={styles.more}>{`${hidden.toLocaleString()} more. Search to narrow them.`}</Text> : null}
			</>}
			empty={needle ? `No models match “${query.trim()}”.` : undefined}
			options={options}
			selected={selected}
			onSelect={choose}
			refreshing={Boolean(refreshing)}
			onRefresh={onRefresh}
			footer={custom}
		/>
	);
}

/** A model the catalog does not list, typed by its identifier. */
function CustomModel({ initial, onUse }: { initial: string; onUse(value: string): void }) {
	const t = useTheme();
	const { scheme } = useThemeState();
	// The field keeps its own text natively (one state type serves SwiftUI and
	// Compose); this copy enables Use.
	const field = useNativeState(initial);
	const [value, setValue] = useState(initial);
	const use = () => { if (value.trim()) onUse(value.trim()); };
	if (Platform.OS === "ios") {
		return (
			<FieldGroup.Section title="Custom model">
				<HStack spacing={space.md}>
					<TextField
						text={field}
						placeholder="Model identifier"
						onTextChange={setValue}
						modifiers={[textInputAutocapitalization("never"), autocorrectionDisabled(), submitLabel("done"), onSubmit(use), tint(t.highlight)]}
					/>
					{/* Borderless, or a Form row hands every tap in it to the button. */}
					<SwiftUIButton label="Use" onPress={use} modifiers={[buttonStyle("borderless"), disabledModifier(!value.trim())]} />
				</HStack>
			</FieldGroup.Section>
		);
	}
	return (
		<Host matchContents={{ vertical: true }} style={{ alignSelf: "stretch" }} colorScheme={scheme}>
			<ComposeColumn verticalArrangement={{ spacedBy: space.sm }} modifiers={[fillMaxWidth()]}>
				<ComposeText color={t.textSecondary} style={{ typography: "titleSmall" }}>Custom model</ComposeText>
				<ComposeRow verticalAlignment="center" horizontalArrangement={{ spacedBy: space.sm }} modifiers={[fillMaxWidth()]}>
					<OutlinedTextField
						value={field}
						onValueChange={setValue}
						singleLine
						keyboardOptions={{ capitalization: "none", autoCorrectEnabled: false, imeAction: "done" }}
						keyboardActions={{ onDone: use }}
						colors={{ cursorColor: t.highlight }}
						textSelectionColors={{ handleColor: t.highlight, backgroundColor: t.selection }}
						modifiers={[weight(1)]}
					>
						<OutlinedTextField.Label><ComposeText>Model identifier</ComposeText></OutlinedTextField.Label>
					</OutlinedTextField>
					<ComposeButton enabled={Boolean(value.trim())} onClick={use}><ComposeText>Use</ComposeText></ComposeButton>
				</ComposeRow>
			</ComposeColumn>
		</Host>
	);
}

/** Rows the list builds at most; the search reaches the rest. */
const MAX_ROWS = 200;
/** A catalog longer than this gets a search field. */
const SEARCH_FROM = 20;

const makeStyles = (t: Theme) => StyleSheet.create({
	more: { fontFamily: type.footnote.fontFamily, color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	notice: { fontFamily: type.footnote.fontFamily, color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	loading: { marginVertical: space.lg },
});
