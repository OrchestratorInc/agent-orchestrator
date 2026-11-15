import { Column, Host } from "@expo/ui/jetpack-compose";
import { fillMaxWidth } from "@expo/ui/jetpack-compose/modifiers";
import { Linking, ScrollView, StyleSheet, Text, View } from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { LOCAL_NETWORK_HINT } from "./connectionError";
import { FormPasswordField, FormTextField, FormTextFieldRow, FormToggle } from "./formFields";
import type { Theme } from "./theme";
import { useThemeState, useThemedStyles } from "./ThemeProvider";
import { space, type } from "./tokens";
import { Button, SHEET_SCROLL_CONTENT } from "./ui";
import { useManualConnect, type ManualConnectProps } from "./useManualConnect";

// Android: outlined Material fields on a page under the stack's top app bar,
// which carries the title and the back button, with Connect right after them.
export function ManualConnectSheet(props: ManualConnectProps) {
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const form = useManualConnect(props);
	const { cfg, failure } = form;
	const insets = useSafeAreaInsets();
	// The keyboard controller keeps the window from resizing for the IME, so the
	// page pads itself above the keyboard, as the machine editor does. Connect
	// follows the last field. The password's Go key connects too.
	return (
		<KeyboardAvoidingView behavior="padding" automaticOffset style={styles.screen}>
			<ScrollView style={styles.form} contentContainerStyle={[SHEET_SCROLL_CONTENT, { paddingTop: space.sm, paddingBottom: space.lg + insets.bottom }]} keyboardShouldPersistTaps="handled">
				<Host matchContents={{ vertical: true }} colorScheme={scheme}>
					<Column verticalArrangement={{ spacedBy: space.sm }} modifiers={[fillMaxWidth()]}>
						<FormTextField
							label="Name"
							value={form.machineName}
							onChangeText={form.setMachineName}
							placeholder="Required"
							maxLength={48}
							returnKeyType="next"
							testID="manual-connect-name"
						/>
						{/* The port is a few digits: a narrow field beside the host. */}
						<FormTextFieldRow
							label="Host"
							fields={[
								{
									label: "Host",
									value: cfg.host,
									onChangeText: form.setField("host"),
									placeholder: "192.168.x.x",
									autoCapitalize: "none",
									autoCorrect: false,
									keyboardType: "url",
									error: failure?.field === "host" ? failure.message : undefined,
									width: "grow",
									testID: "manual-connect-host",
								},
								{
									label: "Port",
									value: cfg.httpPort,
									onChangeText: form.setField("httpPort"),
									keyboardType: "number-pad",
									error: form.portError,
									width: 104,
									testID: "manual-connect-port",
								},
							]}
						/>
						<FormPasswordField
							label="Password"
							value={cfg.password}
							onChangeText={form.setField("password")}
							placeholder="Pairing code or password"
							returnKeyType="go"
							onSubmitEditing={() => { if (form.canConnect && !form.busy) void form.connect(); }}
							error={failure?.field === "password" ? failure.message : undefined}
							testID="manual-connect-password"
						/>
						<FormToggle label="Use TLS" value={!!cfg.secure} onValueChange={form.setSecure} testID="manual-connect-tls" />
					</Column>
				</Host>
				<Button
					title={form.editing ? "Save changes" : "Connect"}
					icon={form.editing ? "check" : "link"}
					loading={form.busy}
					disabled={!form.canConnect}
					onPress={() => void form.connect()}
					style={styles.connect}
				/>
				{form.connectHint ? <Text style={[styles.hintText, styles.connectHint]}>{form.connectHint}</Text> : null}
				{props.onScanInstead ? (
					<Button
						title="Connect with QR code"
						variant="ghost"
						icon="qrScanner"
						disabled={form.busy}
						onPress={props.onScanInstead}
						style={styles.scanInstead}
					/>
				) : null}
				{failure?.showLocalNetworkHint ? (
					<View style={styles.hint}>
						<Text style={styles.hintText}>{LOCAL_NETWORK_HINT}</Text>
						<Button title="Open settings" variant="secondary" icon="settings" onPress={() => void Linking.openSettings()} />
					</View>
				) : null}
			</ScrollView>
		</KeyboardAvoidingView>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
		form: { flex: 1 },
		hint: { gap: space.sm, marginTop: space.md },
		connectHint: { marginTop: space.sm, textAlign: "center" },
		hintText: { fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		connect: { marginTop: space.lg },
		scanInstead: { alignSelf: "center", marginTop: space.xs },
	});
