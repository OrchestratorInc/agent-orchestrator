import { Host } from "@expo/ui";
import { Asset } from "expo-asset";
import { Button, Divider, HStack, Image, Label, Menu, Section, Spacer, Text, Toggle, VStack } from "@expo/ui/swift-ui";
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
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { StyleSheet, View } from "react-native";
import { COMPOSER_DISC } from "./chat/composerStyles";
import { hasLogo, logoKey } from "./harnessLogo";
import { logoFor } from "./harnessLogoAssets";
import { haptics } from "./haptics";
import { SpawnComposer } from "./spawn-composer";
import type { SpawnComposerControlsProps, SpawnComposerOption } from "./spawn-composer-controls.types";
import { defaultModelChoice } from "./spawnModel";
import { approvalSymbol } from "./approvalSymbol";
import type { IOSSymbol } from "./symbols";
import { useTheme, useThemeState } from "./ThemeProvider";
import { space, touchTarget } from "./tokens";

/**
 * Spawn's selectors and composer. The selectors are SwiftUI menus whose options
 * are the system's checkable items, so the current choice carries a real
 * checkmark and selected state rather than a drawn one. The triggers keep their
 * icon and name, size to their content and scale with Dynamic Type. The
 * composer below them is the chat's, its controls row holding who runs the
 * task and how freely as the chat's holds the turn settings.
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
	prompt,
	onChangePrompt,
	placeholder,
	attachments,
	hasAttachments,
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
	// A choice only fires when it changes, so each one is worth the tick.
	const choose = (apply: (id: string) => void, id: string) => {
		haptics.select();
		apply(id);
	};

	return (
		<View style={styles.stack}>
			{/* Where the task runs as plain rows above the composer, one line
			    each, as a new conversation lists them: the machine, then the
			    project. Every
			    trigger's touch target carries its own air above and below the
			    text, so the rows need no gap. Compact and left-anchored: a
			    max-width frame would make a Menu fill the row, which centers its
			    popup over the whole screen. */}
			<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.host}>
				<VStack alignment="leading" spacing={space.none} modifiers={[padding({ horizontal: space.xs })]}>
					<HStack>
						<Menu
							label={<SelectorLabel icon={<SelectorGlyph name="laptopcomputer" />} title={hostLabel} />}
							modifiers={[buttonStyle("plain"), menuOrder("fixed"), tint(t.textSecondary), accessibilityLabel(`Run on ${hostLabel}`), accessibilityIdentifier("spawn-host")]}
						>
							{hosts.map((host) => <Choice key={host.id} label={host.label} selected={host.id === hostId} onChoose={() => choose(onSelectHost, host.id)} />)}
						</Menu>
						<Spacer minLength={0} />
					</HStack>

					<HStack>
						<Menu
							label={<SelectorLabel icon={<SelectorGlyph name={projectSystemImage(selectedProject)} />} title={projectLabel} />}
							modifiers={[buttonStyle("plain"), menuOrder("fixed"), tint(t.textSecondary), accessibilityIdentifier("spawn-project")]}
						>
							{projects.flatMap((project) => [
								project.sectionBreakBefore ? <Divider key={`${project.id}-break`} /> : null,
								<Choice key={project.id} label={project.label} systemImage={projectSystemImage(project)} selected={project.id === projectId} onChoose={() => choose(onSelectProject, project.id)} />,
							])}
						</Menu>
						<Spacer minLength={0} />
					</HStack>

				</VStack>
			</Host>

			{attachments}

			<SpawnComposer
				prompt={prompt}
				onChangePrompt={onChangePrompt}
				placeholder={placeholder}
				hasAttachments={hasAttachments}
				onChoosePhoto={onChoosePhoto}
				onChooseFile={onChooseFile}
				voice={voice}
				onSpawn={onSpawn}
				busy={busy}
				disabled={disabled}
				settings={
					// As the chat's turn settings: who runs the task at the row's
					// start beside attach, how freely at its end beside the round
					// button; the space between them gives way first.
					<Host ignoreSafeArea="all" colorScheme={scheme} style={styles.settingsHost}>
						<HStack spacing={space.none}>
							{/* The harness's mark and the model it runs (the harness's
							    name only when the catalog names no default), in the chat
							    composer's capsule; each agent in its menu opens one level
							    down onto its models. */}
							<Menu
								label={
									<HStack modifiers={[frame({ minHeight: touchTarget }), contentShape(shapes.rectangle())]}>
										<HStack spacing={space.xs} modifiers={[padding({ horizontal: space.md }), frame({ minHeight: COMPOSER_DISC, maxHeight: COMPOSER_DISC }), backgroundOverlay({ color: t.bgElevated }), clipShape("capsule")]}>
											<HarnessImage harness={harness} />
											<SelectorText>{agentTitle}</SelectorText>
											<SelectorGlyph name="chevron.up.chevron.down" indicator />
										</HStack>
									</HStack>
								}
								modifiers={[
									buttonStyle("plain"),
									menuOrder("fixed"),
									tint(t.textSecondary),
									disabledModifier(busy),
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
							{/* The approval mode as its symbol alone, the chat's; the menu
							    and VoiceOver name it. */}
							<Menu
								label={
									<Image
										systemName={approvalSymbol(approvalMode, harness)}
										modifiers={[font({ textStyle: "subheadline", weight: "medium" }), frame({ width: touchTarget, height: touchTarget }), contentShape(shapes.rectangle())]}
									/>
								}
								modifiers={[
									buttonStyle("plain"),
									menuOrder("fixed"),
									tint(t.textSecondary),
									disabledModifier(busy),
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
						</HStack>
					</Host>
				}
			/>
		</View>
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
 * A row's face: its glyph, the current choice and the disclosure arrow. A
 * plain-style Menu answers taps only inside its label, so the label itself is
 * a full touch target, gaps included.
 */
function SelectorLabel({ icon, title }: { icon: ReactNode; title: string }) {
	return (
		<HStack spacing={space.sm} modifiers={[frame({ minHeight: touchTarget }), contentShape(shapes.rectangle())]}>
			{icon}
			<Text modifiers={[font({ textStyle: "body" }), lineLimit(1), truncationMode("tail")]}>{title}</Text>
			<SelectorGlyph name="chevron.up.chevron.down" indicator />
		</HStack>
	);
}

function SelectorText({ children }: { children: string }) {
	return <Text modifiers={[font({ textStyle: "subheadline", weight: "medium" }), lineLimit(1), truncationMode("tail")]}>{children}</Text>;
}

/** A symbol that scales with the label beside it; the disclosure arrow sits a step smaller. */
function SelectorGlyph({ name, indicator = false }: { name: IOSSymbol; indicator?: boolean }) {
	return <Image systemName={name} modifiers={[font(indicator ? { textStyle: "caption2", weight: "semibold" } : { textStyle: "body" })]} />;
}

function projectSystemImage(project?: SpawnComposerOption): "plus.bubble" | "folder" {
	return project?.icon === "message-square-plus" ? "plus.bubble" : "folder";
}

const styles = StyleSheet.create({
	stack: { gap: space.xs },
	host: { width: "100%" },
	// The chat's turn-settings host: the controls row's height, filling it.
	settingsHost: { alignSelf: "stretch", height: touchTarget },
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
