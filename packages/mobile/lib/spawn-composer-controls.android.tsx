import {
	Box,
	Button,
	CircularProgressIndicator,
	DropdownMenu,
	DropdownMenuItem,
	Host,
	HorizontalDivider,
	Icon,
	IconButton,
	Row,
	Text,
	TextButton,
} from "@expo/ui/jetpack-compose";
import { background, clip, fillMaxWidth, padding, Shapes, size, testID, weight } from "@expo/ui/jetpack-compose/modifiers";
import { Fragment, useState, type ReactNode } from "react";
import { StyleSheet, View } from "react-native";
import { rowDividerWidth } from "./divider";
import { chipColorFor } from "./harnessLogo";
import { logoFor } from "./harnessLogoAssets";
import { haptics } from "./haptics";
import { spawnDrawables } from "./spawn-composer-drawables.android";
import type { SpawnComposerControlsProps, SpawnComposerOption } from "./spawn-composer-controls.types";
import type { Theme } from "./theme";
import { useTheme, useThemeState, useThemedStyles } from "./ThemeProvider";
import { materialDrawable } from "./toolbarIcon.android";
import { font, iconSize, radius, space, touchTarget } from "./tokens";
import { MicKey } from "./voice/MicKey";

type OpenMenu = "host" | "project" | "attach" | "harness" | "model";

type MenuOption = { id: string; label: string; leading?: ReactNode; sectionBreakBefore?: boolean };

/**
 * Spawn's selectors as Material 3 dropdown menus, each anchored to the text
 * button that names the current choice, and Start as a filled Compose button.
 * Compose owns the 48dp targets, the ripples and the menu semantics; TalkBack
 * hears the chosen row as selected from its check, not from its colour.
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
	models,
	modelSelection,
	modelLabel,
	onSelectModel,
	onChoosePhoto,
	onChooseFile,
	voice,
	onSpawn,
	busy,
	disabled,
}: SpawnComposerControlsProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const [openMenu, setOpenMenu] = useState<OpenMenu | null>(null);
	const close = () => setOpenMenu(null);
	const selectedProject = projects.find((project) => project.id === projectId);
	const hostLabel = hosts.find((host) => host.id === hostId)?.label ?? "Choose machine";
	const projectLabel = selectedProject?.label ?? "Choose project";
	const harnessLabel = agents.find((agent) => agent.id === harness)?.label ?? "Choose harness";
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
				<Row horizontalArrangement={{ spacedBy: space.xs }} verticalAlignment="center" modifiers={[fillMaxWidth()]}>
					<SelectorMenu
						{...menu("host")}
						label={`Run on ${hostLabel}`}
						leading={<Icon source={spawnDrawables.machine} size={iconSize.lg} tint={t.textSecondary} />}
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
				</Row>
			</Host>

			<View style={styles.rail}>
				<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.railHost}>
					<Row verticalAlignment="center" modifiers={[fillMaxWidth()]}>
						<DropdownMenu expanded={openMenu === "attach"} onDismissRequest={close} color={t.bgElevated}>
							<DropdownMenu.Trigger>
								<IconButton onClick={() => setOpenMenu("attach")} modifiers={[testID("spawn-attachment")]}>
									<Icon source={spawnDrawables.attach} size={iconSize.xl} tint={t.textSecondary} contentDescription="Attach" />
								</IconButton>
							</DropdownMenu.Trigger>
							<DropdownMenu.Items>
								<MenuRow label="Choose photo" leading={<Icon source={spawnDrawables.photo} size={iconSize.xl} tint={t.textSecondary} />} onClick={() => { close(); onChoosePhoto(); }} />
								<MenuRow label="Choose file" leading={<Icon source={spawnDrawables.file} size={iconSize.xl} tint={t.textSecondary} />} onClick={() => { close(); onChooseFile(); }} />
							</DropdownMenu.Items>
						</DropdownMenu>
						<SelectorMenu
							{...menu("harness")}
							label={harnessLabel}
							leading={<HarnessMark harness={harness} />}
							options={agents.map((agent) => ({ ...agent, leading: <HarnessMark harness={agent.id} /> }))}
							selectedId={harness}
							onSelect={choose(harness, onSelectHarness)}
							testTag="spawn-harness"
						/>
						<SelectorMenu
							{...menu("model")}
							label={modelLabel}
							options={[{ id: "__auto__", label: "Automatic" }, ...models]}
							selectedId={modelSelection}
							onSelect={choose(modelSelection, onSelectModel)}
							testTag="spawn-model"
						/>
					</Row>
				</Host>
				{/* Hold-to-talk needs press-in and press-out, which a Compose button
				    doesn't expose, so the mic stays the React Native key. Plain: the
				    rail is the surface, and a second disc would compete with Start. */}
				<MicKey
					variant="plain"
					size={touchTarget}
					glyphSize={iconSize.xl}
					state={voice.state}
					mode={voice.mode}
					onPressIn={voice.onPressIn}
					onPressOut={voice.onPressOut}
				/>
			</View>

			<Host matchContents={{ vertical: true }} colorScheme={scheme}>
				<Button
					onClick={onSpawn}
					enabled={!disabled}
					// The accent is not one of the Material scheme's own roles, so the
					// button is given its fill and its ink, enabled and disabled.
					colors={{ containerColor: t.accent, contentColor: t.onAccent, disabledContainerColor: t.bgElevatedHover, disabledContentColor: t.textFaint }}
					modifiers={[fillMaxWidth(), testID("spawn-submit")]}
				>
					{busy ? <CircularProgressIndicator color={t.textFaint} strokeWidth={2} modifiers={[size(iconSize.md, iconSize.md)]} /> : null}
					{/* TalkBack names a Compose button by its text (@expo/ui gives it
					    no content description), so the busy text spells out the name
					    iOS announces; the spinner already shows the progress. */}
					<Text style={{ typography: "labelLarge", fontFamily: font.semibold }} modifiers={busy ? [padding(space.sm, 0, 0, 0)] : undefined}>
						{busy ? "Starting task" : "Start task"}
					</Text>
				</Button>
			</Host>
		</View>
	);
}

/**
 * A text button that names the current choice and opens its options as a
 * dropdown menu below it. Rows sharing a menu take equal widths, and a long
 * label ellipsizes before it can push the arrow out.
 */
function SelectorMenu({ label, leading, options, selectedId, expanded, onExpand, onDismiss, onSelect, testTag }: {
	label: string;
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
		<DropdownMenu expanded={expanded} onDismissRequest={onDismiss} color={t.bgElevated} modifiers={[weight(1)]}>
			<DropdownMenu.Trigger>
				<TextButton onClick={onExpand} colors={{ contentColor: t.textPrimary }} modifiers={[fillMaxWidth(), testID(testTag)]}>
					{leading ?? null}
					<Text
						maxLines={1}
						overflow="ellipsis"
						style={{ typography: "labelLarge", fontFamily: font.medium }}
						modifiers={[weight(1), padding(leading ? space.xs : 0, 0, space.xxs, 0)]}
					>
						{label}
					</Text>
					<Icon source={spawnDrawables.dropDown} size={iconSize.xl} tint={t.textTertiary} />
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
	const source = project?.icon === "message-square-plus" ? spawnDrawables.standalone : materialDrawable("folder");
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
	if (!source) return <Icon source={spawnDrawables.agent} size={MARK_SIZE} tint={t.textSecondary} />;
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

const makeStyles = (t: Theme) => StyleSheet.create({
	stack: { gap: space.sm },
	rail: {
		flexDirection: "row",
		alignItems: "center",
		paddingHorizontal: space.xxs,
		borderRadius: radius.lg,
		borderCurve: "continuous",
		backgroundColor: t.bgElevated,
		borderWidth: rowDividerWidth,
		borderColor: t.borderDefault,
	},
	railHost: { flex: 1, minWidth: 0 },
});
