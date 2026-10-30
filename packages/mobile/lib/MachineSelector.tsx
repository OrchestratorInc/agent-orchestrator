import type { DesktopStatusTone } from "./desktopStatus";
import { haptics } from "./haptics";
import type { Host as PairedHost } from "./hosts";
import { SettingsPickerRow, SettingsValueRow, type SettingsOption, type SettingsTone } from "./SettingsForm";

/**
 * Which paired machine is the default, as the platform's picker row: a menu
 * on iOS, a dropdown on Android. Choosing one is the whole job here; editing
 * and forgetting machines stay in Environments.
 */
export function MachineSelector({
	hosts,
	selectedHostId,
	selectedHostName,
	switchingHostId,
	onSelect,
}: {
	hosts: PairedHost[];
	selectedHostId: string | null;
	selectedHostName?: string | null;
	switchingHostId: string | null;
	onSelect(id: string): void;
}) {
	const selectedHost = hosts.find((host) => host.id === selectedHostId);
	if (hosts.length === 0) {
		return <SettingsValueRow icon="defaultMachine" label="Default machine" value={selectedHostName ?? "None"} testID="machine-selector" />;
	}
	const options: SettingsOption<string>[] = hosts.map((host) => ({ value: host.id, label: host.name }));
	return (
		<SettingsPickerRow
			icon="defaultMachine"
			label="Default machine"
			// Until a paired machine is chosen, the one the saved config names
			// still shows as the value.
			options={selectedHost ? options : [{ value: "", label: selectedHostName ?? "None" }, ...options]}
			selectedValue={selectedHost?.id ?? ""}
			onValueChange={(id) => {
				if (!id || id === selectedHostId) return;
				haptics.select();
				onSelect(id);
			}}
			disabled={switchingHostId !== null}
			testID="machine-selector"
		/>
	);
}

/**
 * How the connection to the default machine is doing. The words carry the
 * state; the green or red only backs them up.
 */
export function MachineStatusRow({
	hosts,
	statusLabel,
	statusTone,
	switchingHostId,
}: {
	hosts: PairedHost[];
	statusLabel: string;
	statusTone: DesktopStatusTone;
	switchingHostId: string | null;
}) {
	const switchingTo = switchingHostId === null ? undefined : hosts.find((host) => host.id === switchingHostId);
	const tone: SettingsTone = statusTone === "ok" ? "good" : statusTone === "error" ? "bad" : "default";
	return (
		<SettingsValueRow
			icon="status"
			label="Status"
			value={switchingTo ? `Switching to ${switchingTo.name}…` : statusLabel}
			tone={switchingTo ? "default" : tone}
			testID="machine-selector-status"
		/>
	);
}
