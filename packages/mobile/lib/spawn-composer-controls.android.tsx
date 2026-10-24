import { Feather } from "./icons";
import { useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { AgentLogo } from "./AgentLogo";
import { useTheme } from "./ThemeProvider";
import type { Theme } from "./theme";
import type { SpawnComposerControlsProps } from "./spawn-composer-controls.types";
import { PickerOptions } from "./PickerOptions";
import { type, space } from "./tokens";
import { MicKey } from "./voice/MicKey";
import { sourceKey } from "./environment/scopedBoard";

type OpenMenu = "destination" | "project" | "harness" | "model" | null;

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
	showAttachments = true,
	showModels = true,
	voice,
	onSpawn,
	submitLabel = "Start task",
	busy,
	disabled,
}: SpawnComposerControlsProps) {
	const t = useTheme();
	const styles = makeStyles(t);
	const [openMenu, setOpenMenu] = useState<OpenMenu>(null);
	const selectedProject = projects.find((project) => project.id === projectId);
	const projectLabel = selectedProject?.label ?? "Choose project";
	const harnessLabel = agents.find((agent) => agent.id === harness)?.label ?? "Choose harness";
	const modelOptions = [{ id: "__auto__", label: "Automatic" }, ...models];
	const destinationOptions = destinations.map((option) => ({ id: sourceKey(option.source), label: option.available ? option.label : `${option.label} · Unavailable` }));
	const destinationLabel = destinations.find((option) => destination && sourceKey(option.source) === sourceKey(destination))?.label ?? "Choose where to run";
	const options = openMenu === "destination" ? destinationOptions : openMenu === "project" ? projects : openMenu === "harness" ? agents : modelOptions;
	const selectedValue = openMenu === "destination" ? (destination ? sourceKey(destination) : "") : openMenu === "project" ? (projectId ?? "") : openMenu === "harness" ? harness : modelSelection;
	const menuTitle = openMenu === "destination" ? "Destination" : openMenu === "project" ? "Project" : openMenu === "harness" ? "Agent" : "Model";

	const selectOption = (value: string) => {
		if (openMenu === "destination") {
			const selected = destinations.find((option) => sourceKey(option.source) === value);
			if (!selected?.available) return;
			onSelectDestination(selected.source);
		}
		if (openMenu === "project") onSelectProject(value);
		if (openMenu === "harness") onSelectHarness(value);
		if (openMenu === "model") onSelectModel(value);
		setOpenMenu(null);
	};

	// Choices replace the controls inside Spawn's own sheet. They used to open a
	// second sheet on top of it, so picking a project, agent and model stacked
	// three sheets deep.
	if (openMenu) {
		return (
			<PickerOptions
				title={menuTitle}
				options={options}
				selectedValue={selectedValue}
				showAgentLogos={openMenu === "harness"}
				onSelect={selectOption}
				onBack={() => setOpenMenu(null)}
			/>
		);
	}

	return (
		<View style={styles.stack}>
			<View style={styles.destinationRow}>
				<SelectorButton label={destinationLabel} icon={destination?.kind === "cloud" ? "cloud" : "monitor"} onPress={() => setOpenMenu("destination")} style={styles.destinationButton} />
				<SelectorButton label={projectLabel} icon={selectedProject?.icon ?? "folder"} onPress={() => setOpenMenu("project")} style={styles.destinationButton} />
			</View>

			<View style={styles.rail}>
				{showAttachments ? <>
					<Pressable
						accessibilityRole="button"
						accessibilityLabel="Attach a file"
						android_ripple={{ color: t.accentTint, borderless: true, radius: 20 }}
						onPress={onAttach}
						style={styles.attach}
					>
						<Feather name="paperclip" size={20} color={t.textSecondary} />
					</Pressable>
					<View style={styles.divider} />
				</> : null}
				<SelectorButton label={harnessLabel} icon="terminal" harness={harness} onPress={() => setOpenMenu("harness")} style={styles.railButton} />
				{showModels ? <>
					<View style={styles.divider} />
					<SelectorButton label={modelLabel} onPress={() => setOpenMenu("model")} style={styles.railButton} />
				</> : null}
				<View style={styles.divider} />
				{/* Plain, like the paperclip: the rail is the surface, and a second
				    disc inside it would compete with Start task. */}
				<MicKey
					variant="plain"
					size={42}
					glyphSize={20}
					state={voice.state}
					mode={voice.mode}
					onPressIn={voice.onPressIn}
					onPressOut={voice.onPressOut}
				/>
			</View>

			<Pressable
				accessibilityRole="button"
				accessibilityLabel={busy ? "Starting task" : submitLabel}
				accessibilityState={{ disabled }}
				testID="spawn-submit"
				disabled={disabled}
				android_ripple={{ color: "rgba(255,255,255,0.18)" }}
				onPress={onSpawn}
				style={[styles.spawn, { opacity: disabled ? 0.68 : 1 }]}
			>
				{busy ? <ActivityIndicator size="small" color={t.onAccent} /> : null}
				<Text style={styles.spawnLabel}>{busy ? "Starting…" : submitLabel}</Text>
			</Pressable>

		</View>
	);
}

function SelectorButton({ label, icon, harness, onPress, style }: {
	label: string;
	icon?: keyof typeof Feather.glyphMap;
	harness?: string;
	onPress: () => void;
	style?: object;
}) {
	const t = useTheme();
	const styles = makeStyles(t);
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={label}
			android_ripple={{ color: t.accentTint }}
			onPress={onPress}
			style={[styles.selector, style]}
		>
			{harness ? <AgentLogo harness={harness} size={20} /> : icon ? <Feather name={icon} size={15} color={t.textSecondary} /> : null}
			<Text numberOfLines={1} style={styles.selectorLabel}>{label}</Text>
			<Feather name="chevron-down" size={15} color={t.textTertiary} />
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	stack: { gap: space.sm },
	destinationRow: { minHeight: 36, flexDirection: "row", alignItems: "center", gap: space.xs },
	destinationButton: { flex: 1, minWidth: 0, height: 36, paddingHorizontal: space.sm, backgroundColor: "transparent" },
	rail: {
		height: 52,
		flexDirection: "row",
		alignItems: "center",
		paddingHorizontal: space.xxs,
		borderRadius: 16,
		borderCurve: "continuous",
		backgroundColor: t.bgElevated,
		borderWidth: StyleSheet.hairlineWidth,
		borderColor: t.borderDefault,
		overflow: "hidden",
	},
	attach: { width: 42, height: 42, borderRadius: 20, alignItems: "center", justifyContent: "center", overflow: "hidden" },
	divider: { width: StyleSheet.hairlineWidth, height: 24, backgroundColor: t.borderDefault },
	selector: { minHeight: 36, flexDirection: "row", alignItems: "center", gap: space.xs, borderRadius: 12, overflow: "hidden" },
	railButton: { flex: 1, minWidth: 0, height: 42, paddingHorizontal: space.sm },
	selectorLabel: { fontFamily: "Geist_600SemiBold", flexShrink: 1, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
	spawn: { height: 44, flexDirection: "row", gap: space.sm, borderRadius: 16, borderCurve: "continuous", alignItems: "center", justifyContent: "center", overflow: "hidden", backgroundColor: t.accent },
	spawnLabel: { fontFamily: "Geist_600SemiBold", color: t.onAccent, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight, fontWeight: "600" },
});
