import { memo, useEffect, useState } from "react";
import { StyleSheet, Text, View } from "react-native";
import { Pressable } from "react-native-gesture-handler";
import type { WheelAgent, WheelSetting } from "./chat/useChatModelWheel";
import { ComposerOverlay } from "./composer-overlay";
import { useWarmAfterIdle } from "./effort-slider-host";
import { haptics } from "./haptics";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { radius, space, type } from "./tokens";
import { useEffortSliderOpen, type EffortSliderSwitch } from "./useEffortSlider";
import { WHEEL_ROW, WHEEL_ROWS, WheelPicker } from "./wheel-picker";

/*
 * The model, picked on two wheels over the keyboard, as iOS's date picker
 * picks the time: the agent on the left, its models on the right, the pair in
 * force in one band across both. A wheel chooses where it comes to rest. The
 * provider's other settings are rows under the wheels.
 */

export type ModelWheelProps = {
	agents: readonly WheelAgent[];
	chosenAgent: string;
	chosenModel?: string;
	settings: readonly WheelSetting[];
	agentsLocked: boolean;
	choose: (agentId: string, modelKey: string) => void;
	toggleSetting: (id: string) => void;
};

/**
 * The wheels over the composer, wired to its switch as the effort slider is:
 * built hidden once the screen is idle, revealed on open, closed by a tap
 * outside, back, or the keyboard. No timeout: a list to read.
 */
export const ModelWheelHost = memo(function ModelWheelHost({ slider, busy, ...wheel }: ModelWheelProps & { slider: EffortSliderSwitch; busy: boolean }) {
	const { open } = useEffortSliderOpen(wheel.agents.length ? [{ selected: true }] : [], busy, undefined, slider, null);
	const warm = useWarmAfterIdle();
	if (!(open || warm) || !wheel.agents.length) return null;
	return (
		<ComposerOverlay overKeyboard visible={open} onDismiss={() => slider.setOpen(false)} fill>
			<ModelWheel {...wheel} />
		</ComposerOverlay>
	);
});

export function ModelWheel({ agents, chosenAgent, chosenModel, settings, agentsLocked, choose, toggleSetting }: ModelWheelProps) {
	const styles = useThemedStyles(makeStyles);
	const chosenAgentIndex = Math.max(0, agents.findIndex((agent) => agent.id === chosenAgent));
	// The agent the left wheel rests on: it leads the right wheel's list.
	const [agentIndex, setAgentIndex] = useState(chosenAgentIndex);
	useEffect(() => { setAgentIndex(chosenAgentIndex); }, [chosenAgentIndex]);
	const agent = agents[Math.min(agentIndex, agents.length - 1)];
	const modelIndex = agent.id === chosenAgent ? Math.max(0, agent.models.findIndex((model) => model.key === chosenModel)) : 0;

	return (
		<View style={styles.panel} testID="model-wheel">
			<View style={styles.wheels}>
				{/* The band the chosen pair sits in, across both wheels. */}
				<View pointerEvents="none" style={styles.band} />
				{agents.length > 1 ? (
					<View style={styles.agentColumn}>
						<WheelPicker
							items={agents.map((item) => ({ key: item.id, label: item.label, mark: item.id }))}
							index={agentIndex}
							disabled={agentsLocked}
							align="right"
							testID="model-wheel-agents"
							onSettle={(index) => {
								const next = agents[index];
								if (!next || index === agentIndex) return;
								setAgentIndex(index);
								// The running agent rests on its own model; another starts on
								// its first, its default, unless it is the switch already picked.
								const first = next.id === chosenAgent ? chosenModel ?? next.models[0]?.key : next.models[0]?.key;
								if (first !== undefined) choose(next.id, first);
							}}
						/>
					</View>
				) : null}
				<View style={styles.modelColumn}>
					<WheelPicker
						items={agent.models}
						index={modelIndex}
						align={agents.length > 1 ? "left" : "center"}
						testID="model-wheel-models"
						onSettle={(index) => {
							const model = agent.models[index];
							if (model) choose(agent.id, model.key);
						}}
					/>
				</View>
			</View>
			{settings.length ? (
				<View style={styles.settings}>
					{settings.map((setting) => (
						<Pressable
							key={setting.id}
							onPress={() => { haptics.select(); toggleSetting(setting.id); }}
							accessibilityRole={setting.kind === "boolean" ? "switch" : "button"}
							accessibilityState={setting.kind === "boolean" ? { checked: setting.on } : undefined}
							accessibilityLabel={setting.kind === "boolean" ? setting.label : `${setting.label}, ${setting.value}`}
							testID={`model-wheel-setting-${setting.id}`}
							style={({ pressed }) => [styles.setting, pressed && styles.settingPressed]}
						>
							<Text style={styles.settingLabel} numberOfLines={1}>{setting.label}</Text>
							{setting.kind === "boolean" ? (
								<View style={[styles.toggle, setting.on && styles.toggleOn]}>
									<View style={[styles.knob, setting.on && styles.knobOn]} />
								</View>
							) : (
								<Text style={styles.settingValue} numberOfLines={1}>{setting.value}</Text>
							)}
						</Pressable>
					))}
				</View>
			) : null}
		</View>
	);
}

const TOGGLE_WIDTH = 40;
const TOGGLE_HEIGHT = 24;
const KNOB = TOGGLE_HEIGHT - 6;

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		panel: { alignSelf: "stretch", gap: space.sm },
		wheels: { flexDirection: "row", height: WHEEL_ROW * WHEEL_ROWS, gap: space.md },
		band: {
			position: "absolute",
			left: 0,
			right: 0,
			top: WHEEL_ROW * Math.floor(WHEEL_ROWS / 2) - 2,
			height: WHEEL_ROW + 4,
			borderRadius: radius.lg,
			borderCurve: "continuous",
			backgroundColor: t.bgElevated,
		},
		// Model names run longer than agents' ("Demo Large · Default").
		agentColumn: { flex: 1 },
		modelColumn: { flex: 1.6 },
		settings: { gap: space.hair },
		setting: {
			flexDirection: "row",
			alignItems: "center",
			justifyContent: "space-between",
			minHeight: 44,
			paddingHorizontal: space.md,
			borderRadius: radius.lg,
			borderCurve: "continuous",
		},
		settingPressed: { backgroundColor: t.bgSubtle },
		settingLabel: { ...type.subheadline, color: t.textPrimary, flexShrink: 1 },
		settingValue: { ...type.subheadline, color: t.textSecondary },
		toggle: { width: TOGGLE_WIDTH, height: TOGGLE_HEIGHT, borderRadius: TOGGLE_HEIGHT / 2, backgroundColor: t.bgElevatedHover, justifyContent: "center", paddingHorizontal: 3 },
		toggleOn: { backgroundColor: t.accentBorder },
		knob: { width: KNOB, height: KNOB, borderRadius: KNOB / 2, backgroundColor: t.textSecondary },
		knobOn: { alignSelf: "flex-end", backgroundColor: "#ffffff" },
	});
