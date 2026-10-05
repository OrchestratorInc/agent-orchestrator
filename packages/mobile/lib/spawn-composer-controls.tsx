import { Button, Host, Picker } from "@expo/ui";
import { StyleSheet, View } from "react-native";
import { useTheme, useThemeState } from "./ThemeProvider";
import type { SpawnComposerControlsProps } from "./spawn-composer-controls.types";
import { space } from "./tokens";
import { sourceKey } from "./environment/scopedBoard";

export function SpawnComposerControls({
	destinations,
	destination,
	onSelectDestination,
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
	onAttach,
	onSpawn,
	busy,
	disabled,
}: SpawnComposerControlsProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	return (
		<View style={styles.stack}>
			<View style={styles.selectorRow}>
				<Host style={styles.destinationHost} colorScheme={scheme} seedColor={t.accent}>
					<Picker selectedValue={destination ? sourceKey(destination) : ""} onValueChange={(key) => { const next = destinations.find((option) => sourceKey(option.source) === key); if (next?.available) onSelectDestination(next.source); }} appearance="menu">
						<Picker.Item label="Run on · Choose destination" value="" />
						{destinations.map((option) => <Picker.Item key={sourceKey(option.source)} label={`Run on ${option.label}${option.available ? "" : " · Unavailable"}`} value={sourceKey(option.source)} />)}
					</Picker>
				</Host>
				<Host style={styles.projectHost} colorScheme={scheme} seedColor={t.accent}>
					<Picker selectedValue={projectId ?? ""} onValueChange={onSelectProject} appearance="menu">
						<Picker.Item label="Choose project" value="" />
						{projects.map((project) => <Picker.Item key={project.id} label={project.label} value={project.id} />)}
					</Picker>
				</Host>
			</View>
			<View style={styles.rail}>
				<Host style={styles.iconHost} colorScheme={scheme} seedColor={t.accent}>
					<Button label="📎" variant="text" onPress={onAttach} style={styles.iconButton} />
				</Host>
				<Host style={styles.menuHost} colorScheme={scheme} seedColor={t.accent}>
					<Picker selectedValue={harness} onValueChange={onSelectHarness} appearance="menu">
						{agents.map((agent) => <Picker.Item key={agent.id} label={agent.label} value={agent.id} />)}
					</Picker>
				</Host>
				<Host style={styles.menuHost} colorScheme={scheme} seedColor={t.accent}>
					<Picker selectedValue={modelSelection} onValueChange={onSelectModel} appearance="menu">
						<Picker.Item label={modelLabel} value="__auto__" />
						{models.map((model) => <Picker.Item key={model.id} label={model.label} value={model.id} />)}
					</Picker>
				</Host>
			</View>
			<Host style={styles.spawnHost} colorScheme={scheme} seedColor={t.accent}>
				<Button label={busy ? "Starting…" : "Start task"} variant="filled" onPress={onSpawn} disabled={disabled} style={styles.spawnButton} />
			</Host>
		</View>
	);
}

const styles = StyleSheet.create({
	stack: { gap: space.hair },
	selectorRow: { flexDirection: "row", alignItems: "center", gap: space.sm },
	destinationHost: { flex: 2, minWidth: 0, height: 36 },
	projectHost: { flex: 3, minWidth: 0, height: 36 },
	rail: { minHeight: 52, flexDirection: "row", alignItems: "center", gap: space.xs },
	iconHost: { width: 44, height: 44 },
	iconButton: { width: 44, height: 44, borderRadius: 20 },
	menuHost: { flex: 1, minWidth: 0, height: 44 },
	spawnHost: { width: "100%", height: 44 },
	spawnButton: { width: "100%", height: 44, borderRadius: 16 },
});
