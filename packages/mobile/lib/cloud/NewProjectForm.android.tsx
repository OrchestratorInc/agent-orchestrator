import {
	Box,
	CircularProgressIndicator,
	Column,
	DropdownMenu,
	DropdownMenuItem,
	Host,
	Icon,
	IconButton,
	Shape,
	Text as ComposeText,
	TextButton,
	TextField,
	useNativeState,
} from "@expo/ui/jetpack-compose";
import { background, clip, fillMaxWidth, padding, Shapes, size, testID as testTag } from "@expo/ui/jetpack-compose/modifiers";
import { Stack } from "expo-router";
import { useMemo, useRef, useState, type ComponentProps } from "react";
import { ScrollView, StyleSheet, View } from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { GroupedRow, GroupedSection } from "../composeGroupedList.android";
import { SegmentedChoice } from "../files/SegmentedChoice";
import { FormTextField } from "../formFields";
import { chipColorFor } from "../harnessLogo";
import { logoFor } from "../harnessLogoAssets";
import { InlineBanner } from "../InlineBanner";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { Spinner } from "../Spinner";
import { gutter, iconSize, radius, space } from "../tokens";
import { toolbarIcon } from "../toolbarIcon";
import { materialDrawable } from "../toolbarIcon.android";
import type { MaterialDrawableName } from "../toolbarIcon.types";
import { useNativeTextInput } from "../useNativeTextInput";
import { cloudAgentLabel, type CloudAgent } from "./agents";
import { PROJECT_NAME_MAX, repositoryDetail, repositorySearchHint } from "./newProject";
import {
	GITHUB_INSTALL_NOTE,
	nameNote,
	NO_AGENT_NOTE,
	noMatchesNote,
	REPOSITORIES_FAILED_NOTE,
	SOURCE_OPTIONS,
	WAITING_FOR_GITHUB,
	type NewProjectBarProps,
	type NewProjectFormProps,
} from "./NewProjectForm.types";
import type { NewCloudProject } from "./useNewCloudProject";

export { NEW_PROJECT_TITLE, type NewProjectBarProps, type NewProjectFormProps } from "./NewProjectForm.types";

// Android: a page under the stack's top app bar, which carries the title and
// the back button, as the manual connection form is. Not a sheet: in a form
// sheet the first tap on a Compose field went to the sheet rather than the
// field, and at full height the sheet's own top row ran under the status bar.
// Under the bar, Material segmented buttons for the source, the repository
// search as a filled search field over a connected list of matches, an
// outlined name field, and the agents as list items that open a dropdown.

type RowPosition = ComponentProps<typeof GroupedRow>["position"];

/**
 * Create, as the bar's trailing check, as the machine editor and the coding
 * agent page save: Android's bar draws icons only. Back cancels. Memoized on
 * what it shows, so the header is not registered again on every key.
 */
export function NewProjectBar({ canCreate, busy, onCreate }: NewProjectBarProps) {
	const createRef = useRef(onCreate);
	createRef.current = onCreate;
	return useMemo(() => (
		<Stack.Toolbar placement="right">
			{busy ? (
				<Stack.Toolbar.View hidesSharedBackground>
					<Spinner accessibilityLabel="Creating project" />
				</Stack.Toolbar.View>
			) : (
				<Stack.Toolbar.Button icon={toolbarIcon("checkmark")} accessibilityLabel="Create project" disabled={!canCreate} onPress={() => createRef.current()} />
			)}
		</Stack.Toolbar>
	), [busy, canCreate]);
}

/**
 * The keyboard controller keeps the window from resizing for the IME, so the
 * page pads itself above the keyboard, as the manual connection form does.
 */
export function NewProjectForm({ project, onConnectAgent }: NewProjectFormProps) {
	const styles = useThemedStyles(makeStyles);
	const { scheme } = useThemeState();
	const insets = useSafeAreaInsets();
	const github = project.source === "github";
	return (
		<KeyboardAvoidingView behavior="padding" automaticOffset style={styles.screen}>
			{/* Under the bar rather than in the scroll, so it shows however far the form has scrolled. */}
			{project.error ? (
				<View style={styles.banner}>
					<InlineBanner tone="danger" icon="error" title={project.error} accessibilityRole="alert" onDismiss={project.dismissError} />
				</View>
			) : null}
			<ScrollView
				style={styles.scroll}
				contentContainerStyle={[styles.content, { paddingBottom: space.xxl + insets.bottom }]}
				keyboardShouldPersistTaps="handled"
			>
				{project.githubUnavailable ? null : (
					<SegmentedChoice
						value={project.source}
						options={SOURCE_OPTIONS}
						onChange={project.chooseSource}
						accessibilityLabel="Project source"
						testID="new-project-source"
					/>
				)}
				{github ? (
					<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.block}>
						<RepositorySection project={project} />
					</Host>
				) : null}
				<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.block}>
					<Column modifiers={[fillMaxWidth()]}>
						<FormTextField
							label={github ? "Name (optional)" : "Name"}
							value={project.name}
							onChangeText={project.setName}
							placeholder={github ? project.repo?.name : "My project"}
							maxLength={PROJECT_NAME_MAX}
							returnKeyType="done"
							testID="new-project-name"
						/>
						<Footnote>{nameNote(project.source)}</Footnote>
					</Column>
				</Host>
				<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.block}>
					<AgentsSection project={project} onConnectAgent={onConnectAgent} />
				</Host>
			</ScrollView>
		</KeyboardAvoidingView>
	);
}

function RepositorySection({ project }: { project: NewCloudProject }) {
	if (project.loadingRepos && !project.repos) {
		return (
			<GroupedSection title="Repository">
				<GroupedRow headline="Loading repositories…" trailing={<RowSpinner />} />
			</GroupedSection>
		);
	}
	if (project.reposFailed && !project.repos) {
		return (
			<GroupedSection title="Repository" footer={REPOSITORIES_FAILED_NOTE}>
				<GroupedRow headline="Try again" leading={<RowIcon name="refresh" accent />} onPress={project.retryRepos} testID="new-project-retry-repos" />
			</GroupedSection>
		);
	}
	if (!project.hasInstallation) {
		return (
			<GroupedSection title="Repository" footer={GITHUB_INSTALL_NOTE}>
				<GitHubRow title="Connect GitHub" icon="open_in_new" project={project} testID="new-project-connect-github" />
			</GroupedSection>
		);
	}
	const chosen = project.searching ? undefined : project.repo;
	if (chosen) {
		// The choice, once made; Change goes back to searching.
		return (
			<GroupedSection title="Repository">
				<GroupedRow
					headline={chosen.fullName}
					supporting={repositoryDetail(chosen)}
					leading={<RowIcon name="folder" />}
					trailing={<TextButton onClick={project.changeRepo}><ComposeText>Change</ComposeText></TextButton>}
					testID="new-project-repo"
				/>
				<GitHubRow title="Add repositories on GitHub" icon="add" project={project} testID="new-project-add-repos" />
			</GroupedSection>
		);
	}
	const footer = project.searching
		? project.matches.length ? undefined : noMatchesNote(project.query)
		: repositorySearchHint(project.repos?.length ?? 0);
	return (
		<Column verticalArrangement={{ spacedBy: space.sm }} modifiers={[fillMaxWidth()]}>
			<SectionTitle>Repository</SectionTitle>
			<RepositorySearchField project={project} />
			<GroupedSection footer={footer}>
				{project.matches.map((item) => (
					<GroupedRow
						key={item.githubRepositoryId}
						headline={item.fullName}
						supporting={repositoryDetail(item)}
						leading={<RowIcon name="folder" />}
						onPress={() => project.chooseRepo(item.githubRepositoryId)}
						testID={`new-project-repo-${item.githubRepositoryId}`}
					/>
				))}
				<GitHubRow title="Add repositories on GitHub" icon="add" project={project} testID="new-project-add-repos" />
			</GroupedSection>
		</Column>
	);
}

/**
 * Material's search field, a filled pill led by the glass. At its end a clear
 * button while something is typed, Refresh otherwise, or a spinner while the
 * list is fetched again.
 */
function RepositorySearchField({ project }: { project: NewCloudProject }) {
	const t = useTheme();
	const text = useNativeState(project.query);
	const change = useNativeTextInput<string>(project.query, text, project.setQuery);
	return (
		<TextField
			value={text}
			onValueChange={(next) => change(next)}
			singleLine
			autoFocus={project.reselecting}
			shape={Shape.Pill({})}
			keyboardOptions={{ capitalization: "none", autoCorrectEnabled: false, imeAction: "search" }}
			// No underline: Material's search field is a filled pill.
			colors={{ focusedIndicatorColor: "transparent", unfocusedIndicatorColor: "transparent", cursorColor: t.highlight }}
			textSelectionColors={{ handleColor: t.highlight, backgroundColor: t.selection }}
			modifiers={[fillMaxWidth(), testTag("new-project-repo-search")]}
		>
			<TextField.Placeholder><ComposeText>Search repositories</ComposeText></TextField.Placeholder>
			<TextField.LeadingIcon><Icon source={materialDrawable("search")} /></TextField.LeadingIcon>
			<TextField.TrailingIcon>
				{project.searching ? (
					<IconButton onClick={() => change("", true)}>
						<Icon source={materialDrawable("close")} contentDescription="Clear search" />
					</IconButton>
				) : project.loadingRepos ? (
					<RowSpinner />
				) : (
					<IconButton onClick={() => void project.refreshRepos()}>
						<Icon source={materialDrawable("refresh")} contentDescription="Refresh repositories" />
					</IconButton>
				)}
			</TextField.TrailingIcon>
		</TextField>
	);
}

/**
 * Opens GitHub's install page. While the phone waits for GitHub to report the
 * installation it says so, with a spinner, and holds.
 */
function GitHubRow({ title, icon, project, testID, position }: { title: string; icon: MaterialDrawableName; project: NewCloudProject; testID: string; position?: RowPosition }) {
	return (
		<GroupedRow
			headline={project.connecting ? WAITING_FOR_GITHUB : title}
			leading={<RowIcon name={icon} accent />}
			trailing={project.connecting ? <RowSpinner /> : undefined}
			onPress={() => void project.connectGitHub()}
			disabled={project.connecting}
			testID={testID}
			position={position}
		/>
	);
}

function AgentsSection({ project, onConnectAgent }: NewProjectFormProps) {
	if (!project.available.length) {
		return (
			<GroupedSection title="Agents" footer={NO_AGENT_NOTE}>
				<GroupedRow headline="Connect a coding agent" leading={<RowIcon name="add" accent />} onPress={onConnectAgent} testID="new-project-connect-agent" />
			</GroupedSection>
		);
	}
	return (
		<GroupedSection title="Agents">
			<AgentRow title="Workers run" agents={project.available} value={project.workerAgent} onChange={project.chooseWorkerAgent} testID="new-project-worker-agent" />
			<AgentRow title="Orchestrator runs" agents={project.available} value={project.orchestratorAgent} onChange={project.chooseOrchestratorAgent} testID="new-project-orchestrator-agent" />
		</GroupedSection>
	);
}

/**
 * A list item showing the role's agent, which opens a dropdown of the
 * connected agents under itself, the chosen one checked: the menu Android's own
 * settings use for picking one value.
 */
function AgentRow({ title, agents, value, onChange, testID, position }: {
	title: string;
	agents: readonly CloudAgent[];
	value?: CloudAgent;
	onChange(agent: CloudAgent): void;
	testID: string;
	/** Set by GroupedSection. */
	position?: RowPosition;
}) {
	const t = useTheme();
	const [open, setOpen] = useState(false);
	return (
		<DropdownMenu expanded={open} onDismissRequest={() => setOpen(false)} color={t.bgElevated} modifiers={[fillMaxWidth()]}>
			<DropdownMenu.Trigger>
				<GroupedRow
					headline={title}
					supporting={value ? cloudAgentLabel(value) : undefined}
					leading={value ? <HarnessMark harness={value} /> : undefined}
					trailing={<Icon source={materialDrawable("expand_more")} size={iconSize.xl} tint={t.textSecondary} />}
					onPress={() => setOpen(true)}
					testID={testID}
					position={position}
				/>
			</DropdownMenu.Trigger>
			<DropdownMenu.Items>
				{agents.map((agent) => (
					<DropdownMenuItem
						key={agent}
						elementColors={{ textColor: t.textPrimary, trailingIconColor: t.textPrimary }}
						modifiers={[testTag(`${testID}-${agent}`)]}
						onClick={() => {
							setOpen(false);
							if (agent !== value) onChange(agent);
						}}
					>
						<DropdownMenuItem.Text><ComposeText>{cloudAgentLabel(agent)}</ComposeText></DropdownMenuItem.Text>
						<DropdownMenuItem.LeadingIcon><HarnessMark harness={agent} /></DropdownMenuItem.LeadingIcon>
						{agent === value ? (
							<DropdownMenuItem.TrailingIcon>
								<Icon source={materialDrawable("check")} size={iconSize.xl} contentDescription="Selected" />
							</DropdownMenuItem.TrailingIcon>
						) : null}
					</DropdownMenuItem>
				))}
			</DropdownMenu.Items>
		</DropdownMenu>
	);
}

/** A harness's brand mark in its own colours, on a chip only where the mark needs one to show. */
function HarnessMark({ harness }: { harness: string }) {
	const source = logoFor(harness);
	if (!source) return <Icon source={materialDrawable("terminal")} size={iconSize.xl} />;
	const chip = chipColorFor(harness);
	if (!chip) return <Icon source={source} tint={null} size={iconSize.xl} />;
	return (
		<Box contentAlignment="center" modifiers={[size(iconSize.xl, iconSize.xl), clip(Shapes.RoundedCorner(radius.sm)), background(chip)]}>
			<Icon source={source} tint={null} size={iconSize.md} />
		</Box>
	);
}

/** A row's glyph: the secondary ink, or the accent for a row that does something. */
function RowIcon({ name, accent = false }: { name: MaterialDrawableName; accent?: boolean }) {
	const t = useTheme();
	return <Icon source={materialDrawable(name)} size={iconSize.xl} tint={accent ? t.accent : t.textSecondary} />;
}

function RowSpinner() {
	const t = useTheme();
	return <CircularProgressIndicator color={t.textSecondary} strokeWidth={2} modifiers={[size(iconSize.lg, iconSize.lg)]} />;
}

/** A section's title, set as GroupedSection sets its own. */
function SectionTitle({ children }: { children: string }) {
	const t = useTheme();
	return <ComposeText color={t.textSecondary} style={{ typography: "titleSmall" }} modifiers={[padding(gutter, space.sm, gutter, space.none)]}>{children}</ComposeText>;
}

/** A field's hint, set as GroupedSection sets its footer. */
function Footnote({ children }: { children: string }) {
	const t = useTheme();
	return <ComposeText color={t.textSecondary} style={{ typography: "bodySmall" }} modifiers={[padding(gutter, space.xs, gutter, space.none)]}>{children}</ComposeText>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	banner: { marginHorizontal: gutter, marginTop: space.sm, marginBottom: space.sm, borderRadius: radius.lg, borderCurve: "continuous", overflow: "hidden" },
	scroll: { flex: 1 },
	content: { paddingHorizontal: gutter, paddingTop: space.sm },
	// Stretched by the scroll view, not given a percentage width, which a
	// Compose host cannot take as a layout value.
	block: { alignSelf: "stretch", marginTop: space.xl },
});
