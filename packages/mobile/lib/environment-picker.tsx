import { Host, Menu, Section, Toggle } from "@expo/ui/swift-ui";
import { buttonStyle, controlSize, tint } from "@expo/ui/swift-ui/modifiers";
import { startTransition } from "react";
import type { EnvironmentPickerProps } from "./environment-picker.types";
import { haptics } from "./haptics";
import { machineAppearance } from "./machineAppearance";
import { machineMenuImage } from "./machine-menu-image";
import { symbols } from "./symbols";
import { useTheme, useThemeState } from "./ThemeProvider";
import { ALL_WORKER_ENVIRONMENTS, workerEnvironmentOptions } from "./worker-controls";

export type { EnvironmentPickerProps } from "./environment-picker.types";

/**
 * Moves the Workers board to another machine: a bordered SwiftUI button whose
 * menu lists every environment, each with its machine's symbol and the system's
 * check on the current one. ./environment-picker.android draws the same choice
 * as Material's outlined button and dropdown.
 */
export function EnvironmentPicker({ environments, selectedEnvironmentId, onSelectEnvironment }: EnvironmentPickerProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const select = (environmentId: string) => {
		if (environmentId === selectedEnvironmentId) return;
		haptics.select();
		// Preparing a replacement board must not block the menu's dismissal.
		startTransition(() => onSelectEnvironment(environmentId));
	};
	return (
		<Host matchContents colorScheme={scheme}>
			<Menu label="Switch environment" systemImage={symbols.devices.ios} modifiers={[buttonStyle("bordered"), controlSize("large"), tint(t.accent)]} testID="environment-picker">
				<Section title="Environment">
					{workerEnvironmentOptions(environments).map((option) => (
						<Toggle
							key={option.id}
							label={option.label}
							systemImage={machineMenuImage(option.id === ALL_WORKER_ENVIRONMENTS ? "server" : machineAppearance({ id: option.id, name: option.label, appearance: option.appearance }).symbol)}
							isOn={option.id === selectedEnvironmentId}
							onIsOnChange={() => select(option.id)}
						/>
					))}
				</Section>
			</Menu>
		</Host>
	);
}
