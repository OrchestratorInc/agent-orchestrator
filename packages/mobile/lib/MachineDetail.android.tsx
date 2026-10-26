import { Host, Icon, LazyColumn, Text } from "@expo/ui/jetpack-compose";
import { fillMaxSize, graphicsLayer } from "@expo/ui/jetpack-compose/modifiers";
import * as Clipboard from "expo-clipboard";
import { Stack } from "expo-router";
import { useMemo, useState, type ComponentProps } from "react";
import { Platform } from "react-native";
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
import { ToastHost } from "./Toast";
import { useToast } from "./useToast";

export { DEFAULT_MACHINE_NOTE, FORGET_MACHINE_NOTE, RECONNECT_NOTE, type MachineDetailProps } from "./MachineDetail.types";

// Android: a machine's page as a Material 3 settings list: its status and
// routes, the actions that change how the phone reaches it, and Forget at the
// foot. Edit is a top app bar action. A route row copies its address on a tap
// or a long press, as a settings row showing a value does.
export function MachineDetail({ host, activeConfig, isDefault, status, switching, forgetting, onMakeDefault, onReconnect, onForget, onEdit }: MachineDetailProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const toast = useToast();
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

	function copyAddress(authority: string) {
		void Clipboard.setStringAsync(authority);
		// Android 13 and later confirm a copy themselves, and read it out.
		if (Number(Platform.Version) < 33) toast.show("Address copied.");
	}

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
							<AddressRow
								headline="Active route"
								authority={endpointDisplayAddress(primary.address).authority}
								trailing={primary.label}
								onCopy={copyAddress}
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
							<AddressRow
								key={endpoint.key}
								headline={endpoint.label}
								authority={endpointDisplayAddress(endpoint.address).authority}
								onCopy={copyAddress}
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
			<ToastHost toast={toast} />
		</>
	);
}

/** A route's row, which copies its address. GroupedSection sets `position`. */
function AddressRow({ authority, onCopy, ...row }: Omit<ComponentProps<typeof GroupedRow>, "supporting" | "onPress" | "onLongPress"> & {
	authority: string;
	onCopy: (authority: string) => void;
}) {
	const copy = () => onCopy(authority);
	return <GroupedRow {...row} supporting={authority} onPress={copy} onLongPress={copy} />;
}
