import { Host } from "@expo/ui";
import { Button, Form, Section } from "@expo/ui/swift-ui";
import { Stack } from "expo-router";
import { useMemo, useRef } from "react";
import { Linking } from "react-native";
import { LOCAL_NETWORK_HINT } from "./connectionError";
import { FormFooter, formModifiers, FormPasswordField, FormTextField, FormToggle, sectionModifiers } from "./formFields";
import { Spinner } from "./Spinner";
import { useTheme, useThemeState } from "./ThemeProvider";
import { useManualConnect, type ManualConnectProps } from "./useManualConnect";

// iOS: a SwiftUI Form under the sheet's native bar. app/sheets/connect.tsx
// titles the bar and holds Cancel; Connect (or Save) is the bar's trailing item.
export function ManualConnectSheet(props: ManualConnectProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const form = useManualConnect(props);
	const { cfg, failure, busy, canConnect, editing } = form;
	// Memoized on what the item shows, so typing does not register the header
	// again on every key; the item calls the latest connect. Semibold rather
	// than the done style, which iOS 26 fills with the tint, near white in the
	// dark theme.
	const connectRef = useRef(form.connect);
	connectRef.current = form.connect;
	const headerItems = useMemo(() => (
		<Stack.Toolbar placement="right">
			{busy ? (
				<Stack.Toolbar.View hidesSharedBackground>
					<Spinner accessibilityLabel={editing ? "Saving changes" : "Connecting"} />
				</Stack.Toolbar.View>
			) : (
				<Stack.Toolbar.Button accessibilityLabel={editing ? "Save changes" : "Connect"} disabled={!canConnect} style={{ fontWeight: "600" }} onPress={() => void connectRef.current()}>
					{editing ? "Save" : "Connect"}
				</Stack.Toolbar.Button>
			)}
		</Stack.Toolbar>
	), [busy, canConnect, editing]);

	// iOS has no per-field error, so the connection section's footer says what
	// is wrong, in the error colour, under the fields it is about.
	const connectionError = failure?.message ?? form.portError;
	return (
		<>
			{headerItems}
			<Host style={{ flex: 1 }} colorScheme={scheme}>
				<Form modifiers={formModifiers(t)}>
					<Section footer={<FormFooter>{form.subtitle}</FormFooter>} modifiers={sectionModifiers(t)}>
						<FormTextField
							label="Name"
							value={form.machineName}
							onChangeText={form.setMachineName}
							placeholder="AzureLinux"
							maxLength={48}
							returnKeyType="next"
							testID="manual-connect-name"
						/>
					</Section>
					<Section
						title="Connection"
						footer={connectionError ? <FormFooter error>{connectionError}</FormFooter> : undefined}
						modifiers={sectionModifiers(t)}
					>
						<FormTextField
							label="Host"
							value={cfg.host}
							onChangeText={form.setField("host")}
							placeholder="192.168.x.x or my-pc.tailXXXX.ts.net"
							autoCapitalize="none"
							autoCorrect={false}
							keyboardType="url"
							textContentType="URL"
							testID="manual-connect-host"
						/>
						<FormTextField
							label="API port"
							value={cfg.httpPort}
							onChangeText={form.setField("httpPort")}
							keyboardType="number-pad"
							testID="manual-connect-port"
						/>
						<FormPasswordField
							label="Password"
							value={cfg.password}
							onChangeText={form.setField("password")}
							placeholder="Connection password"
							testID="manual-connect-password"
						/>
						<FormToggle label="Use TLS" value={!!cfg.secure} onValueChange={form.setSecure} testID="manual-connect-tls" />
					</Section>
					{failure?.showLocalNetworkHint ? (
						<Section footer={<FormFooter>{LOCAL_NETWORK_HINT}</FormFooter>} modifiers={sectionModifiers(t)}>
							<Button label="Open settings" systemImage="gearshape" onPress={() => void Linking.openSettings()} />
						</Section>
					) : null}
				</Form>
			</Host>
		</>
	);
}
