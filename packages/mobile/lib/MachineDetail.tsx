import { Host } from "@expo/ui";
import { Button, DisclosureGroup, Form, LabeledContent, Section, Text, VStack } from "@expo/ui/swift-ui";
import { disabled, font, foregroundStyle, textSelection } from "@expo/ui/swift-ui/modifiers";
import { Stack } from "expo-router";
import { useMemo, useState } from "react";
import {
	endpointDisplayAddress,
	environmentEndpointRows,
	environmentPlatformLabel,
	environmentStatusColor,
	primaryEnvironmentEndpoint,
} from "./environmentView";
import { FormFooter, formModifiers, sectionModifiers } from "./formFields";
import { DEFAULT_MACHINE_NOTE, FORGET_MACHINE_NOTE, RECONNECT_NOTE, type MachineDetailProps } from "./MachineDetail.types";
import { symbols } from "./symbols";
import { useTheme, useThemeState } from "./ThemeProvider";
import { space } from "./tokens";

export { DEFAULT_MACHINE_NOTE, FORGET_MACHINE_NOTE, RECONNECT_NOTE, type MachineDetailProps } from "./MachineDetail.types";

// iOS: a machine's page as a SwiftUI Form: its status and routes, the actions
// that change how the phone reaches it, and Forget at the foot. Edit is the
// bar's trailing item.
export function MachineDetail({ host, activeConfig, isDefault, status, switching, forgetting, testing, canTest, onTest, onMakeDefault, onReconnect, onForget, onEdit }: MachineDetailProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const [showOtherRoutes, setShowOtherRoutes] = useState(false);
	const endpoints = environmentEndpointRows(host, activeConfig);
	const primary = primaryEnvironmentEndpoint(endpoints);
	const others = endpoints.filter((endpoint) => endpoint.key !== primary?.key);
	const busy = switching || forgetting;
	const headerItems = useMemo(() => (
		<Stack.Toolbar placement="right">
			<Stack.Toolbar.Button accessibilityLabel={`Edit ${host.name}`} disabled={forgetting} onPress={onEdit}>Edit</Stack.Toolbar.Button>
		</Stack.Toolbar>
	), [forgetting, host.name, onEdit]);

	return (
		<>
			{headerItems}
			<Host style={{ flex: 1 }} colorScheme={scheme}>
				<Form modifiers={formModifiers(t)}>
					<Section footer={isDefault ? <FormFooter>{DEFAULT_MACHINE_NOTE}</FormFooter> : undefined} modifiers={sectionModifiers(t)}>
						<LabeledContent label="Status">
							<Text testID="machine-status" modifiers={[foregroundStyle(environmentStatusColor(t, status))]}>{status.label}</Text>
						</LabeledContent>
						{host.platform ? (
							<LabeledContent label="Platform">
								<Text>{environmentPlatformLabel(host.platform)}</Text>
							</LabeledContent>
						) : null}
					</Section>
					<Section title="Routes" modifiers={sectionModifiers(t)}>
						{primary ? (
							<LabeledContent label="Active route">
								<VStack alignment="trailing" spacing={space.hair}>
									<Text testID={`environment-active-address-${host.id}`} modifiers={[textSelection(true)]}>{endpointDisplayAddress(primary.address).authority}</Text>
									<Text modifiers={[font({ textStyle: "caption" }), foregroundStyle(t.textSecondary)]}>{primary.label}</Text>
								</VStack>
							</LabeledContent>
						) : (
							<Text modifiers={[foregroundStyle(t.textSecondary)]}>No saved connection routes.</Text>
						)}
						{others.length > 0 ? (
							<DisclosureGroup
								label={`Other routes (${others.length})`}
								isExpanded={showOtherRoutes}
								onIsExpandedChange={setShowOtherRoutes}
								testID={`environment-other-routes-${host.id}`}
							>
								{others.map((endpoint) => (
									<LabeledContent key={endpoint.key} label={endpoint.label} testID={`environment-other-route-${endpoint.key}`}>
										<Text modifiers={[textSelection(true)]}>{endpointDisplayAddress(endpoint.address).authority}</Text>
									</LabeledContent>
								))}
							</DisclosureGroup>
						) : null}
					</Section>
					<Section footer={<FormFooter>{RECONNECT_NOTE}</FormFooter>} modifiers={sectionModifiers(t)}>
						<Button
							label={testing ? "Testing…" : "Test connection"}
							systemImage="bolt.horizontal.circle"
							onPress={onTest}
							testID={`environment-test-${host.id}`}
							modifiers={[disabled(testing || busy || !canTest)]}
						/>
						{isDefault ? null : (
							<Button
								label={switching ? "Making default…" : "Make default"}
								systemImage={symbols.checkCircle.ios}
								onPress={onMakeDefault}
								testID={`environment-make-default-${host.id}`}
								modifiers={[disabled(busy)]}
							/>
						)}
						<Button
							label="Reconnect"
							systemImage={symbols.qrScanner.ios}
							onPress={onReconnect}
							testID={`environment-reconnect-${host.id}`}
							modifiers={[disabled(forgetting)]}
						/>
					</Section>
					<Section footer={<FormFooter>{FORGET_MACHINE_NOTE}</FormFooter>} modifiers={sectionModifiers(t)}>
						<Button
							role="destructive"
							label={forgetting ? "Forgetting…" : "Forget machine"}
							systemImage={symbols.delete.ios}
							onPress={onForget}
							testID={`environment-forget-${host.id}`}
							modifiers={[disabled(busy)]}
						/>
					</Section>
				</Form>
			</Host>
		</>
	);
}
