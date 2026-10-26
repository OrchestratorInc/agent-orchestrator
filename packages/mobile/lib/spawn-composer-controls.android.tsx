import {
	Box,
	Button,
	CircularProgressIndicator,
	Column,
	DropdownMenu,
	DropdownMenuItem,
	FilledIconButton,
	Host,
	HorizontalDivider,
	Icon,
	Row,
	Spacer,
	Text,
	TextButton,
} from "@expo/ui/jetpack-compose";
import { background, clip, fillMaxWidth, height, padding, semantics, Shapes, size, testID, weight } from "@expo/ui/jetpack-compose/modifiers";
import { Fragment, useState, type ReactNode } from "react";
import { StyleSheet, View } from "react-native";
import { chipColorFor } from "./harnessLogo";
import { logoFor } from "./harnessLogoAssets";
import { haptics } from "./haptics";
import type { SpawnComposerControlsProps, SpawnComposerOption } from "./spawn-composer-controls.types";
import { defaultModelChoice } from "./spawnModel";
import type { Theme } from "./theme";
import { useTheme, useThemeState, useThemedStyles } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, iconSize, space } from "./tokens";

type OpenMenu = "host" | "project" | "approval" | "attach" | "harness";

type MenuOption = { id: string; label: string; leading?: ReactNode; sectionBreakBefore?: boolean };

/** The composer line's controls: Material's 48dp touch target, one height for all three. */
const ACTION_SIZE = 48;

/**
 * Spawn's controls as Material 3, laid out as on iOS: the machine on its own
 * line, then the project and the approval mode, then the composer's own line,
 * with who runs the task in a filled capsule at its start and the attach and
 * send buttons as filled circles at its end. Each selector is a dropdown menu
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
	agents,
	harness,
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
	const styles = useThemedStyles(makeStyles);
	const [openMenu, setOpenMenu] = useState<OpenMenu | null>(null);
	// The agent whose models are open, one level down the agent menu.
	const [openAgent, setOpenAgent] = useState<string | null>(null);
	const close = () => { setOpenMenu(null); setOpenAgent(null); };
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
	const primaryEnabled = dictates ? !transcribing : !disabled;
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
			<Host matchContents={{ vertical: true }} colorScheme={scheme}>
				<Column modifiers={[fillMaxWidth()]}>
					<Row verticalAlignment="center" modifiers={[fillMaxWidth()]}>
						<SelectorMenu
							{...menu("host")}
							label={hostLabel}
							accessibilityLabel={`Run on ${hostLabel}`}
							leading={<Icon source={materialDrawable("desktop_windows")} size={iconSize.lg} tint={t.textSecondary} />}
							options={hosts}
							selectedId={hostId}
							onSelect={choose(hostId, onSelectHost)}
							testTag="spawn-host"
						/>
					</Row>
					<Row horizontalArrangement={{ spacedBy: space.xs }} verticalAlignment="center" modifiers={[fillMaxWidth()]}>
						<SelectorMenu
							{...menu("project")}
							label={projectLabel}
							leading={<ProjectGlyph project={selectedProject} />}
							options={projects.map((project) => ({ ...project, leading: <ProjectGlyph project={project} /> }))}
							selectedId={projectId}
							onSelect={choose(projectId, onSelectProject)}
							testTag="spawn-project"
						/>
						{/* What the worker may do before it asks, apart from the model. */}
						<SelectorMenu
							{...menu("approval")}
							label={approvalOption?.shortLabel ?? approvalOption?.label ?? "Auto"}
							leading={<Icon source={materialDrawable("verified_user")} size={iconSize.lg} tint={t.textSecondary} />}
							options={approvalModes}
							selectedId={approvalMode}
							onSelect={choose(approvalMode, onSelectApprovalMode)}
							testTag="spawn-approval"
						/>
					</Row>

					<Row horizontalArrangement={{ spacedBy: space.sm }} verticalAlignment="center" modifiers={[fillMaxWidth(), padding(0, space.xs, 0, 0)]}>
						{/* Who runs the task: the harness's mark and the model it runs,
						    in a filled capsule the circles' height. */}
						<DropdownMenu expanded={openMenu === "harness"} onDismissRequest={close} color={t.bgElevated}>
							<DropdownMenu.Trigger>
								<Button
									onClick={() => setOpenMenu("harness")}
									colors={{ containerColor: t.bgElevated, contentColor: t.textPrimary }}
									modifiers={[height(ACTION_SIZE), testID("spawn-harness")]}
								>
									<HarnessMark harness={harness} />
									<Text maxLines={1} overflow="ellipsis" style={{ typography: "labelLarge", fontFamily: font.medium }} modifiers={[padding(space.sm, 0, space.xxs, 0)]}>
										{agentTitle}
									</Text>
									<Icon source={materialDrawable("arrow_drop_down")} size={iconSize.xl} tint={t.textTertiary} />
								</Button>
							</DropdownMenu.Trigger>
							<DropdownMenu.Items>
								{agents.map((agent) => (
									<DropdownMenu key={agent.id} expanded={openAgent === agent.id} onDismissRequest={() => setOpenAgent(null)} color={t.bgElevated}>
										<DropdownMenu.Trigger>
											<DropdownMenuItem onClick={() => setOpenAgent(agent.id)} elementColors={{ textColor: t.textPrimary }} modifiers={[testID(`spawn-model-${agent.id}`)]}>
												<DropdownMenuItem.Text>
													<Text maxLines={1} overflow="ellipsis" style={{ typography: "bodyLarge", fontFamily: font.regular }}>{agent.label}</Text>
												</DropdownMenuItem.Text>
												<DropdownMenuItem.LeadingIcon><HarnessMark harness={agent.id} /></DropdownMenuItem.LeadingIcon>
												<DropdownMenuItem.TrailingIcon>
													<Icon source={materialDrawable("chevron_right")} size={iconSize.xl} tint={t.textTertiary} />
												</DropdownMenuItem.TrailingIcon>
											</DropdownMenuItem>
										</DropdownMenu.Trigger>
										<DropdownMenu.Items>
											{/* The default first, named when the catalog says which it is;
											    then the first few models; then the whole catalog. */}
											{[{ id: "__auto__", label: defaultModelChoice(agentModels[agent.id]?.defaultLabel) }, ...(agentModels[agent.id]?.models ?? [])].map((model) => (
												<MenuRow
													key={model.id}
													label={model.label}
													selected={agent.id === harness && model.id === modelSelection}
													onClick={() => {
														close();
														if (agent.id === harness && model.id === modelSelection) return;
														haptics.select();
														onSelectAgentModel(agent.id, model.id);
													}}
												/>
											))}
											{(agentModels[agent.id]?.total ?? 0) > (agentModels[agent.id]?.models.length ?? 0) ? (
												<>
													<HorizontalDivider color={t.borderDefault} />
													<MenuRow
														label={`All models (${agentModels[agent.id]!.total.toLocaleString()})`}
														leading={<Icon source={materialDrawable("search")} size={iconSize.xl} tint={t.textSecondary} />}
														onClick={() => { close(); onBrowseModels(agent.id); }}
													/>
												</>
											) : null}
										</DropdownMenu.Items>
									</DropdownMenu>
								))}
							</DropdownMenu.Items>
						</DropdownMenu>

						<Spacer modifiers={[weight(1)]} />

						<DropdownMenu expanded={openMenu === "attach"} onDismissRequest={close} color={t.bgElevated}>
							<DropdownMenu.Trigger>
								<FilledIconButton
									onClick={() => setOpenMenu("attach")}
									enabled={!busy}
									colors={{ containerColor: t.bgElevated, contentColor: t.textPrimary, disabledContainerColor: t.bgElevated, disabledContentColor: t.textFaint }}
									modifiers={[size(ACTION_SIZE, ACTION_SIZE), testID("spawn-attachment")]}
								>
									<Icon source={materialDrawable("attach_file")} size={iconSize.xl} contentDescription="Attach" />
								</FilledIconButton>
							</DropdownMenu.Trigger>
							<DropdownMenu.Items>
								<MenuRow label="Choose photo" leading={<Icon source={materialDrawable("photo")} size={iconSize.xl} tint={t.textSecondary} />} onClick={() => { close(); onChoosePhoto(); }} />
								<MenuRow label="Choose file" leading={<Icon source={materialDrawable("draft")} size={iconSize.xl} tint={t.textSecondary} />} onClick={() => { close(); onChooseFile(); }} />
							</DropdownMenu.Items>
						</DropdownMenu>

						{/* One round button that follows the prompt: with text it starts the
						    task, with none it dictates, and while the mic is open it stops,
						    in the recording red. A button only taps, so dictation here is
						    hands-free: tap, speak, tap again. The success and failure
						    haptics come from the request itself, so the press adds none. */}
						<FilledIconButton
							onClick={dictates ? voice.onToggleLatched : onSpawn}
							enabled={primaryEnabled}
							colors={{
								containerColor: listening ? t.red : t.accent,
								contentColor: listening ? t.onDestructive : t.onAccent,
								disabledContainerColor: t.bgElevated,
								disabledContentColor: t.textFaint,
							}}
							modifiers={[size(ACTION_SIZE, ACTION_SIZE), testID(dictates ? "spawn-dictate" : "spawn-submit")]}
						>
							{busy || transcribing
								? <CircularProgressIndicator color={t.textFaint} strokeWidth={2} modifiers={[size(iconSize.md, iconSize.md)]} />
								: <Icon source={materialDrawable(listening ? "stop" : dictates ? "mic" : "arrow_upward")} size={iconSize.xl} contentDescription={primaryLabel} />}
						</FilledIconButton>
					</Row>
				</Column>
			</Host>
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
		<DropdownMenu expanded={expanded} onDismissRequest={onDismiss} color={t.bgElevated}>
			<DropdownMenu.Trigger>
				<TextButton onClick={onExpand} colors={{ contentColor: t.textPrimary }} modifiers={[testID(testTag), ...(accessibilityLabel ? [semantics({ contentDescription: accessibilityLabel })] : [])]}>
					{leading ?? null}
					<Text
						maxLines={1}
						overflow="ellipsis"
						style={{ typography: "labelLarge", fontFamily: font.medium }}
						modifiers={[padding(leading ? space.xs : 0, 0, space.xxs, 0)]}
					>
						{label}
					</Text>
					<Icon source={materialDrawable("arrow_drop_down")} size={iconSize.xl} tint={t.textTertiary} />
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

function MenuRow({ label, leading, selected = false, onClick }: { label: string; leading?: ReactNode; selected?: boolean; onClick: () => void }) {
	const t = useTheme();
	return (
		<DropdownMenuItem onClick={onClick} elementColors={{ textColor: t.textPrimary }}>
			<DropdownMenuItem.Text>
				<Text maxLines={2} overflow="ellipsis" style={{ typography: "bodyLarge", fontFamily: font.regular }}>{label}</Text>
			</DropdownMenuItem.Text>
			{leading ? <DropdownMenuItem.LeadingIcon>{leading}</DropdownMenuItem.LeadingIcon> : null}
			{selected ? (
				<DropdownMenuItem.TrailingIcon>
					<Icon source={materialDrawable("check")} size={iconSize.xl} tint={t.accent} contentDescription="Selected" />
				</DropdownMenuItem.TrailingIcon>
			) : null}
		</DropdownMenuItem>
	);
}

function ProjectGlyph({ project }: { project?: SpawnComposerOption }) {
	const t = useTheme();
	const source = project?.icon === "message-square-plus" ? materialDrawable("add_comment") : materialDrawable("folder");
	return <Icon source={source} size={iconSize.lg} tint={t.textSecondary} />;
}

// The mark's box, and the chip it sits on when it needs one to stay visible.
// Same arithmetic as `AgentLogo`: a 16% inset and a 28% corner radius.
const MARK_SIZE = iconSize.lg;
const CHIP_INSET = Math.round(MARK_SIZE * 0.16);
const CHIP_RADIUS = Math.round(MARK_SIZE * 0.28);

/** A harness's brand mark in its own colours, or the terminal symbol when it has none. */
function HarnessMark({ harness }: { harness: string }) {
	const t = useTheme();
	const source = logoFor(harness);
	if (!source) return <Icon source={materialDrawable("terminal")} size={MARK_SIZE} tint={t.textSecondary} />;
	// White marks (opencode, cursor) vanish on a light menu; `chipColorFor`
	// says which need a backdrop, as it does for every other surface.
	const chip = chipColorFor(harness);
	if (!chip) return <Icon source={source} size={MARK_SIZE} tint={null} />;
	return (
		<Box contentAlignment="center" modifiers={[size(MARK_SIZE, MARK_SIZE), clip(Shapes.RoundedCorner(CHIP_RADIUS)), background(chip)]}>
			<Icon source={source} size={MARK_SIZE - CHIP_INSET * 2} tint={null} />
		</Box>
	);
}

const makeStyles = (_t: Theme) => StyleSheet.create({
	stack: { gap: space.sm },
});
