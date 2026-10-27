import { Host } from "@expo/ui";
import { Asset } from "expo-asset";
import { Button, Divider, HStack, Image, Label, Menu, ProgressView, Section, Spacer, Text, Toggle, VStack } from "@expo/ui/swift-ui";
import {
	accessibilityIdentifier,
	accessibilityLabel,
	backgroundOverlay,
	buttonStyle,
	clipShape,
	contentShape,
	disabled as disabledModifier,
	font,
	foregroundStyle,
	frame,
	layoutPriority,
	lineLimit,
	menuOrder,
	padding,
	shapes,
	tint,
	truncationMode,
} from "@expo/ui/swift-ui/modifiers";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { StyleSheet } from "react-native";
import { hasLogo, logoKey } from "./harnessLogo";
import { logoFor } from "./harnessLogoAssets";
import { haptics } from "./haptics";
import type { SpawnComposerControlsProps, SpawnComposerOption } from "./spawn-composer-controls.types";
import { defaultModelChoice } from "./spawnModel";
import { approvalSymbol } from "./approvalSymbol";
import type { IOSSymbol } from "./symbols";
import { useTheme, useThemeState } from "./ThemeProvider";
import { space, touchTarget } from "./tokens";

// SwiftUI's `.infinity` can't cross the bridge, and any width past the sheet's
// does the same job: take all the room the row offers.
const FILL_WIDTH = 10_000;
// A large bordered button's height, so the attach circle beside Start matches it.
const ACTION_HEIGHT = 50;

/**
 * Spawn's selectors as SwiftUI menus whose options are the system's checkable
 * items, so the current choice carries a real checkmark and selected state
 * rather than a drawn one. The triggers keep their icon and name, size to their
 * content and scale with Dynamic Type.
 */
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
	agentModels,
	modelSelection,
	modelLabel,
	usesDefaultModel,
	onSelectAgentModel,
	onBrowseModels,
	approvalModes,
	approvalMode,
	onSelectApprovalMode,
	onChoosePhoto,
	onChooseFile,
	voice,
	promptEmpty,
	onSpawn,
	busy,
	disabled,
}: SpawnComposerControlsProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const selectedProject = projects.find((project) => project.id === projectId);
	const hostLabel = hosts.find((host) => host.id === hostId)?.label ?? "Choose machine";
	const projectLabel = selectedProject?.label ?? "Choose project";
	const harnessLabel = agents.find((agent) => agent.id === harness)?.label ?? "Choose harness";
	const approvalOption = approvalModes.find((mode) => mode.id === approvalMode);
	// The model the task runs: the one picked or pinned, else the catalog's own
	// default by name, and the agent's name only when the catalog names none.
	const agentTitle = usesDefaultModel ? agentModels[harness]?.defaultLabel ?? harnessLabel : modelLabel;
	const listening = voice.state === "recording" || voice.state === "starting";
	const transcribing = voice.state === "transcribing";
	// Dictating while the mic is busy or the prompt is empty; a device that
	// cannot dictate keeps Start, which says what is missing when pressed.
	const dictates = !busy && (listening || transcribing || (promptEmpty && voice.state !== "unavailable" && voice.state !== "denied"));
	const primaryLabel = busy ? "Starting task" : listening ? "Stop dictating" : transcribing ? "Transcribing" : dictates ? "Dictate the task" : "Start task";
	const primaryDisabled = dictates ? transcribing : disabled;
	// Solid in every state: the accent to press, red while the mic is open, and
	// the attach circle's grey while it cannot be pressed.
	const primaryFill = listening ? t.red : primaryDisabled ? t.bgElevated : t.accent;
	const primaryInk = listening ? t.onDestructive : primaryDisabled ? t.textFaint : t.onAccent;
	// A choice only fires when it changes, so each one is worth the tick.
	const choose = (apply: (id: string) => void, id: string) => {
		haptics.select();
		apply(id);
	};

	return (
		<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.host}>
			<VStack alignment="leading" spacing={space.sm}>
				{/* The task's settings as plain selectors, one line each: where it
				    runs, then what it works on and how freely, then who runs it.
				    Every trigger's touch target carries its own air above and below
				    the text, so the lines need no gap of their own. Compact and
				    left-anchored: a max-width frame would make a Menu fill the row,
				    which centers its popup over the whole sheet. */}
				<VStack alignment="leading" spacing={space.none}>
					<HStack spacing={space.sm}>
						<Menu
							label={<SelectorLabel icon={<SelectorGlyph name="desktopcomputer" />} title={hostLabel} indicator="chevron.up.chevron.down" />}
							modifiers={[buttonStyle("plain"), menuOrder("fixed"), tint(t.textSecondary), accessibilityLabel(`Run on ${hostLabel}`), accessibilityIdentifier("spawn-host")]}
						>
							{hosts.map((host) => <Choice key={host.id} label={host.label} selected={host.id === hostId} onChoose={() => choose(onSelectHost, host.id)} />)}
						</Menu>
						<Spacer minLength={0} />
					</HStack>

					<HStack spacing={space.lg}>
						<Menu
							label={<SelectorLabel icon={<SelectorGlyph name={projectSystemImage(selectedProject)} />} title={projectLabel} indicator="chevron.up.chevron.down" />}
							modifiers={[buttonStyle("plain"), menuOrder("fixed"), tint(t.textSecondary), accessibilityIdentifier("spawn-project")]}
						>
							{projects.flatMap((project) => [
								project.sectionBreakBefore ? <Divider key={`${project.id}-break`} /> : null,
								<Choice key={project.id} label={project.label} systemImage={projectSystemImage(project)} selected={project.id === projectId} onChoose={() => choose(onSelectProject, project.id)} />,
							])}
						</Menu>
						{/* What the worker may do before it asks: its own control, so it
						    is never read as part of the model. Short enough that the
						    project's name gives way first. */}
						<Menu
							label={<SelectorLabel icon={<SelectorGlyph name={approvalSymbol(approvalMode, harness)} />} title={approvalOption?.shortLabel ?? approvalOption?.label ?? "Auto"} indicator="chevron.up.chevron.down" priority={1} />}
							modifiers={[
								buttonStyle("plain"),
								menuOrder("fixed"),
								layoutPriority(1),
								tint(t.textSecondary),
								accessibilityLabel(`Approval, ${approvalOption?.label ?? "Auto"}`),
								accessibilityIdentifier("spawn-approval"),
							]}
						>
							<Section title="Approval">
								{approvalModes.map((mode) => (
									<Choice key={mode.id} label={mode.label} systemImage={approvalSymbol(mode.id, harness)} selected={mode.id === approvalMode} onChoose={() => choose(onSelectApprovalMode, mode.id)} />
								))}
							</Section>
						</Menu>
						<Spacer minLength={0} />
					</HStack>

				</VStack>

				{/* The composer's own line: who runs the task at the start, then
				    attach and the one round button that sends it, side by side. The
				    harness's mark and the model it runs (the harness's name only when
				    the catalog names no default); each agent in its menu opens one
				    level down onto its models. */}
				<HStack spacing={space.sm}>
					<Menu
						label={
							// Enclosed, as the circles beside it are: one capsule of
							// their height holding the harness's mark and the model.
							<HStack spacing={space.xs} modifiers={[padding({ horizontal: space.md }), frame({ minHeight: ACTION_HEIGHT, maxHeight: ACTION_HEIGHT }), backgroundOverlay({ color: t.bgElevated }), clipShape("capsule"), contentShape(shapes.rectangle())]}>
								<HarnessImage harness={harness} />
								<SelectorText>{agentTitle}</SelectorText>
								<SelectorGlyph name="chevron.up.chevron.down" indicator />
							</HStack>
						}
						modifiers={[
							buttonStyle("plain"),
							menuOrder("fixed"),
							tint(t.textSecondary),
							accessibilityLabel(`${harnessLabel}, ${modelLabel}`),
							accessibilityIdentifier("spawn-harness"),
						]}
					>
						{agents.map((agent) => (
							<Menu
								key={agent.id}
								label={<Label title={agent.label} icon={<HarnessImage harness={agent.id} />} />}
								modifiers={[accessibilityIdentifier(`spawn-model-${agent.id}`)]}
							>
								{/* The default first, named when the catalog says which it
								    is; then the first few models; then the whole catalog. */}
								{[{ id: "__auto__", label: defaultModelChoice(agentModels[agent.id]?.defaultLabel) }, ...(agentModels[agent.id]?.models ?? [])].map((model) => (
									<Choice
										key={model.id}
										label={model.label}
										selected={agent.id === harness && model.id === modelSelection}
										onChoose={() => choose((id) => onSelectAgentModel(agent.id, id), model.id)}
									/>
								))}
								{(agentModels[agent.id]?.total ?? 0) > (agentModels[agent.id]?.models.length ?? 0) ? (
									<Section>
										<Button label={`All models (${agentModels[agent.id]!.total.toLocaleString()})`} systemImage="magnifyingglass" onPress={() => onBrowseModels(agent.id)} />
									</Section>
								) : null}
							</Menu>
						))}
					</Menu>

					<Spacer minLength={0} />

					<Menu
						label={
							<Image
								systemName="paperclip"
								modifiers={[
									font({ textStyle: "body", weight: "medium" }),
									foregroundStyle(t.textPrimary),
									frame({ width: ACTION_HEIGHT, height: ACTION_HEIGHT }),
									backgroundOverlay({ color: t.bgElevated }),
									clipShape("circle"),
									contentShape(shapes.rectangle()),
								]}
							/>
						}
						modifiers={[buttonStyle("plain"), menuOrder("fixed"), disabledModifier(busy), accessibilityLabel("Attach"), accessibilityIdentifier("spawn-attachment")]}
					>
						<Button label="Choose Photo" systemImage="photo" onPress={onChoosePhoto} />
						<Button label="Choose File" systemImage="doc" onPress={onChooseFile} />
					</Menu>


					{/* One round button that follows the prompt: with text it starts the
					    task, with none it dictates, and while the mic is open it stops,
					    in the recording red. A button only taps, so dictation here is
					    hands-free: tap, speak, tap again. The success and failure
					    haptics come from the request itself, so the press adds none. */}
					<Button
						onPress={dictates ? voice.onToggleLatched : onSpawn}
						testID={dictates ? "spawn-dictate" : "spawn-submit"}
						modifiers={[
							buttonStyle("plain"),
							disabledModifier(primaryDisabled),
							accessibilityLabel(primaryLabel),
							accessibilityIdentifier(dictates ? "spawn-dictate" : "spawn-submit"),
						]}
					>
						{/* Drawn as a fixed disc, the attach circle's size: a system
						    round button sizes itself to its glyph, so the arrow, the mic,
						    the stop square and the spinner each made it a different size. */}
						<HStack modifiers={[frame({ width: ACTION_HEIGHT, height: ACTION_HEIGHT }), backgroundOverlay({ color: primaryFill }), clipShape("circle"), contentShape(shapes.rectangle())]}>
							{busy || transcribing
								? <ProgressView modifiers={[tint(t.textSecondary)]} />
								: <Image systemName={listening ? "stop.fill" : dictates ? "mic.fill" : "arrow.up"} modifiers={[font({ textStyle: "headline", weight: "bold" }), foregroundStyle(primaryInk)]} />}
						</HStack>
					</Button>
				</HStack>
			</VStack>
		</Host>
	);
}

/**
 * One option in a selector menu. A Toggle is the system's checkable menu item:
 * it draws the checkmark and VoiceOver's selected state from `selected` alone,
 * and turning the current choice off again changes nothing.
 */
function Choice({ label, systemImage, icon, selected, onChoose }: {
	label: string;
	systemImage?: IOSSymbol;
	icon?: ReactNode;
	selected: boolean;
	onChoose: () => void;
}) {
	const onIsOnChange = (on: boolean) => { if (on) onChoose(); };
	return icon
		? <Toggle isOn={selected} onIsOnChange={onIsOnChange}><Label title={label} icon={icon} /></Toggle>
		: <Toggle isOn={selected} onIsOnChange={onIsOnChange} label={label} systemImage={systemImage} />;
}

/**
 * A trigger's face. A plain-style Menu answers taps only inside its label, so
 * the label itself is a full touch target, gaps included. `priority` decides
 * which face in a row keeps its text when the row runs short: the machine's
 * name gives way before the project and the approval mode.
 */
function SelectorLabel({ icon, title, indicator, priority }: { icon: ReactNode; title: string; indicator: IOSSymbol; priority?: number }) {
	return (
		<HStack spacing={space.xs} modifiers={[frame({ minHeight: touchTarget }), contentShape(shapes.rectangle()), ...(priority ? [layoutPriority(priority)] : [])]}>
			{icon}
			<SelectorText>{title}</SelectorText>
			<SelectorGlyph name={indicator} indicator />
		</HStack>
	);
}

function SelectorText({ children }: { children: string }) {
	return <Text modifiers={[font({ textStyle: "subheadline", weight: "medium" }), lineLimit(1), truncationMode("tail")]}>{children}</Text>;
}

/** A symbol that scales with the label beside it; the disclosure arrow sits a step smaller. */
function SelectorGlyph({ name, indicator = false }: { name: IOSSymbol; indicator?: boolean }) {
	return <Image systemName={name} modifiers={[font(indicator ? { textStyle: "caption2", weight: "semibold" } : { textStyle: "subheadline" })]} />;
}

function projectSystemImage(project?: SpawnComposerOption): "plus.bubble" | "folder" {
	return project?.icon === "message-square-plus" ? "plus.bubble" : "folder";
}

const styles = StyleSheet.create({
	host: { width: "100%" },
});

/**
 * A harness's mark in its own colours, from the app's asset catalog: a 20pt
 * image set with its chip baked in where it needs one (see
 * scripts/generate-harness-menu-icons.mjs). Drawn by name, it keeps its size
 * wherever SwiftUI puts it, an open submenu's header included, which ignores
 * any frame. `uri` is the bundled PNG a caller may still pass; the asset wins.
 */
export function HarnessImage({ harness }: { uri?: string; harness: string }) {
	// One slot for every harness, so switching to one without a mark (drawn as
	// the terminal symbol) never resizes the row it sits in.
	const slot = frame({ width: HARNESS_MARK, height: HARNESS_MARK });
	if (!hasLogo(harness)) return <Image systemName="terminal" modifiers={[font({ textStyle: "subheadline" }), slot]} />;
	return <Image assetName={`HarnessLogo-${logoKey(harness)}`} modifiers={[slot]} />;
}

/** The marks' image sets are drawn at this many points. */
const HARNESS_MARK = 20;

export function useHarnessLogoUris(agents: readonly SpawnComposerOption[]) {
	const ids = useMemo(() => agents.map((agent) => agent.id), [agents]);
	const [uris, setUris] = useState<Record<string, string>>({});
	useEffect(() => {
		let cancelled = false;
		void Promise.all(ids.map(async (id) => {
			const source = logoFor(id);
			if (!source) return [id, ""] as const;
			const asset = Asset.fromModule(source);
			await asset.downloadAsync();
			return [id, asset.localUri ?? asset.uri] as const;
		})).then((entries) => {
			if (!cancelled) setUris(Object.fromEntries(entries.filter(([, uri]) => Boolean(uri))));
		});
		return () => { cancelled = true; };
	}, [ids]);
	return uris;
}
