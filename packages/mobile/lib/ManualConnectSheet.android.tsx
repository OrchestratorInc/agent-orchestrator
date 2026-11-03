import { Column, Host } from "@expo/ui/jetpack-compose";
import { fillMaxWidth } from "@expo/ui/jetpack-compose/modifiers";
import { Linking, ScrollView, StyleSheet, Text, View } from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { LOCAL_NETWORK_HINT } from "./connectionError";
import { FormPasswordField, FormTextField, FormToggle } from "./formFields";
import type { Theme } from "./theme";
import { useThemeState, useThemedStyles } from "./ThemeProvider";
import { space, type } from "./tokens";
import { Button, SHEET_SCROLL_CONTENT, SheetHeader } from "./ui";
import { useManualConnect, type ManualConnectProps } from "./useManualConnect";

// Android: the title and the Connect button stay in the sheet's body, as a
// Material bottom sheet's do, with outlined Material fields between them.
export function ManualConnectSheet(props: ManualConnectProps) {
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const form = useManualConnect(props);
	const { cfg, failure } = form;
	const insets = useSafeAreaInsets();

	// The sheet opens at full height, under the status bar, so its body is
	// padded for the bar (see CONNECT_SHEET_OPTIONS). The keyboard controller
	// keeps the window from resizing for the IME, so the sheet pads itself above
	// the keyboard, as the machine editor does. Connect and what it waits for
	// are pinned below the scrolling fields, so they sit just above the keyboard
	// while it is up and above the gesture bar while it is down: at the form's
	// end, Connect was left under the keyboard. The password's Go key connects too.
	return (
		// The bar's room is outside the scroll view, so scrolled fields stop below the bar.
		<KeyboardAvoidingView behavior="padding" automaticOffset style={[styles.screen, { paddingTop: insets.top }]}>
			<ScrollView style={styles.form} contentContainerStyle={[SHEET_SCROLL_CONTENT, { paddingBottom: space.lg }]} keyboardShouldPersistTaps="handled">
				<SheetHeader title={form.title} subtitle={form.subtitle} />
				<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.fields}>
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
						<FormTextField
							label="Host"
							value={cfg.host}
							onChangeText={form.setField("host")}
							placeholder="192.168.x.x or my-pc.tailXXXX.ts.net"
							autoCapitalize="none"
							autoCorrect={false}
							keyboardType="url"
							error={failure?.field === "host" ? failure.message : undefined}
							testID="manual-connect-host"
						/>
						<FormTextField
							label="API port"
							value={cfg.httpPort}
							onChangeText={form.setField("httpPort")}
							keyboardType="number-pad"
							error={form.portError}
							testID="manual-connect-port"
						/>
						<FormPasswordField
							label="Password"
							value={cfg.password}
							onChangeText={form.setField("password")}
							placeholder="Connection password"
							returnKeyType="go"
							onSubmitEditing={() => { if (form.canConnect && !form.busy) void form.connect(); }}
							error={failure?.field === "password" ? failure.message : undefined}
							testID="manual-connect-password"
						/>
						<FormToggle label="Use TLS" value={!!cfg.secure} onValueChange={form.setSecure} testID="manual-connect-tls" />
					</Column>
				</Host>
				{failure?.showLocalNetworkHint ? (
					<View style={styles.hint}>
						<Text style={styles.hintText}>{LOCAL_NETWORK_HINT}</Text>
						<Button title="Open settings" variant="secondary" icon="settings" onPress={() => void Linking.openSettings()} />
					</View>
				) : null}
			</ScrollView>
			<View style={[styles.footer, { paddingBottom: space.md + insets.bottom }]}>
				{/* What the disabled Connect is waiting for, beside it. */}
				{form.connectHint ? <Text style={styles.hintText}>{form.connectHint}</Text> : null}
				<Button
					title={form.editing ? "Save changes" : "Connect"}
					icon={form.editing ? "check" : "link"}
					loading={form.busy}
					disabled={!form.canConnect}
					onPress={() => void form.connect()}
				/>
			</View>
		</KeyboardAvoidingView>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgSurface },
		form: { flex: 1 },
		fields: { marginTop: space.lg },
		hint: { gap: space.sm, marginTop: space.md },
		hintText: { fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		footer: { gap: space.sm, paddingHorizontal: SHEET_SCROLL_CONTENT.paddingHorizontal, paddingTop: space.sm },
	});
