import { Host } from "@expo/ui";
import { Button, Form, HStack, Image, Section, Spacer, Text, VStack } from "@expo/ui/swift-ui";
import { accessibilityLabel, font, foregroundStyle, frame, lineLimit, refreshable, truncationMode } from "@expo/ui/swift-ui/modifiers";
import type { EnvironmentMachineListProps, EnvironmentMachineSummary } from "./EnvironmentMachineRow.types";
import { environmentRouteSummary, environmentStatusColor } from "./environmentView";
import { FormFooter, formModifiers, sectionModifiers } from "./formFields";
import { machineMenuImage } from "./machine-menu-image";
import { machineAppearance } from "./machineAppearance";
import { useTheme, useThemeState } from "./ThemeProvider";
import { space } from "./tokens";

export type { EnvironmentMachineListProps, EnvironmentMachineSummary } from "./EnvironmentMachineRow.types";

// iOS: the paired machines as rows of a SwiftUI Form, each opening the
// machine's page. Pull to refresh is the list's own.
export function EnvironmentMachineList({ machines, footer, error, onRefresh, onOpen }: EnvironmentMachineListProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<Host style={{ flex: 1 }} colorScheme={scheme} testID="environment-list">
			<Form modifiers={[...formModifiers(t), refreshable(onRefresh)]}>
				<Section footer={<FormFooter error={Boolean(error)}>{error ?? footer}</FormFooter>} modifiers={sectionModifiers(t)}>
					{machines.map((machine) => (
						<EnvironmentMachineRow key={machine.host.id} machine={machine} onPress={() => onOpen(machine.host.id)} />
					))}
				</Section>
			</Form>
		</Host>
	);
}

/** A machine's row: its glyph, name and route, and its connection in words. */
export function EnvironmentMachineRow({ machine, onPress }: { machine: EnvironmentMachineSummary; onPress: () => void }) {
	const t = useTheme();
	const { host, isDefault, status } = machine;
	const route = environmentRouteSummary(host, machine.activeConfig);
	const detail = isDefault ? `Default · ${route}` : route;
	return (
		<Button
			onPress={onPress}
			testID={`environment-machine-${host.id}`}
			modifiers={[accessibilityLabel(`${host.name}, ${status.label}, ${detail}`)]}
		>
			<HStack spacing={space.md}>
				<Image
					systemName={machineMenuImage(machineAppearance(host).symbol)}
					modifiers={[font({ textStyle: "title3" }), foregroundStyle(t.textSecondary), frame({ width: 28 })]}
				/>
				<VStack alignment="leading" spacing={space.hair}>
					<Text modifiers={[foregroundStyle(t.textPrimary), lineLimit(1)]}>{host.name}</Text>
					<Text
						testID={`environment-route-summary-${host.id}`}
						modifiers={[font({ textStyle: "subheadline" }), foregroundStyle(t.textSecondary), lineLimit(1), truncationMode("middle")]}
					>
						{detail}
					</Text>
				</VStack>
				<Spacer />
				<Text testID={`environment-status-${host.id}`} modifiers={[font({ textStyle: "subheadline" }), foregroundStyle(environmentStatusColor(t, status))]}>
					{status.label}
				</Text>
				{/* A push row's disclosure, as a NavigationLink draws it. */}
				<Image systemName="chevron.forward" modifiers={[font({ textStyle: "footnote", weight: "semibold" }), foregroundStyle(t.textTertiary)]} />
			</HStack>
		</Button>
	);
}
