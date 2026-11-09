import type { DesktopStatusTone } from "./desktopStatus";
import type { Host as PairedHost } from "./hosts";
import { machineAppearance, machineSymbolNames } from "./machineAppearance";
import { SettingsStatusRow, type SettingsTone } from "./SettingsForm";

/**
 * One paired machine, said once: its own glyph, its name, how its link is
 * doing and, beside other machines, which one is the default. The row opens
 * the machine's page, where it is tested, made the default, reconnected,
 * edited or forgotten; Settings itself only lists the machines.
 */
export function MachineRow({
	host,
	status,
	isDefault,
	onPress,
}: {
	host: Pick<PairedHost, "id" | "name" | "appearance">;
	status: { label: string; tone: SettingsTone };
	/** Marks the default. Only worth saying when there is another machine to tell it from. */
	isDefault: boolean;
	onPress(): void;
}) {
	return (
		<SettingsStatusRow
			icon={machineSymbolNames[machineAppearance(host).symbol]}
			label={host.name}
			status={isDefault ? `${status.label} · Default` : status.label}
			tone={status.tone}
			onPress={onPress}
			testID={`settings-machine-${host.id}`}
		/>
	);
}

/** A connection state's tone, as a settings row draws it. */
export function machineStatusTone(tone: DesktopStatusTone): SettingsTone {
	return tone === "ok" ? "good" : tone === "error" ? "bad" : "default";
}
