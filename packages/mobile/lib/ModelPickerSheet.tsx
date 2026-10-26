import { FieldGroup, Host } from "@expo/ui";
import { Button as ComposeButton, Column as ComposeColumn, OutlinedTextField, Row as ComposeRow, Text as ComposeText, useNativeState } from "@expo/ui/jetpack-compose";
import { fillMaxWidth, weight } from "@expo/ui/jetpack-compose/modifiers";
import { Button as SwiftUIButton, HStack, TextField } from "@expo/ui/swift-ui";
import { autocorrectionDisabled, buttonStyle, disabled as disabledModifier, onSubmit, submitLabel, textInputAutocapitalization, tint } from "@expo/ui/swift-ui/modifiers";
import { useState } from "react";
import { Platform, StyleSheet, Text } from "react-native";
import type { AgentModelCatalog } from "./api";
import { haptics } from "./haptics";
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
	const choose = (value: string) => { haptics.select(); onClose(); onSelect(value); };
	const options: PickerOption[] = [
		{ id: "", title: "Auto", detail: "Let the agent choose" },
		...(catalog?.models ?? []).map((model) => ({ id: model.id, title: model.label })),
	];
	const notice = error || catalog?.warning;
	const custom = catalog && (catalog.selectionMode === "text" || catalog.allowCustom)
		? <CustomModel initial={selected && !catalog.models.some((item) => item.id === selected) ? selected : ""} onUse={choose} />
		: null;
	return (
		<NativePickerList
			header={<>
				<SheetHeader title="Model" subtitle="Choose how this agent runs the task." />
				{notice ? <Text accessibilityRole="alert" style={styles.notice}>{notice}</Text> : null}
				{loading ? <Spinner size="large" accessibilityLabel="Loading models" style={styles.loading} /> : null}
			</>}
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

const makeStyles = (t: Theme) => StyleSheet.create({
	notice: { fontFamily: type.footnote.fontFamily, color: t.amber, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	loading: { marginVertical: space.lg },
});
