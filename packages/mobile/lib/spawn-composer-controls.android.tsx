import {
	Box,
	Column,
	DropdownMenu,
	DropdownMenuItem,
	Host,
	HorizontalDivider,
	Icon,
	RNHostView,
	Text,
	TextButton,
} from "@expo/ui/jetpack-compose";
import { fillMaxWidth, height, padding, semantics, testID, width } from "@expo/ui/jetpack-compose/modifiers";
import { Fragment, useState, type ReactNode } from "react";
import { StyleSheet, View } from "react-native";
import { ComposerControlsRow, KEEP_KEYBOARD, MenuRow, ModelCapsuleMenu, useCloseMenuOnBack } from "./composeMenu.android";
import { effortGaugeToggles } from "./effort-gauge";
import { EffortSliderGauge, EffortSliderHost } from "./effort-slider-host";
import { haptics } from "./haptics";
import { ModelWheelHost } from "./model-wheel";
import { PermissionPickerButton, PermissionPickerHost } from "./permission-picker";
import { SpawnComposer } from "./spawn-composer";
import type { SpawnComposerControlsProps, SpawnComposerOption } from "./spawn-composer-controls.types";
import { useSpawnPickers } from "./spawnPickers";
import type { Theme } from "./theme";
import { useTheme, useThemeState, useThemedStyles } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, iconSize, space, touchTarget } from "./tokens";

type OpenMenu = "host" | "project" | "account";

type MenuOption = { id: string; label: string; leading?: ReactNode; sectionBreakBefore?: boolean };

/**
 * Spawn's controls as Material 3, laid out as on iOS: the machine and the
 * project each on its own line, then the chat's composer, its controls row
 * holding who runs the task at the start and the approval mode at the end, as
 * the chat's holds the turn settings. Each selector is a dropdown menu
 * anchored to the button that names the current choice; each agent in its menu
 * opens one level down onto its models. Compose owns the 48dp targets, the
 * ripples and the menu semantics; TalkBack hears the chosen row as selected
 * from its check, not from its colour.
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
	fast,
	ultracode,
	settingsTemplate,
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
	const styles = useThemedStyles(makeStyles);
	const [openMenu, setOpenMenu] = useState<OpenMenu | null>(null);
	const close = () => setOpenMenu(null);
	// The chat's own model wheels, effort slider and permission picker, over
	// the keyboard; typing puts them away.
	const pickers = useSpawnPickers({ agents, agentModels, harness, modelSelection, onSelectAgentModel, approvalModes, approvalMode, onSelectApprovalMode, prompt, settingsTemplate });
	const effortSlider = pickers.effortSlider;
	useCloseMenuOnBack(Boolean(openMenu), close);
	const selectedProject = projects.find((project) => project.id === projectId);
	const hostLabel = hosts.find((host) => host.id === hostId)?.label ?? "Choose machine";
	const projectLabel = selectedProject?.label ?? "Choose project";
	const accountLabel = accounts.find((account) => account.id === accountId)?.label ?? "Default account";
	const harnessLabel = agents.find((agent) => agent.id === harness)?.label ?? "Choose harness";
	// The model the task runs: the one picked or pinned, else the catalog's own
	// default by name, and the agent's name only when the catalog names none.
	const agentTitle = usesDefaultModel ? agentModels[harness]?.defaultLabel ?? harnessLabel : modelLabel;
	// Only a real change is worth a tick; the owner ignores a repeat anyway.
	const choose = (current: string | null, apply: (id: string) => void) => (id: string) => {
		close();
		if (id === current) return;
		haptics.select();
		apply(id);
	};
	const menu = (name: OpenMenu) => ({ expanded: openMenu === name, onExpand: () => setOpenMenu(name), onDismiss: close });

	return (
		<View style={styles.stack}>
			{/* Where the task runs as plain rows above the composer, one line
			    each: the machine, then the project. */}
			<Host matchContents={{ vertical: true }} colorScheme={scheme}>
				<Column modifiers={[fillMaxWidth()]}>
					<SelectorMenu
						{...menu("host")}
						label={hostLabel}
						accessibilityLabel={`Run on ${hostLabel}`}
						leading={<HostGlyph host={hosts.find((host) => host.id === hostId)} />}
						// Each machine with its glyph, as each project has its folder.
						options={hosts.map((host) => ({ ...host, leading: <HostGlyph host={host} /> }))}
						selectedId={hostId}
						onSelect={choose(hostId, onSelectHost)}
						testTag="spawn-host"
					/>
					<SelectorMenu
						{...menu("project")}
						label={projectLabel}
						accessibilityLabel={`Project, ${projectLabel}`}
						leading={<ProjectGlyph project={selectedProject} />}
						options={projects.map((project) => ({ ...project, leading: <ProjectGlyph project={project} /> }))}
						selectedId={projectId}
						onSelect={choose(projectId, onSelectProject)}
						testTag="spawn-project"
					/>
					{accounts.length > 1 && onSelectAccount ? (
						<SelectorMenu
							{...menu("account")}
							label={accountLabel}
							accessibilityLabel={`Account, ${accountLabel}`}
							leading={<Icon source={materialDrawable("account_circle")} size={iconSize.lg} tint={t.textSecondary} />}
							options={accounts}
							selectedId={accountId ?? null}
							onSelect={choose(accountId ?? null, onSelectAccount)}
							testTag="spawn-account"
						/>
					) : null}
				</Column>
			</Host>

			{/* The chat's pickers, over the keyboard in a window of their own. */}
			<EffortSliderHost slider={effortSlider} choices={effortChoices} busy={busy} onChoose={onSelectEffort} fast={fast} ultracode={ultracode} overKeyboard />
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
						<ComposerControlsRow
							// Who runs the task: the harness's mark and the model it runs.
							// A tap opens the chat's model wheels, agent and model.
							capsule={
								<ModelCapsuleMenu
									expanded={false}
									onDismiss={close}
									harness={harness}
									title={agentTitle}
									onClick={() => {
										haptics.tap();
										pickers.closeAllBut("model");
										pickers.model.slider.setOpen(!pickers.model.slider.isOpen());
									}}
									enabled={!busy}
									testTag="spawn-harness"
									// The capsule shows the model alone; TalkBack hears the agent too, as VoiceOver does.
									contentDescription={agentTitle === harnessLabel ? harnessLabel : `${harnessLabel}, ${agentTitle}`}
								>
									{null}
								</ModelCapsuleMenu>
							}
							// The effort as a speedometer lit to the chosen level; a tap
							// opens the effort slider over the keyboard. A model with no
							// levels still has it while the slider carries a switch.
							effort={effortChoices.length || fast || ultracode ? <EffortSliderGauge slider={effortSlider} choices={effortChoices} toggles={effortGaugeToggles(fast, ultracode)} onPress={() => { close(); pickers.closeAllBut("effort"); }} disabled={busy} testID="spawn-effort" /> : null}
							// The approval mode as its own icon, as the chat's; a tap opens
							// the chat's permission picker.
							end={
								<Box contentAlignment="center" modifiers={[width(touchTarget), height(touchTarget)]}>
									<RNHostView matchContents>
										<PermissionPickerButton
											slider={pickers.permissions.slider}
											choices={pickers.permissions.choices}
											disabled={busy}
											onPress={() => { close(); pickers.closeAllBut("permissions"); }}
											testID="spawn-approval"
										/>
									</RNHostView>
								</Box>
							}
						/>
					}
				/>
			</View>
		</View>
	);
}

/**
 * A text button that names the current choice and opens its options as a
 * dropdown menu below it. It takes the width its label needs, and a long label
 * ellipsizes before it can push the arrow out.
 */
function SelectorMenu({ label, accessibilityLabel, leading, options, selectedId, expanded, onExpand, onDismiss, onSelect, testTag }: {
	label: string;
	/** What TalkBack hears when the label alone is too terse, such as a bare machine name. */
	accessibilityLabel?: string;
	leading?: ReactNode;
	options: readonly MenuOption[];
	selectedId: string | null;
	expanded: boolean;
	onExpand: () => void;
	onDismiss: () => void;
	onSelect: (id: string) => void;
	testTag: string;
}) {
	const t = useTheme();
	return (
		<DropdownMenu expanded={expanded} properties={KEEP_KEYBOARD} onDismissRequest={onDismiss} color={t.bgElevated}>
			<DropdownMenu.Trigger>
				<TextButton onClick={onExpand} colors={{ contentColor: t.textPrimary }} modifiers={[testID(testTag), ...(accessibilityLabel ? [semantics({ contentDescription: accessibilityLabel })] : [])]}>
					{leading ?? null}
					<Text
						maxLines={1}
						overflow="ellipsis"
						style={{ typography: "bodyLarge", fontFamily: font.regular }}
						modifiers={[padding(leading ? space.sm : 0, 0, space.xxs, 0)]}
					>
						{label}
					</Text>
					<Icon source={materialDrawable("unfold_more")} size={iconSize.md} tint={t.textTertiary} />
				</TextButton>
			</DropdownMenu.Trigger>
			<DropdownMenu.Items>
				{options.map((option) => (
					<Fragment key={option.id}>
						{option.sectionBreakBefore ? <HorizontalDivider color={t.borderDefault} /> : null}
						<MenuRow label={option.label} leading={option.leading} selected={option.id === selectedId} onClick={() => onSelect(option.id)} />
					</Fragment>
				))}
			</DropdownMenu.Items>
		</DropdownMenu>
	);
}

/** A machine's glyph: a computer, or Orchestrator Cloud's cloud. */
function HostGlyph({ host }: { host?: SpawnComposerOption }) {
	const t = useTheme();
	return <Icon source={materialDrawable(host?.icon === "cloud" ? "cloud" : "laptop")} size={iconSize.lg} tint={t.textSecondary} />;
}

function ProjectGlyph({ project }: { project?: SpawnComposerOption }) {
	const t = useTheme();
	const source = materialDrawable(project?.icon === "message-square-plus" ? "add_comment" : project?.icon === "folder-plus" ? "create_new_folder" : "folder");
	return <Icon source={source} size={iconSize.lg} tint={t.textSecondary} />;
}

const makeStyles = (_t: Theme) => StyleSheet.create({
	stack: { gap: space.xs },
});
