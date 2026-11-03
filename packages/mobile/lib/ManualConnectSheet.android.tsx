import { Column, Host } from "@expo/ui/jetpack-compose";
import { fillMaxWidth } from "@expo/ui/jetpack-compose/modifiers";
import { Linking, ScrollView, StyleSheet, Text, View } from "react-native";
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

	// The sheet is sized by detents rather than by its content (see
	// CONNECT_SHEET_OPTIONS — two detents are what lets the OS expand it for the
	// keyboard). A detent gives the content a definite height, so the form
	// scrolls within it while Connect stays pinned under it, above the gesture
	// bar the sheet runs on under: with the hint above it, a Connect at the
	// form's end sat half under the bar at the first detent.
	return (
		<View style={styles.screen}>
			<ScrollView style={styles.form} contentContainerStyle={[SHEET_SCROLL_CONTENT, styles.formContent]} keyboardShouldPersistTaps="handled">
				<SheetHeader title={form.title} subtitle={form.subtitle} />
				<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.fields}>
					<Column verticalArrangement={{ spacedBy: space.sm }} modifiers={[fillMaxWidth()]}>
						<FormTextField
							label="Name"
							value={form.machineName}
							onChangeText={form.setMachineName}
							placeholder="AzureLinux"
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
			<View style={[styles.footer, { paddingBottom: space.lg + insets.bottom }]}>
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
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgSurface },
		form: { flex: 1 },
		formContent: { paddingBottom: space.lg },
		fields: { marginTop: space.lg },
		hint: { gap: space.sm, marginTop: space.md },
		hintText: { fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		footer: { gap: space.sm, paddingHorizontal: SHEET_SCROLL_CONTENT.paddingHorizontal, paddingTop: space.sm },
	});
