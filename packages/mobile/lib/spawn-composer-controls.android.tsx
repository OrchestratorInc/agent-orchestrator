import {
	Box,
	Button,
	Column,
	DropdownMenu,
	DropdownMenuItem,
	Host,
	HorizontalDivider,
	Icon,
	IconButton,
	Row,
	Spacer,
	Text,
	TextButton,
} from "@expo/ui/jetpack-compose";
import { background, clip, fillMaxWidth, height, padding, semantics, Shapes, size, testID, weight } from "@expo/ui/jetpack-compose/modifiers";
import { Fragment, useState, type ReactNode } from "react";
import { StyleSheet, View } from "react-native";
import { COMPOSER_DISC } from "./chat/composerStyles";
import { chipColorFor } from "./harnessLogo";
import { logoFor } from "./harnessLogoAssets";
import { haptics } from "./haptics";
import { SpawnComposer } from "./spawn-composer";
import type { SpawnComposerControlsProps, SpawnComposerOption } from "./spawn-composer-controls.types";
import { defaultModelChoice } from "./spawnModel";
import type { Theme } from "./theme";
import { useTheme, useThemeState, useThemedStyles } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, iconSize, space, touchTarget } from "./tokens";

type OpenMenu = "host" | "project" | "approval" | "harness" | "effort";

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
	effortChoices,
	onSelectEffort,
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
						leading={<Icon source={materialDrawable("laptop")} size={iconSize.lg} tint={t.textSecondary} />}
						options={hosts}
						selectedId={hostId}
						onSelect={choose(hostId, onSelectHost)}
						testTag="spawn-host"
					/>
					<SelectorMenu
						{...menu("project")}
						label={projectLabel}
						leading={<ProjectGlyph project={selectedProject} />}
						options={projects.map((project) => ({ ...project, leading: <ProjectGlyph project={project} /> }))}
						selectedId={projectId}
						onSelect={choose(projectId, onSelectProject)}
						testTag="spawn-project"
					/>
				</Column>
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
					<Host matchContents={{ vertical: true }} colorScheme={scheme}>
						<Row verticalAlignment="center" modifiers={[fillMaxWidth(), height(touchTarget)]}>
							{/* Who runs the task: the harness's mark and the model it runs,
							    in a filled capsule the circles' height. */}
							<DropdownMenu expanded={openMenu === "harness"} onDismissRequest={close} color={t.bgElevated}>
								<DropdownMenu.Trigger>
									<Button
										onClick={() => setOpenMenu("harness")}
										colors={{ containerColor: t.bgSubtle, contentColor: t.textPrimary }}
										contentPadding={{ start: space.md, end: space.xs }}
										enabled={!busy}
										modifiers={[height(COMPOSER_DISC), testID("spawn-harness")]}
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

							{/* The effort as a speedometer, the chat's; the menu names the
							    model's levels and checks the one it will run at. */}
							{effortChoices.length ? (
								<DropdownMenu expanded={openMenu === "effort"} onDismissRequest={close} color={t.bgElevated}>
									<DropdownMenu.Trigger>
										<IconButton onClick={() => setOpenMenu("effort")} enabled={!busy} colors={{ contentColor: t.textSecondary }} modifiers={[testID("spawn-effort")]}>
											<Icon source={materialDrawable("speed")} size={iconSize.lg} contentDescription={`Effort, ${effortChoices.find((choice) => choice.selected)?.label ?? "default"}`} />
										</IconButton>
									</DropdownMenu.Trigger>
									<DropdownMenu.Items>
										{effortChoices.map((choice) => (
											<MenuRow key={choice.value} label={choice.label} selected={choice.selected} onClick={() => choose(effortChoices.find((item) => item.selected)?.value ?? null, onSelectEffort)(choice.value)} />
										))}
									</DropdownMenu.Items>
								</DropdownMenu>
							) : null}

							<Spacer modifiers={[weight(1)]} />

							{/* The approval mode as its symbol alone, as the chat's; the
							    menu and TalkBack name it. */}
							<DropdownMenu expanded={openMenu === "approval"} onDismissRequest={close} color={t.bgElevated}>
								<DropdownMenu.Trigger>
									<IconButton onClick={() => setOpenMenu("approval")} enabled={!busy} colors={{ contentColor: t.textSecondary }} modifiers={[testID("spawn-approval")]}>
										<Icon source={materialDrawable("verified_user")} size={iconSize.lg} contentDescription={`Approval, ${approvalOption?.label ?? "Auto"}`} />
									</IconButton>
								</DropdownMenu.Trigger>
								<DropdownMenu.Items>
									{approvalModes.map((mode) => (
										<MenuRow key={mode.id} label={mode.label} selected={mode.id === approvalMode} onClick={() => choose(approvalMode, onSelectApprovalMode)(mode.id)} />
									))}
								</DropdownMenu.Items>
							</DropdownMenu>
						</Row>
					</Host>
				}
			/>
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
						style={{ typography: "bodyLarge", fontFamily: font.regular }}
						modifiers={[padding(leading ? space.sm : 0, 0, space.xxs, 0)]}
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
	stack: { gap: space.xs },
});
