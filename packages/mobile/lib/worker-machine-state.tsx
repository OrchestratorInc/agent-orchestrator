import { ActivityIndicator, StyleSheet, Text, View } from "react-native";
import { EnvironmentPicker } from "./environment-picker";
import type { LinkHealth } from "./linkHealth";
import { MachineAvatar } from "./MachineIdentity";
import type { HostSnapshot } from "./otherHosts";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { space, type } from "./tokens";
import { Button } from "./ui";
import type { WorkerEnvironment } from "./worker-controls";

export function WorkerMachineState({ host, environments, onSelectEnvironment, onRetry, linkHealth = "ok" }: {
	host: Pick<HostSnapshot, "hostId" | "name" | "appearance" | "connection" | "errorStatus">;
	/**
	 * This machine's classified link. "phone-offline" means the phone is the one
	 * to blame; "reconnecting" means one blip, not yet a machine that is down.
	 */
	linkHealth?: LinkHealth;
	environments: readonly WorkerEnvironment[];
	onSelectEnvironment: (environmentId: string) => void;
	onRetry: () => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const phoneOffline = linkHealth === "phone-offline";
	const reconnecting = linkHealth === "reconnecting" && host.connection !== "open";
	const connecting = host.connection === "connecting" && !phoneOffline;
	const busy = connecting || reconnecting;
	const status = connecting ? "Connecting…" : phoneOffline ? "You're offline" : reconnecting ? "Reconnecting…" : "Can't reach this machine";
	return (
		<View style={styles.container}>
			<MachineAvatar appearance={host.appearance} size={space.huge * 2} />
			<Text style={styles.name}>{host.name}</Text>
			<View testID="worker-machine-status" accessibilityLabel={connecting ? `Connecting to ${host.name}` : phoneOffline ? "This phone is offline" : reconnecting ? `Reconnecting to ${host.name}` : `Can't reach ${host.name}`} accessibilityLiveRegion="polite" accessibilityState={{ busy }} style={styles.status}>
				{busy ? <ActivityIndicator size="small" color={t.textSecondary} /> : null}
				<Text style={styles.statusText}>{status}</Text>
			</View>
			<Text testID="worker-machine-hint" style={styles.hint}>
				{connecting ? "Workers will appear once this machine connects."
					: phoneOffline ? "Workers load once this phone is back online."
					: reconnecting ? "Still trying to reach this machine. Workers will appear once it answers."
					: host.errorStatus === 426 ? "Update Orchestrator on this phone and the machine to load its workers."
						: "Check that Orchestrator is running and this machine is reachable to load its workers."}
			</Text>
			<View style={styles.actions}>
				{!connecting && !phoneOffline ? <Button title="Retry" icon="refresh-cw" variant="ghost" onPress={onRetry} /> : null}
				<EnvironmentPicker environments={environments} selectedEnvironmentId={host.hostId} onSelectEnvironment={onSelectEnvironment} />
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	container: { flex: 1, minHeight: 320, alignItems: "center", justifyContent: "center", padding: space.xxxl },
	name: { ...type.title3, color: t.textPrimary, textAlign: "center", marginTop: space.sm },
	status: { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.sm, marginTop: space.md },
	statusText: { ...type.callout, color: t.textSecondary },
	hint: { ...type.subheadline, color: t.textTertiary, textAlign: "center", marginTop: space.sm },
	actions: { alignItems: "center", gap: space.sm, marginTop: space.xxl },
});
