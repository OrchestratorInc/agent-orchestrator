import { memo, useCallback, useEffect, useRef, useState } from "react";
import { ScrollView, StyleSheet, Text, View } from "react-native";
import { Pressable } from "react-native-gesture-handler";
import { AgentLogo } from "./AgentLogo";
import type { WheelAgent, WheelSetting } from "./chat/useChatModelWheel";
import { COMPOSER_CONTROL_RISE, ComposerOverlay } from "./composer-overlay";
import { useWarmAfterIdle } from "./effort-slider-host";
import { haptics } from "./haptics";
import type { Theme } from "./theme";
import { useThemedStyles } from "./ThemeProvider";
import { radius, space, type } from "./tokens";
import { useEffortSliderOpen, type EffortSliderSwitch } from "./useEffortSlider";
import { WHEEL_ROW, WHEEL_ROWS, WheelPicker } from "./wheel-picker";

/*
 * The model, picked over the keyboard: the agents as a row of buttons, one tap
 * each (desktop's logo rail), and the agent's models on a wheel under them, as
 * iOS's date picker picks the time. Two wheels side by side made switching
 * agent a fiddly turn that also turned the models; a button is one tap. The
 * pair they stand on as they close is chosen. The provider's other settings
 * are rows under the wheel.
 */

/**
 * How far the wheel reaches down over the keys for its band to sit on the
 * composer's control row, level with the pill it opens from.
 */
const WHEEL_KEYS_OVERLAP = (WHEEL_ROW * WHEEL_ROWS) / 2 - COMPOSER_CONTROL_RISE;

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
		<ComposerOverlay overKeyboard keysOverlap={WHEEL_KEYS_OVERLAP} visible={open} onDismiss={() => slider.setOpen(false)} fill>
			<ModelWheel {...wheel} visible={open} onMove={onMove} onEmptyTap={() => slider.setOpen(false)} />
		</ComposerOverlay>
	);
});

/** The model an agent starts on: its starred default, else its first. */
function defaultModel(agent: WheelAgent): string | undefined {
	return (agent.models.find((model) => model.starred) ?? agent.models[0])?.key;
}

export function ModelWheel({ agents, chosenAgent, chosenModel, runningAgent, runningModel, settings, toggleSetting, agentsLocked, visible = true, onMove, onEmptyTap }: Omit<ModelWheelProps, "choose"> & {
	visible?: boolean;
	/** The wheels came to rest here. */
	onMove: (at: WheelPosition) => void;
	/** A tap on a wheel's empty rows, which read as the chat around the wheels. */
	onEmptyTap?: () => void;
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
	// A wheel still coasting as the wheels close settles after they are hidden;
	// that is no choice, and kept would be chosen on the next close.
	const shown = useRef(visible);
	shown.current = visible;
	const move = (next: WheelPosition) => {
		if (!shown.current) return;
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
			{agents.length > 1 ? (
				<AgentRail
					agents={agents}
					chosen={agent.id}
					locked={agentsLocked}
					visible={visible}
					onPick={(next) => {
						if (next.id === agent.id) return;
						// The pair in force rests on its model, the running agent on
						// its own; another starts on its default.
						const model = next.id === chosenAgent && chosenModel !== undefined
							? chosenModel
							: next.id === runningAgent && runningModel !== undefined ? runningModel : defaultModel(next);
						if (model !== undefined) move({ agent: next.id, model });
					}}
				/>
			) : null}
			<View style={styles.wheels}>
				{/* The band the chosen model sits in. */}
				<View pointerEvents="none" style={styles.band} />
				<View style={styles.modelColumn}>
					{/* Keyed by agent: a settle still pending on the last agent's list
					    is dropped with it rather than landing on the new agent. */}
					<WheelPicker
						key={agent.id}
						items={agent.models}
						index={modelIndex}
						align="center"
						accessibilityLabel="Model"
						testID="model-wheel-models"
						onEmptyTap={onEmptyTap}
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

/**
 * The agents as buttons, the one the wheel lists filled: a tap picks it. Held
 * still while the agent is working (a switch waits until it is idle).
 */
function AgentRail({ agents, chosen, locked, visible, onPick }: { agents: readonly WheelAgent[]; chosen: string; locked: boolean; visible: boolean; onPick: (agent: WheelAgent) => void }) {
	const styles = useThemedStyles(makeStyles);
	const rail = useRef<ScrollView>(null);
	// The chosen agent in view each time the picker opens, however far along
	// the row; never while it is open, so a button never slides away from the
	// finger that tapped it.
	const placed = useRef<Record<string, number>>({});
	const chosenNow = useRef(chosen);
	chosenNow.current = chosen;
	const reveal = () => {
		const x = placed.current[chosenNow.current];
		if (x !== undefined) rail.current?.scrollTo({ x: Math.max(0, x - space.xl), animated: false });
	};
	useEffect(() => { if (visible) reveal(); }, [visible]); // eslint-disable-line react-hooks/exhaustive-deps
	return (
		<ScrollView
			ref={rail}
			horizontal
			showsHorizontalScrollIndicator={false}
			keyboardShouldPersistTaps="always"
			contentContainerStyle={styles.rail}
			accessibilityRole="tablist"
			accessibilityLabel="Agent"
			testID="model-wheel-agents"
		>
			{agents.map((item) => {
				const on = item.id === chosen;
				return (
					<Pressable
						key={item.id}
						disabled={locked && !on}
						onLayout={(event) => {
							const first = placed.current[item.id] === undefined;
							placed.current[item.id] = event.nativeEvent.layout.x;
							if (first && on) reveal();
						}}
						onPress={() => { if (!on) { haptics.select(); onPick(item); } }}
						accessibilityRole="tab"
						accessibilityState={{ selected: on, disabled: locked && !on }}
						accessibilityLabel={item.label}
						testID={`model-wheel-agent-${item.id}`}
						style={({ pressed }) => [styles.agent, on && styles.agentOn, pressed && !on && styles.agentPressed, locked && !on && styles.agentLocked]}
					>
						<AgentLogo harness={item.id} size={AGENT_MARK} />
						<Text style={[styles.agentLabel, on && styles.agentLabelOn]} numberOfLines={1}>{item.label}</Text>
					</Pressable>
				);
			})}
		</ScrollView>
	);
}

/** An agent's mark on its button. */
const AGENT_MARK = 16;
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
		modelColumn: { flex: 1 },
		rail: { gap: space.xs, paddingHorizontal: space.xs },
		agent: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.xs,
			height: 36,
			paddingHorizontal: space.md,
			borderRadius: radius.pill,
			borderCurve: "continuous",
		},
		agentOn: { backgroundColor: t.bgElevated },
		agentPressed: { backgroundColor: t.bgSubtle },
		agentLocked: { opacity: 0.4 },
		agentLabel: { ...type.subheadline, color: t.textSecondary },
		agentLabelOn: { color: t.textPrimary },
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
