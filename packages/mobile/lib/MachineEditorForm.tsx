import { Host } from "@expo/ui";
import { Form, LabeledContent, Section, Text } from "@expo/ui/swift-ui";
import { textSelection } from "@expo/ui/swift-ui/modifiers";
import { ACCOUNT_MANAGED_NOTE, endpointDisplayAddress, environmentEndpointRows } from "./environmentView";
import { FormFooter, formModifiers, FormPasswordField, FormTextField, FormToggle, sectionModifiers } from "./formFields";
import type { Host as PairedHost } from "./hosts";
import { MachineSymbolMenu } from "./MachineSymbolMenu";
import { useTheme, useThemeState } from "./ThemeProvider";
import type { MachineEditor } from "./useMachineEditor";

export type MachineEditorFormProps = { host: PairedHost; editor: MachineEditor };

// iOS: the machine editor as a SwiftUI Form. The page's bar holds Save; the
// back button leaves, asking first when there are unsaved changes.
export function MachineEditorForm({ host, editor }: MachineEditorFormProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const { draft, saving, failure } = editor;
	// iOS has no per-field error, so the section with the fields at fault says
	// what is wrong in its footer.
	const connectionError = failure?.message ?? editor.portError;
	const route = environmentEndpointRows(host)[draft.endpointIndex];
	return (
		<Host style={{ flex: 1 }} colorScheme={scheme}>
			<Form modifiers={formModifiers(t)}>
				<Section
					footer={editor.accountManaged && failure ? <FormFooter error>{failure.message}</FormFooter> : undefined}
					modifiers={sectionModifiers(t)}
				>
					<FormTextField
						label="Name"
						value={draft.machineName}
						onChangeText={editor.setName}
						placeholder="Studio Mac"
						maxLength={48}
						editable={!saving}
						testID={`environment-name-${host.id}`}
					/>
					<MachineSymbolMenu selected={draft.appearance.symbol} onSelect={editor.setSymbol} disabled={saving} testID={`environment-symbol-${host.id}`} />
				</Section>
				{editor.accountManaged ? (
					<Section title="Connection" footer={<FormFooter>{ACCOUNT_MANAGED_NOTE}</FormFooter>} modifiers={sectionModifiers(t)}>
						<LabeledContent label="Address" testID={`environment-account-managed-${host.id}`}>
							<Text modifiers={[textSelection(true)]}>{route ? endpointDisplayAddress(route.address).authority : "None"}</Text>
						</LabeledContent>
					</Section>
				) : (
					<Section
						title="Connection"
						footer={connectionError ? <FormFooter error>{connectionError}</FormFooter> : undefined}
						modifiers={sectionModifiers(t)}
					>
						<FormTextField
							label="Host"
							value={draft.config.host}
							onChangeText={editor.setHost}
							autoCapitalize="none"
							autoCorrect={false}
							keyboardType="url"
							textContentType="URL"
							editable={!saving}
							testID={`environment-host-${host.id}`}
						/>
						<FormTextField
							label="API port"
							value={draft.config.httpPort}
							onChangeText={editor.setPort}
							keyboardType="number-pad"
							editable={!saving}
							testID={`environment-port-${host.id}`}
						/>
						<FormPasswordField
							label="Password"
							value={draft.config.password}
							onChangeText={editor.setPassword}
							placeholder="Connection password"
							editable={!saving}
							testID={`environment-password-${host.id}`}
						/>
						<FormToggle
							label="Use TLS"
							value={Boolean(draft.config.secure)}
							onValueChange={editor.setSecure}
							disabled={saving}
							testID={`environment-tls-${host.id}`}
						/>
					</Section>
				)}
			</Form>
		</Host>
	);
}
