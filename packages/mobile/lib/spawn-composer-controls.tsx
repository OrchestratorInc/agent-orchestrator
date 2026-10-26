import { Button, Host, Picker } from "@expo/ui";
import { StyleSheet, View } from "react-native";
import { AppSymbol } from "./AppSymbol";
import { useThemeState } from "./ThemeProvider";
import type { SpawnComposerControlsProps } from "./spawn-composer-controls.types";
import { radius, space, touchTarget } from "./tokens";

export function SpawnComposerControls({
	hosts,
	hostId,
	onSelectHost,
	projects,
	projectId,
	onSelectProject,
	agents,
	harness,
	onSelectHarness,
	models,
	modelSelection,
	modelLabel,
	onSelectModel,
	approvalModes,
	approvalMode,
	onSelectApprovalMode,
	onChoosePhoto,
	onChooseFile,
	onSpawn,
	busy,
	disabled,
}: SpawnComposerControlsProps) {
	const { scheme } = useThemeState();
	return (
		<View style={styles.stack}>
			<View style={styles.destinationRow}>
				<Host style={styles.destinationHost} colorScheme={scheme}>
					<Picker selectedValue={hostId ?? ""} onValueChange={onSelectHost} appearance="menu">
						{hosts.map((host) => <Picker.Item key={host.id} label={`Run on ${host.label}`} value={host.id} />)}
					</Picker>
				</Host>
				<Host style={styles.destinationHost} colorScheme={scheme}>
					<Picker selectedValue={projectId ?? ""} onValueChange={onSelectProject} appearance="menu">
						<Picker.Item label="Choose project" value="" />
						{projects.map((project) => <Picker.Item key={project.id} label={project.label} value={project.id} />)}
					</Picker>
				</Host>
				<Host style={styles.destinationHost} colorScheme={scheme}>
					<Picker selectedValue={approvalMode} onValueChange={onSelectApprovalMode} appearance="menu">
						{approvalModes.map((mode) => <Picker.Item key={mode.id} label={mode.label} value={mode.id} />)}
					</Picker>
				</Host>
			</View>
			<View style={styles.rail}>
				{/* The universal Button takes no accessibility label, so each glyph
				    carries its button's name. */}
				<Host style={styles.iconHost} colorScheme={scheme}>
					<Button variant="text" onPress={onChoosePhoto} style={styles.iconButton}>
						<AppSymbol name="photo" accessibilityLabel="Choose photo" />
					</Button>
				</Host>
				<Host style={styles.iconHost} colorScheme={scheme}>
					<Button variant="text" onPress={onChooseFile} style={styles.iconButton}>
						<AppSymbol name="attach" accessibilityLabel="Choose file" />
					</Button>
				</Host>
				<Host style={styles.menuHost} colorScheme={scheme}>
					<Picker selectedValue={harness} onValueChange={onSelectHarness} appearance="menu">
						{agents.map((agent) => <Picker.Item key={agent.id} label={agent.label} value={agent.id} />)}
					</Picker>
				</Host>
				<Host style={styles.menuHost} colorScheme={scheme}>
					<Picker selectedValue={modelSelection} onValueChange={onSelectModel} appearance="menu">
						<Picker.Item label={modelLabel} value="__auto__" />
						{models.map((model) => <Picker.Item key={model.id} label={model.label} value={model.id} />)}
					</Picker>
				</Host>
			</View>
			<Host style={styles.spawnHost} colorScheme={scheme}>
				<Button label={busy ? "Starting…" : "Start task"} variant="filled" onPress={onSpawn} disabled={disabled} style={styles.spawnButton} />
			</Host>
		</View>
	);
}

// Every control is a full touch target tall; the rows take their height from it.
const styles = StyleSheet.create({
	stack: { gap: space.xs },
	destinationRow: { flexDirection: "row", alignItems: "center", gap: space.xs },
	destinationHost: { flex: 1, minWidth: 0, height: touchTarget },
	rail: { flexDirection: "row", alignItems: "center", gap: space.xs },
	iconHost: { width: touchTarget, height: touchTarget },
	iconButton: { width: touchTarget, height: touchTarget, borderRadius: radius.xl },
	menuHost: { flex: 1, minWidth: 0, height: touchTarget },
	spawnHost: { width: "100%", height: touchTarget },
	spawnButton: { width: "100%", height: touchTarget, borderRadius: radius.lg },
});
