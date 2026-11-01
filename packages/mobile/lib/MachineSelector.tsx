import type { DesktopStatusTone } from "./desktopStatus";
import { haptics } from "./haptics";
import type { Host as PairedHost } from "./hosts";
import { SettingsPickerRow, SettingsStatusRow, SettingsValueRow, type SettingsOption, type SettingsTone } from "./SettingsForm";

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

/** A connection state's tone, as a settings row draws it. */
export function machineStatusTone(tone: DesktopStatusTone): SettingsTone {
	return tone === "ok" ? "good" : tone === "error" ? "bad" : "default";
}

/**
 * One paired machine's connection, said once: its name, how the link is doing
 * (or what the last test found) and its own Test button. The words carry the
 * state; the dot beside them only backs it up.
 */
export function MachineConnectionRow({
	hostId,
	name,
	status,
	switching,
	testing,
	disabled,
	onTest,
}: {
	hostId: string;
	name: string;
	status: { label: string; tone: SettingsTone };
	/** This machine is on its way to becoming the default. */
	switching: boolean;
	testing: boolean;
	/** The test can't run now: the phone is offline, or the default is changing. */
	disabled: boolean;
	onTest(): void;
}) {
	return (
		<SettingsStatusRow
			icon="defaultMachine"
			label={name}
			status={switching ? "Making default…" : testing ? "Testing…" : status.label}
			tone={switching || testing ? "default" : status.tone}
			actionLabel="Test"
			actionDisabled={disabled}
			actionLoading={testing}
			onAction={onTest}
			testID={`machine-connection-${hostId || "current"}`}
			actionTestID={`machine-connection-test-${hostId || "current"}`}
		/>
	);
}
