import { Host, Icon, LazyColumn, PullToRefreshBox, Text } from "@expo/ui/jetpack-compose";
import { fillMaxSize } from "@expo/ui/jetpack-compose/modifiers";
import { GroupedRow, GroupedSection } from "./composeGroupedList.android";
import type { EnvironmentMachineListProps, EnvironmentMachineSummary } from "./EnvironmentMachineRow.types";
import { environmentRouteSummary, environmentStatusColor } from "./environmentView";
import { machineMenuImage } from "./machine-menu-image.android";
import { machineAppearance } from "./machineAppearance";
import { useTheme, useThemeState } from "./ThemeProvider";

export type { EnvironmentMachineListProps, EnvironmentMachineSummary } from "./EnvironmentMachineRow.types";

// Android: the paired machines as a Material 3 list in a pull-to-refresh box,
// each row opening the machine's page.
export function EnvironmentMachineList({ machines, footer, error, refreshing, onRefresh, onOpen }: EnvironmentMachineListProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host style={{ flex: 1 }} colorScheme={scheme}>
			<PullToRefreshBox
				isRefreshing={refreshing}
				onRefresh={() => void onRefresh()}
				indicator={{ color: t.accent, containerColor: t.bgElevated }}
				modifiers={[fillMaxSize()]}
			>
				<LazyColumn contentPadding={{ start: 16, end: 16, top: 8, bottom: 24 }} modifiers={[fillMaxSize()]}>
					<GroupedSection footer={error ?? footer} footerColor={error ? t.red : undefined}>
						{machines.map((machine) => (
							<EnvironmentMachineRow key={machine.host.id} machine={machine} onPress={() => onOpen(machine.host.id)} />
						))}
					</GroupedSection>
				</LazyColumn>
			</PullToRefreshBox>
		</Host>
	);
}

/** A machine's row: its glyph, name and route, and its connection in words. */
export function EnvironmentMachineRow({ machine, onPress, position }: {
	machine: EnvironmentMachineSummary;
	onPress: () => void;
	/** Set by GroupedSection. */
	position?: React.ComponentProps<typeof GroupedRow>["position"];
}) {
	const t = useTheme();
	const { host, isDefault, status } = machine;
	const route = environmentRouteSummary(host, machine.activeConfig);
	return (
		<GroupedRow
			position={position}
			headline={host.name}
			supporting={isDefault ? `Default · ${route}` : route}
			leading={<Icon source={machineMenuImage(machineAppearance(host).symbol)} tint={t.textSecondary} />}
			trailing={<Text color={environmentStatusColor(t, status)}>{status.label}</Text>}
			onPress={onPress}
			testID={`environment-machine-${host.id}`}
		/>
	);
}
