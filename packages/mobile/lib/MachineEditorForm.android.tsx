import { Column, Host } from "@expo/ui/jetpack-compose";
import { fillMaxWidth } from "@expo/ui/jetpack-compose/modifiers";
import { ScrollView, StyleSheet, Text, View } from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { ACCOUNT_MANAGED_NOTE } from "./environmentView";
import { FormPasswordField, FormTextField, FormToggle } from "./formFields";
import type { Host as PairedHost } from "./hosts";
import { MachineSymbolPicker } from "./MachineSymbolPicker";
import type { Theme } from "./theme";
import { useThemeState, useThemedStyles } from "./ThemeProvider";
import { space, type } from "./tokens";
import type { MachineEditor } from "./useMachineEditor";

export type MachineEditorFormProps = { host: PairedHost; editor: MachineEditor };

// Android: the machine editor as outlined Material fields, each carrying its
// own error, around the grid of machine shapes. The top app bar holds Save;
// back leaves, asking first when there are unsaved changes.
//
// The keyboard controller keeps the window from resizing for the IME, so the
// page pads itself above the keyboard. That shortens the scroll view, which
// then scrolls the focused field back into sight and lets the rest scroll.
export function MachineEditorForm({ host, editor }: MachineEditorFormProps) {
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const { draft, saving, failure } = editor;
	return (
		<KeyboardAvoidingView behavior="padding" automaticOffset style={styles.screen}>
			<ScrollView style={styles.scroll} contentContainerStyle={styles.content} keyboardShouldPersistTaps="handled">
				<Host matchContents={{ vertical: true }} colorScheme={scheme}>
					<FormTextField
						label="Name"
						value={draft.machineName}
						onChangeText={editor.setName}
						placeholder="Studio Mac"
						maxLength={48}
						editable={!saving}
						// Only the name and icon save for an account machine, so a failure is the name's.
						error={editor.accountManaged ? failure?.message : undefined}
						testID={`environment-name-${host.id}`}
					/>
				</Host>
				<View style={styles.group}>
					<Text accessibilityRole="header" style={styles.heading}>Icon</Text>
					<MachineSymbolPicker
						testIDPrefix={`environment-symbol-${host.id}`}
						selected={draft.appearance.symbol}
						onSelect={editor.setSymbol}
						disabled={saving}
					/>
				</View>
				<View style={styles.group}>
					<Text accessibilityRole="header" style={styles.heading}>Connection</Text>
					{editor.accountManaged ? (
						<Text testID={`environment-account-managed-${host.id}`} style={styles.note}>{ACCOUNT_MANAGED_NOTE}</Text>
					) : (
						<Host matchContents={{ vertical: true }} colorScheme={scheme}>
							<Column verticalArrangement={{ spacedBy: space.sm }} modifiers={[fillMaxWidth()]}>
								<FormTextField
									label="Host"
									value={draft.config.host}
									onChangeText={editor.setHost}
									autoCapitalize="none"
									autoCorrect={false}
									keyboardType="url"
									editable={!saving}
									error={failure?.field === "host" ? failure.message : undefined}
									testID={`environment-host-${host.id}`}
								/>
								<FormTextField
									label="API port"
									value={draft.config.httpPort}
									onChangeText={editor.setPort}
									keyboardType="number-pad"
									editable={!saving}
									error={editor.portError}
									testID={`environment-port-${host.id}`}
								/>
								<FormPasswordField
									label="Password"
									value={draft.config.password}
									onChangeText={editor.setPassword}
									placeholder="Connection password"
									editable={!saving}
									error={failure?.field === "password" ? failure.message : undefined}
									testID={`environment-password-${host.id}`}
								/>
								<FormToggle
									label="Use TLS"
									value={Boolean(draft.config.secure)}
									onValueChange={editor.setSecure}
									disabled={saving}
									testID={`environment-tls-${host.id}`}
								/>
							</Column>
						</Host>
					)}
				</View>
			</ScrollView>
		</KeyboardAvoidingView>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	scroll: { flex: 1 },
	content: { paddingHorizontal: space.lg, paddingTop: space.lg, paddingBottom: space.xxxl },
	group: { marginTop: space.xl, gap: space.sm },
	heading: { fontFamily: "Geist_500Medium", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
	note: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});
