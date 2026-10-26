import { Host, Icon, LazyColumn, Text } from "@expo/ui/jetpack-compose";
import { fillMaxSize, graphicsLayer } from "@expo/ui/jetpack-compose/modifiers";
import { Stack } from "expo-router";
import { useMemo, useState } from "react";
import { GroupedRow, GroupedSection } from "./composeGroupedList.android";
import {
	endpointDisplayAddress,
	environmentEndpointRows,
	environmentPlatformLabel,
	environmentStatusColor,
	primaryEnvironmentEndpoint,
} from "./environmentView";
import { formDrawables } from "./formDrawables.android";
import { DEFAULT_MACHINE_NOTE, FORGET_MACHINE_NOTE, RECONNECT_NOTE, type MachineDetailProps } from "./MachineDetail.types";
import { useTheme, useThemeState } from "./ThemeProvider";

export { DEFAULT_MACHINE_NOTE, FORGET_MACHINE_NOTE, RECONNECT_NOTE, type MachineDetailProps } from "./MachineDetail.types";

// Android: a machine's page as a Material 3 settings list: its status and
// routes, the actions that change how the phone reaches it, and Forget at the
// foot. Edit is a top app bar action.
export function MachineDetail({ host, activeConfig, isDefault, status, switching, forgetting, onMakeDefault, onReconnect, onForget, onEdit }: MachineDetailProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const [showOtherRoutes, setShowOtherRoutes] = useState(false);
	const endpoints = environmentEndpointRows(host, activeConfig);
	const primary = primaryEnvironmentEndpoint(endpoints);
	const others = endpoints.filter((endpoint) => endpoint.key !== primary?.key);
	const busy = switching || forgetting;
	const headerItems = useMemo(() => (
		<Stack.Toolbar placement="right">
			<Stack.Toolbar.Button icon={formDrawables.edit} accessibilityLabel={`Edit ${host.name}`} disabled={forgetting} onPress={onEdit} />
		</Stack.Toolbar>
	), [forgetting, host.name, onEdit]);

	return (
		<>
			{headerItems}
			<Host style={{ flex: 1 }} colorScheme={scheme}>
				<LazyColumn contentPadding={{ start: 16, end: 16, top: 8, bottom: 24 }} verticalArrangement={{ spacedBy: 16 }} modifiers={[fillMaxSize()]}>
					<GroupedSection footer={isDefault ? DEFAULT_MACHINE_NOTE : undefined}>
						<GroupedRow headline="Status" trailing={<Text color={environmentStatusColor(t, status)}>{status.label}</Text>} testID="machine-status" />
						{host.platform ? <GroupedRow headline="Platform" trailing={environmentPlatformLabel(host.platform)} /> : null}
					</GroupedSection>
					<GroupedSection title="Routes">
						{primary ? (
							<GroupedRow
								headline="Active route"
								supporting={endpointDisplayAddress(primary.address).authority}
								trailing={primary.label}
								testID={`environment-active-address-${host.id}`}
							/>
						) : <GroupedRow headline="No saved connection routes." />}
						{others.length > 0 ? (
							<GroupedRow
								headline="Other routes"
								supporting={`${others.length} available`}
								trailing={
									<Icon
										source={formDrawables.expandMore}
										tint={t.textSecondary}
										contentDescription={showOtherRoutes ? "Hide other routes" : "Show other routes"}
										modifiers={[graphicsLayer({ rotationZ: showOtherRoutes ? 180 : 0 })]}
									/>
								}
								onPress={() => setShowOtherRoutes((shown) => !shown)}
								testID={`environment-other-routes-${host.id}`}
							/>
						) : null}
						{showOtherRoutes ? others.map((endpoint) => (
							<GroupedRow
								key={endpoint.key}
								headline={endpoint.label}
								supporting={endpointDisplayAddress(endpoint.address).authority}
								testID={`environment-other-route-${endpoint.key}`}
							/>
						)) : null}
					</GroupedSection>
					<GroupedSection footer={RECONNECT_NOTE}>
						{isDefault ? null : (
							<GroupedRow
								headline={switching ? "Making default…" : "Make default"}
								onPress={onMakeDefault}
								disabled={busy}
								testID={`environment-make-default-${host.id}`}
							/>
						)}
						<GroupedRow headline="Reconnect" onPress={onReconnect} disabled={forgetting} testID={`environment-reconnect-${host.id}`} />
					</GroupedSection>
					<GroupedSection footer={FORGET_MACHINE_NOTE}>
						<GroupedRow
							headline={forgetting ? "Forgetting…" : "Forget machine"}
							destructive
							onPress={onForget}
							disabled={busy}
							testID={`environment-forget-${host.id}`}
						/>
					</GroupedSection>
				</LazyColumn>
			</Host>
		</>
	);
}
