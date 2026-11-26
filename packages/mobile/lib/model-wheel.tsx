import { memo, useCallback, useEffect, useRef, useState } from "react";
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
 * force in one band across both. The pair they stand on as they close is
 * chosen. The provider's other settings are rows under the wheels.
 */

export type ModelWheelProps = {
	agents: readonly WheelAgent[];
	chosenAgent: string;
	chosenModel?: string;
	/** The agent the chat runs now and its model, which the wheels return to when it is rolled back to. */
	runningAgent?: string;
	runningModel?: string;
	settings: readonly WheelSetting[];
	agentsLocked: boolean;
	choose: (agentId: string, modelKey: string) => void;
	toggleSetting: (id: string) => void;
};

/** Where the wheels stand: an agent and one of its models. */
export type WheelPosition = { agent: string; model: string };

/**
 * The wheels over the composer, wired to its switch as the effort slider is:
 * built hidden once the screen is idle, revealed on open, closed by a tap
 * outside, back, or the keyboard. No timeout: a list to read.
 *
 * The wheels only look while they are open: where they stand is kept here,
 * and chosen once, as the wheels close (`choose` does nothing for the pair
 * already in force). Choosing at every stop saved a model change, which
 * resets the effort and Fast mode, for each model rolled past on the way, and
 * recorded an agent switch for every agent looked at.
 */
export const ModelWheelHost = memo(function ModelWheelHost({ slider, busy, ...wheel }: ModelWheelProps & { slider: EffortSliderSwitch; busy: boolean }) {
	const moved = useRef<WheelPosition | undefined>(undefined);
	const chooseRef = useRef(wheel.choose);
	chooseRef.current = wheel.choose;
	const commit = useCallback(() => {
		const at = moved.current;
		moved.current = undefined;
		if (at) chooseRef.current(at.agent, at.model);
	}, []);
	const { open } = useEffortSliderOpen(wheel.agents.length ? [{ selected: true }] : [], busy, commit, slider, null);
	// As the effort slider's host: a request card replacing the composer must
	// not leave the wheels to reopen by themselves when the card goes.
	useEffect(() => () => slider.setOpen(false), [slider]);
	const onMove = useCallback((at: WheelPosition) => { moved.current = at; }, []);
	const warm = useWarmAfterIdle();
	if (!(open || warm) || !wheel.agents.length) return null;
	return (
		<ComposerOverlay overKeyboard visible={open} onDismiss={() => slider.setOpen(false)} fill>
			<ModelWheel {...wheel} visible={open} onMove={onMove} />
		</ComposerOverlay>
	);
});

/** The model an agent starts on: its starred default, else its first. */
function defaultModel(agent: WheelAgent): string | undefined {
	return (agent.models.find((model) => model.starred) ?? agent.models[0])?.key;
}

export function ModelWheel({ agents, chosenAgent, chosenModel, runningAgent, runningModel, settings, toggleSetting, agentsLocked, visible = true, onMove }: Omit<ModelWheelProps, "choose"> & {
	visible?: boolean;
	/** The wheels came to rest here. */
	onMove: (at: WheelPosition) => void;
}) {
	const styles = useThemedStyles(makeStyles);
	const chosen = { agent: chosenAgent, model: chosenModel ?? "" };
	const [at, setAt] = useState<WheelPosition>(chosen);
	// Back where the chat stands each time the wheels are put away, and when
	// it changes from outside while they have not been turned.
	const turned = useRef(false);
	useEffect(() => {
		if (visible && turned.current) return;
		turned.current = false;
		setAt({ agent: chosenAgent, model: chosenModel ?? "" });
	}, [chosenAgent, chosenModel, visible]);
	const move = (next: WheelPosition) => {
		turned.current = true;
		setAt(next);
		onMove(next);
	};
	// An agent not in the list (one with no models to show) leaves the agent
	// wheel on its first row without that standing for a choice: nothing is
	// chosen until a wheel is turned.
	const agentIndex = Math.max(0, agents.findIndex((agent) => agent.id === at.agent));
	const agent = agents[Math.min(agentIndex, agents.length - 1)];
	const found = agent.models.findIndex((model) => model.key === at.model);
	const modelIndex = found >= 0 ? found : Math.max(0, agent.models.findIndex((model) => model.key === defaultModel(agent)));

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
							accessibilityLabel="Agent"
							testID="model-wheel-agents"
							onSettle={(index) => {
								const next = agents[index];
								if (!next || next.id === agent.id) return;
								// The pair in force rests on its model, the running agent on
								// its own; another starts on its default.
								const model = next.id === chosenAgent && chosenModel !== undefined
									? chosenModel
									: next.id === runningAgent && runningModel !== undefined ? runningModel : defaultModel(next);
								if (model !== undefined) move({ agent: next.id, model });
							}}
						/>
					</View>
				) : null}
				<View style={styles.modelColumn}>
					{/* Keyed by agent: a settle still pending on the last agent's list
					    is dropped with it rather than landing on the new agent. */}
					<WheelPicker
						key={agent.id}
						items={agent.models}
						index={modelIndex}
						align={agents.length > 1 ? "left" : "center"}
						accessibilityLabel="Model"
						testID="model-wheel-models"
						onSettle={(index) => {
							const model = agent.models[index];
							if (model) move({ agent: agent.id, model: model.key });
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
