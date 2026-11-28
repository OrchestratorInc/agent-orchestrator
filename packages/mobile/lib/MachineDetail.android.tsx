import { Host, Icon, LazyColumn, Text } from "@expo/ui/jetpack-compose";
import { fillMaxSize, graphicsLayer } from "@expo/ui/jetpack-compose/modifiers";
import * as Clipboard from "expo-clipboard";
import { useMemo, useState, type ComponentProps } from "react";
import { Platform } from "react-native";
import { GroupedRow, GroupedSection, groupedListPadding } from "./composeGroupedList.android";
import {
	endpointDisplayAddress,
	environmentEndpointRows,
	environmentPlatformLabel,
	environmentStatusColor,
	primaryEnvironmentEndpoint,
} from "./environmentView";
import { DEFAULT_MACHINE_NOTE, FORGET_MACHINE_NOTE, RECONNECT_NOTE, type MachineDetailProps } from "./MachineDetail.types";
import { useTheme, useThemeState } from "./ThemeProvider";
import { space } from "./tokens";
import { useGlassBarInset } from "./useHeaderOverlap";
import { HeaderPillButton, PillHeaderRight } from "./headerPill";
import { materialDrawable } from "./toolbarIcon.android";

export { DEFAULT_MACHINE_NOTE, FORGET_MACHINE_NOTE, RECONNECT_NOTE, type MachineDetailProps } from "./MachineDetail.types";

// Android: a machine's page as a Material 3 settings list: its status and
// routes, the actions that change how the phone reaches it, and Forget at the
// foot. Edit is a top app bar action. A route row copies its address on a tap
// or a long press, as a settings row showing a value does.
export function MachineDetail({ host, activeConfig, isDefault, status, switching, forgetting, testing, canTest, onTest, onMakeDefault, onReconnect, onForget, onEdit, notify }: MachineDetailProps) {
	const t = useTheme();
	// Under a clear pill bar the list starts below it, clear of the frost's 16dp seam too.
	const barInset = useGlassBarInset();
	const listPadding = useMemo(() => (barInset > 0 ? { ...groupedListPadding, top: Math.max(groupedListPadding.top, space.lg) + barInset } : groupedListPadding), [barInset]);
	const { scheme } = useThemeState();
	const [showOtherRoutes, setShowOtherRoutes] = useState(false);
	const endpoints = environmentEndpointRows(host, activeConfig);
	const primary = primaryEnvironmentEndpoint(endpoints);
	const others = endpoints.filter((endpoint) => endpoint.key !== primary?.key);
	const busy = switching || forgetting;
	// On a pill of its own over the clear bar, beside the title pill.
	const headerItems = useMemo(() => (
		<PillHeaderRight>
			<HeaderPillButton icon="rename" label={`Edit ${host.name}`} disabled={forgetting} onPress={onEdit} testID="machine-edit" />
		</PillHeaderRight>
	), [forgetting, host.name, onEdit]);

	function copyAddress(authority: string) {
		void Clipboard.setStringAsync(authority);
		// Android 13 and later confirm a copy themselves, and read it out.
		if (Number(Platform.Version) < 33) notify("Address copied.");
	}

	return (
		<>
			{headerItems}
			<Host style={{ flex: 1 }} colorScheme={scheme}>
				<LazyColumn contentPadding={listPadding} verticalArrangement={{ spacedBy: space.lg }} modifiers={[fillMaxSize()]}>
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
										source={materialDrawable("expand_more")}
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
						<GroupedRow
							headline={testing ? "Testing…" : "Test connection"}
							onPress={onTest}
							disabled={testing || busy || !canTest}
							testID={`environment-test-${host.id}`}
						/>
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

/** A route's row, which copies its address. GroupedSection sets `position`. */
function AddressRow({ authority, onCopy, ...row }: Omit<ComponentProps<typeof GroupedRow>, "supporting" | "onPress" | "onLongPress"> & {
	authority: string;
	onCopy: (authority: string) => void;
}) {
	const copy = () => onCopy(authority);
	return <GroupedRow {...row} supporting={authority} onPress={copy} onLongPress={copy} />;
}
