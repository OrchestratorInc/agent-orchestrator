import { Host } from "@expo/ui";
import { Button, Divider, HStack, Image, Label, Menu, RNHostView, Section, Spacer, Text, VStack } from "@expo/ui/swift-ui";
import {
	accessibilityIdentifier,
	accessibilityLabel,
	buttonStyle,
	contentShape,
	font,
	frame,
	lineLimit,
	menuOrder,
	padding,
	shapes,
	tint,
	truncationMode,
} from "@expo/ui/swift-ui/modifiers";
import { useState, type ReactNode } from "react";
import { StyleSheet, View } from "react-native";
import { logoKey } from "./harnessLogo";
import { hasMenuIcon } from "./harnessMenuIcons";
import { EffortSliderGauge, EffortSliderHost } from "./effort-slider-host";
import { haptics } from "./haptics";
import { Choice, controlsRowTypeSize, ModelCapsuleLabel, useTriggerModifiers } from "./model-controls.ios";
import { ModelWheelHost } from "./model-wheel";
import { PermissionPickerButton, PermissionPickerHost } from "./permission-picker";
import { SpawnComposer } from "./spawn-composer";
import type { SpawnComposerControlsProps, SpawnComposerOption } from "./spawn-composer-controls.types";
import { useSpawnPickers } from "./spawnPickers";
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
	accounts = [],
	accountId,
	onSelectAccount,
	agents,
	harness,
	agentModels,
	modelSelection,
	modelLabel,
	usesDefaultModel,
	onSelectAgentModel,
	approvalModes,
	approvalMode,
	onSelectApprovalMode,
	effortChoices,
	onSelectEffort,
	onChoosePhoto,
	onChooseFile,
	onPasteImages,
	voice,
	prompt,
	onChangePrompt,
	onLoadSkills,
	placeholder,
	attachments,
	onSpawn,
	busy,
	disabled,
}: SpawnComposerControlsProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	// The chat's own model wheels, effort slider and permission picker, over
	// the keyboard; typing puts them away.
	const pickers = useSpawnPickers({ agents, agentModels, harness, modelSelection, onSelectAgentModel, approvalModes, approvalMode, onSelectApprovalMode, prompt });
	const effortSlider = pickers.effortSlider;
	const selectedProject = projects.find((project) => project.id === projectId);
	const hostLabel = hosts.find((host) => host.id === hostId)?.label ?? "Choose machine";
	const projectLabel = selectedProject?.label ?? "Choose project";
	const accountLabel = accounts.find((account) => account.id === accountId)?.label ?? "Default account";
	const harnessLabel = agents.find((agent) => agent.id === harness)?.label ?? "Choose harness";
	// The model the task runs: the one picked or pinned, else the catalog's own
	// default by name, and the agent's name only when the catalog names none.
	const agentTitle = usesDefaultModel ? agentModels[harness]?.defaultLabel ?? harnessLabel : modelLabel;
	const capsuleModifiers = useTriggerModifiers({ label: agentTitle === harnessLabel ? harnessLabel : `${harnessLabel}, ${agentTitle}`, id: "spawn-harness", disabled: busy });

	return (
		<View style={styles.stack}>
			{/* Where the task runs as plain rows above the composer, one line
			    each, as a new conversation lists them: the machine, then the
			    project. Every
			    trigger's touch target carries its own air above and below the
			    text, so the rows need no gap. Compact and left-anchored: a
			    max-width frame would make a Menu fill the row, which centers its
			    popup over the whole screen. The dock rises over the keyboard by a
			    transform, so SwiftUI places this host under the keyboard and
			    would lift its rows by the overlap, over whatever the page shows
			    above them (a notice, a button); it keeps out of the safe area. */}
			<Host matchContents={{ vertical: true }} ignoreSafeArea="all" colorScheme={scheme} style={styles.host}>
				<VStack alignment="leading" spacing={space.none} modifiers={[padding({ horizontal: space.xs })]}>
					<HStack>
						<Menu
							label={<SelectorLabel icon={<SelectorGlyph name={hostSystemImage(hosts.find((host) => host.id === hostId))} />} title={hostLabel} />}
							modifiers={[buttonStyle("plain"), menuOrder("fixed"), tint(t.textSecondary), accessibilityLabel(`Run on ${hostLabel}`), accessibilityIdentifier("spawn-host")]}
						>
							{/* Each machine with its glyph, as each project has its folder: a computer, or Cloud's cloud. */}
							{hosts.map((host) => <Choice key={host.id} label={host.label} systemImage={hostSystemImage(host)} selected={host.id === hostId} onChoose={() => onSelectHost(host.id)} />)}
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
								<Choice key={project.id} label={project.label} systemImage={projectSystemImage(project)} selected={project.id === projectId} onChoose={() => onSelectProject(project.id)} />,
							])}
						</Menu>
						<Spacer minLength={0} />
					</HStack>

					{accounts.length > 1 && onSelectAccount ? (
						<HStack>
							<Menu
								label={<SelectorLabel icon={<SelectorGlyph name="person.crop.circle" />} title={accountLabel} />}
								modifiers={[buttonStyle("plain"), menuOrder("fixed"), tint(t.textSecondary), accessibilityLabel(`Account, ${accountLabel}`), accessibilityIdentifier("spawn-account")]}
							>
								{accounts.map((account) => <Choice key={account.id} label={account.label} selected={account.id === accountId} onChoose={() => onSelectAccount?.(account.id)} />)}
							</Menu>
							<Spacer minLength={0} />
						</HStack>
					) : null}
				</VStack>
			</Host>

			{/* The chat's pickers, over the keyboard in a window of their own. */}
			<EffortSliderHost slider={effortSlider} choices={effortChoices} busy={busy} onChoose={onSelectEffort} overKeyboard />
			<PermissionPickerHost slider={pickers.permissions.slider} choices={pickers.permissions.choices} busy={busy} onChoose={pickers.permissions.choose} />
			<ModelWheelHost {...pickers.model} busy={busy} />

			<View onLayout={(event) => effortSlider.setBottom(event.nativeEvent.layout.height)}>
				<SpawnComposer
					prompt={prompt}
					attachments={attachments}
					onChangePrompt={onChangePrompt}
					onLoadSkills={onLoadSkills}
					placeholder={placeholder}
					onChoosePhoto={onChoosePhoto}
					onChooseFile={onChooseFile}
					onPasteImages={onPasteImages}
					voice={voice}
					onSpawn={onSpawn}
					busy={busy}
					disabled={disabled}
					settings={
						// The chat's controls row (see model-controls): who runs the task
						// and its effort at the start beside attach, how freely at the
						// end beside the round button; the space between gives way first.
						<Host ignoreSafeArea="all" colorScheme={scheme} style={styles.settingsHost}>
							<HStack spacing={space.none} modifiers={[controlsRowTypeSize()]}>
								{/* The harness's mark and the model it runs; a tap opens the
								    chat's model wheels, agent and model, over the keyboard. */}
								<Button
									onPress={() => {
										haptics.tap();
										pickers.closeAllBut("model");
										pickers.model.slider.setOpen(!pickers.model.slider.isOpen());
									}}
									modifiers={capsuleModifiers}
								>
									<ModelCapsuleLabel mark={<HarnessImage harness={harness} />} title={agentTitle} />
								</Button>
								{/* The effort as a speedometer lit to the chosen level; a tap
								    opens the effort slider over the keyboard. */}
								{effortChoices.length ? (
									<RNHostView matchContents>
										<EffortSliderGauge slider={effortSlider} choices={effortChoices} disabled={busy} onPress={() => pickers.closeAllBut("effort")} testID="spawn-effort" />
									</RNHostView>
								) : null}
								<Spacer minLength={0} />
								{/* The approval mode as its own icon; a tap opens the chat's
								    permission picker. */}
								<RNHostView matchContents>
									<PermissionPickerButton slider={pickers.permissions.slider} choices={pickers.permissions.choices} disabled={busy} onPress={() => pickers.closeAllBut("permissions")} testID="spawn-approval" />
								</RNHostView>
							</HStack>
						</Host>
					}
				/>
			</View>
		</View>
	);
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

/** A symbol that scales with the label beside it; the disclosure arrow sits a step smaller. */
function SelectorGlyph({ name, indicator = false }: { name: IOSSymbol; indicator?: boolean }) {
	return <Image systemName={name} modifiers={[font(indicator ? { textStyle: "caption2", weight: "semibold" } : { textStyle: "body" })]} />;
}

function hostSystemImage(host?: SpawnComposerOption): "cloud" | "laptopcomputer" {
	return host?.icon === "cloud" ? "cloud" : "laptopcomputer";
}

function projectSystemImage(project?: SpawnComposerOption): "plus.bubble" | "folder.badge.plus" | "folder" {
	return project?.icon === "message-square-plus" ? "plus.bubble" : project?.icon === "folder-plus" ? "folder.badge.plus" : "folder";
}

const styles = StyleSheet.create({
	stack: { gap: space.xs },
	host: { width: "100%" },
	// The chat's turn-settings host: the controls row's height, filling it.
	settingsHost: { alignSelf: "stretch", height: touchTarget },
});

/**
 * A harness's mark in its own colours, from the app's asset catalog: a 20pt
 * image set whose dark appearance is the brand's dark mark, where it ships one
 * (see scripts/sync-harness-logos.mjs). Drawn by name, it keeps its size
 * wherever SwiftUI puts it, an open submenu's header included, which ignores
 * any frame. Only marks the installed binary's catalog has are drawn by name
 * (see harnessMenuIcons.ts); a missing asset would draw an empty slot.
 */
export function HarnessImage({ harness }: { harness: string }) {
	// One slot for every harness, so switching to one without a mark (drawn as
	// the terminal symbol) never resizes the row it sits in.
	const slot = frame({ width: HARNESS_MARK, height: HARNESS_MARK });
	if (!hasMenuIcon(harness)) return <Image systemName="terminal" modifiers={[font({ textStyle: "subheadline" }), slot]} />;
	return <Image assetName={`HarnessLogo-${logoKey(harness)}`} modifiers={[slot]} />;
}

/** The marks' image sets are drawn at this many points. */
const HARNESS_MARK = 20;
