import { HStack, Image, Label, Menu, Section, Text, Toggle } from "@expo/ui/swift-ui";
import {
	accessibilityIdentifier,
	accessibilityLabel,
	backgroundOverlay,
	buttonStyle,
	clipShape,
	contentShape,
	disabled as disabledModifier,
	font,
	frame,
	lineLimit,
	menuOrder,
	padding,
	shapes,
	tint,
	truncationMode,
} from "@expo/ui/swift-ui/modifiers";
import type { ReactNode } from "react";
import { COMPOSER_DISC } from "./chat/composerStyles";
import { haptics } from "./haptics";
import { effortGaugeSymbol, type EffortChoice } from "./modelEffort";
import type { IOSSymbol } from "./symbols";
import { useTheme } from "./ThemeProvider";
import { space, touchTarget } from "./tokens";

/*
 * The controls row both composers draw, the new task's and the chat's, so the
 * two cannot drift apart: who runs the turn in a capsule, the effort as a
 * speedometer beside it, and the approval mode as its symbol at the row's end.
 * Each caller fills the capsule's menu with what it can change: the new task
 * every agent and its models, the chat the models of the agent it runs.
 */

/**
 * One option in a menu. A Toggle is the system's checkable menu item: it draws
 * the checkmark and VoiceOver's selected state from `selected` alone, and
 * turning the current choice off again changes nothing.
 */
export function Choice({ label, systemImage, icon, selected, onChoose }: {
	label: string;
	systemImage?: IOSSymbol;
	icon?: ReactNode;
	selected: boolean;
	onChoose: () => void;
}) {
	const onIsOnChange = (on: boolean) => {
		if (!on) return;
		haptics.select();
		onChoose();
	};
	return icon
		? <Toggle isOn={selected} onIsOnChange={onIsOnChange}><Label title={label} icon={icon} /></Toggle>
		: <Toggle isOn={selected} onIsOnChange={onIsOnChange} label={label} systemImage={systemImage} />;
}

/**
 * The model's capsule: the harness's mark, the model's name and the menu
 * arrow, filled, the chat composer's discs' height, inside a full touch target
 * so every point of the label opens the menu.
 */
export function ModelCapsuleLabel({ mark, title }: { mark: ReactNode; title: string }) {
	const t = useTheme();
	return (
		<HStack modifiers={[frame({ minHeight: touchTarget }), contentShape(shapes.rectangle())]}>
			<HStack spacing={space.xs} modifiers={[padding({ horizontal: space.md }), frame({ minHeight: COMPOSER_DISC, maxHeight: COMPOSER_DISC }), backgroundOverlay({ color: t.bgElevated }), clipShape("capsule")]}>
				{mark}
				<Text modifiers={[font({ textStyle: "subheadline", weight: "medium" }), lineLimit(1), truncationMode("tail")]}>{title}</Text>
				<Image systemName="chevron.up.chevron.down" modifiers={[font({ textStyle: "caption2", weight: "semibold" })]} />
			</HStack>
		</HStack>
	);
}

/** The modifiers every trigger in the row shares, and its own name for VoiceOver and UI tests. */
export function useTriggerModifiers({ label, id, disabled }: { label: string; id: string; disabled: boolean }) {
	const t = useTheme();
	return [buttonStyle("plain"), menuOrder("fixed"), tint(t.textSecondary), disabledModifier(disabled), accessibilityLabel(label), accessibilityIdentifier(id)];
}

/**
 * The effort as a speedometer whose needle sits at the chosen level; the menu
 * lists the model's levels with the current one checked. Drawn only for a
 * model that takes an effort.
 */
export function EffortMenu({ choices, onChoose, disabled, id }: { choices: readonly EffortChoice[]; onChoose: (value: string) => void; disabled: boolean; id: string }) {
	const selected = choices.find((choice) => choice.selected);
	return (
		<Menu
			label={<SymbolFace name={effortGaugeSymbol(choices)} />}
			modifiers={useTriggerModifiers({ label: `Effort, ${selected?.label ?? "default"}`, id, disabled })}
		>
			<Section title="Effort">
				{choices.map((choice) => <Choice key={choice.value} label={choice.label} selected={choice.selected} onChoose={() => onChoose(choice.value)} />)}
			</Section>
		</Menu>
	);
}

export type ApprovalChoice = { value: string; label: string; symbol: IOSSymbol; selected: boolean };

/** The approval mode as its symbol alone; the menu and VoiceOver name it. */
export function ApprovalMenu({ title = "Approval", choices, onChoose, disabled, id }: { title?: string; choices: readonly ApprovalChoice[]; onChoose: (value: string) => void; disabled: boolean; id: string }) {
	const selected = choices.find((choice) => choice.selected);
	return (
		<Menu
			label={<SymbolFace name={selected?.symbol ?? "checkmark.shield"} />}
			modifiers={useTriggerModifiers({ label: `${title}, ${selected?.label ?? "Auto"}`, id, disabled })}
		>
			<Section title={title}>
				{choices.map((choice) => <Choice key={choice.value} label={choice.label} systemImage={choice.symbol} selected={choice.selected} onChoose={() => onChoose(choice.value)} />)}
			</Section>
		</Menu>
	);
}

/** A symbol alone as a trigger's face, a full touch target square. */
function SymbolFace({ name }: { name: IOSSymbol }) {
	return <Image systemName={name} modifiers={[font({ textStyle: "subheadline", weight: "medium" }), frame({ width: touchTarget, height: touchTarget }), contentShape(shapes.rectangle())]} />;
}
