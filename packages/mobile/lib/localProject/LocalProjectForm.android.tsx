import {
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
import { fillMaxWidth, padding, size, testID as testTag } from "@expo/ui/jetpack-compose/modifiers";
import { useState } from "react";
import { ScrollView, StyleSheet, View } from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { repositoryDetail } from "../cloud/newProject";
import { GroupedRow, GroupedSection } from "../composeGroupedList.android";
import { SegmentedChoice } from "../files/SegmentedChoice";
import { FormTextField, FormToggle } from "../formFields";
import { InlineBanner } from "../InlineBanner";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { gutter, iconSize, radius, space } from "../tokens";
import { materialDrawable } from "../toolbarIcon.android";
import type { MaterialDrawableName } from "../toolbarIcon.types";
import { useNativeTextInput } from "../useNativeTextInput";
import {
	browseTitle,
	CHECKING_FOLDER,
	chooseHereLabel,
	cloneTargetNote,
	destinationTitle,
	githubSearchHint,
	LOCAL_SOURCE_OPTIONS,
	NEW_NAME_PLACEHOLDER,
	nameNote,
	noFolderMatchesNote,
	RECENT_TITLE,
	TRUNCATED_NOTE,
	type LocalProjectFormProps,
	REMOTE_MODE_OPTIONS,
	REMOTE_TITLE,
	remoteFooter,
	SETUP_TITLE,
} from "./LocalProjectForm.types";
import { actionLabel } from "./importRules";
import { PlacementPicker } from "./PlacementPicker";
import { baseName, clonePathPreview, displayPath, LOCAL_PROJECT_NAME_MAX, newProjectPathPreview, placeTitle, repositoryNameFromUrl } from "./newLocalProject";
import type { BrowseTarget, NewLocalProject } from "./useNewLocalProject";

// Android: a page under the stack's top app bar, as the cloud project's.
// Material segmented buttons for where and from what, the machine as a list
// item that opens a dropdown, and the machine's folders browsed in place as a
// connected list under a filled filter field.

export function LocalProjectForm({ project, placement }: LocalProjectFormProps) {
	const styles = useThemedStyles(makeStyles);
	const { scheme } = useThemeState();
	const insets = useSafeAreaInsets();
	const browsing = project.browseTarget;
	const nameBlock = (
		<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.block}>
			<NameField project={project} />
		</Host>
	);
	const destinationBlock = (
		<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.block}>
			{browsing === "destination" ? <BrowserSection project={project} target="destination" /> : <DestinationSection project={project} />}
		</Host>
	);
	const setupBlock = (
		<>
			{project.setupActions.length ? (
				<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.block}>
					<SetupSection project={project} />
				</Host>
			) : null}
			{project.remoteNeeded ? (
				<>
					<SegmentedChoice
						value={project.remote.mode}
						options={REMOTE_MODE_OPTIONS}
						onChange={project.remote.chooseMode}
						accessibilityLabel={REMOTE_TITLE}
						style={styles.block}
						testID="new-local-project-remote-mode"
					/>
					<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.blockTight}>
						<RemoteSection project={project} />
					</Host>
				</>
			) : null}
		</>
	);
	return (
		<KeyboardAvoidingView behavior="padding" automaticOffset style={styles.screen}>
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
				{placement ? <PlacementPicker placement={placement} /> : null}
				{/* Held still while a create runs: no touches, dimmed as Material dims a disabled control. */}
				<View pointerEvents={project.busy ? "none" : "auto"} style={[placement ? styles.block : undefined, project.busy ? styles.held : undefined]}>
					<SegmentedChoice
						value={project.source}
						options={LOCAL_SOURCE_OPTIONS}
						onChange={project.chooseSource}
						accessibilityLabel="Project source"
						testID="new-local-project-source"
					/>
				</View>
				{project.machines.length > 1 ? (
					<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.block}>
						<MachineSection project={project} />
					</Host>
				) : null}
				{project.source === "folder" ? (
					<>
						<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.block}>
							{browsing === "folder" ? <BrowserSection project={project} target="folder" /> : <ChosenFolderSection project={project} />}
						</Host>
						{browsing === "folder" ? null : setupBlock}
						{nameBlock}
					</>
				) : project.source === "new" ? (
					<>
						{nameBlock}
						{destinationBlock}
						{setupBlock}
					</>
				) : (
					<>
						<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.block}>
							<Column modifiers={[fillMaxWidth()]}>
								<FormTextField
									label="Repository URL"
									value={project.remoteUrl}
									onChangeText={project.setRemoteUrl}
									placeholder="https://github.com/owner/repo.git"
									keyboardType="url"
									autoCapitalize="none"
									autoCorrect={false}
									returnKeyType="done"
									editable={!project.busy}
									testID="new-local-project-url"
								/>
								<Footnote>{cloneTargetNote(clonePathPreview(project.remoteUrl, displayPath(project.destinationParent, project.home)))}</Footnote>
							</Column>
						</Host>
						{project.github.available ? (
							<Host matchContents={{ vertical: true }} colorScheme={scheme} style={styles.block}>
								<GitHubSection project={project} />
							</Host>
						) : null}
						{destinationBlock}
						{setupBlock}
						{nameBlock}
					</>
				)}
			</ScrollView>
		</KeyboardAvoidingView>
	);
}

function NameField({ project }: { project: NewLocalProject }) {
	const newPath = newProjectPathPreview(project.name, displayPath(project.destinationParent, project.home));
	return (
		<Column modifiers={[fillMaxWidth()]}>
			<FormTextField
				label={project.source === "new" ? "Name" : "Name (optional)"}
				value={project.name}
				onChangeText={project.setName}
				placeholder={namePlaceholder(project)}
				maxLength={LOCAL_PROJECT_NAME_MAX}
				returnKeyType="done"
				testID="new-local-project-name"
			/>
			<Footnote>{nameNote(project.source, newPath)}</Footnote>
		</Column>
	);
}

/**
 * The repositories the Orchestrator GitHub App reaches, as a filled search
 * field over a connected list of matches: a match fills the URL.
 */
function GitHubSection({ project }: { project: NewLocalProject }) {
	const t = useTheme();
	const github = project.github;
	const text = useNativeState(github.query);
	const change = useNativeTextInput<string>(github.query, text, github.setQuery);
	const footer = github.searching
		? github.matches.length ? undefined : `No repository matches “${github.query.trim()}”.`
		: githubSearchHint(github.count);
	return (
		<Column verticalArrangement={{ spacedBy: space.sm }} modifiers={[fillMaxWidth()]}>
			<SectionTitle>From GitHub</SectionTitle>
			<TextField
				value={text}
				onValueChange={(next) => change(next)}
				singleLine
				shape={Shape.Pill({})}
				keyboardOptions={{ capitalization: "none", autoCorrectEnabled: false, imeAction: "search" }}
				colors={{ focusedIndicatorColor: "transparent", unfocusedIndicatorColor: "transparent", cursorColor: t.highlight }}
				textSelectionColors={{ handleColor: t.highlight, backgroundColor: t.selection }}
				modifiers={[fillMaxWidth(), testTag("new-local-project-github-search")]}
			>
				<TextField.Placeholder><ComposeText>Search repositories</ComposeText></TextField.Placeholder>
				<TextField.LeadingIcon><Icon source={materialDrawable("search")} /></TextField.LeadingIcon>
				{/* Always drawn and only shown with text: Compose doesn't draw a trailing slot that fills in later. */}
				<TextField.TrailingIcon>
					<IconButton onClick={() => { if (github.searching) change("", true); }}>
						<Icon source={materialDrawable("close")} tint={github.searching ? t.textSecondary : "transparent"} contentDescription={github.searching ? "Clear search" : undefined} />
					</IconButton>
				</TextField.TrailingIcon>
			</TextField>
			{github.matches.length || footer ? (
				<GroupedSection footer={footer}>
					{github.matches.map((repo) => (
						<GroupedRow
							key={repo.githubRepositoryId}
							headline={repo.fullName}
							supporting={repositoryDetail(repo)}
							leading={<RowIcon name="folder" />}
							onPress={() => project.takeRepository(repo)}
							testID={`new-local-project-github-${repo.githubRepositoryId}`}
						/>
					))}
				</GroupedSection>
			) : null}
		</Column>
	);
}

function namePlaceholder(project: NewLocalProject): string | undefined {
	if (project.source === "new") return NEW_NAME_PLACEHOLDER;
	if (project.source === "folder") return project.folder ? baseName(project.folder) : undefined;
	return repositoryNameFromUrl(project.remoteUrl) || undefined;
}


/** The machine as a list item that opens a dropdown of the connected ones, the chosen one checked. */
function MachineSection({ project }: { project: NewLocalProject }) {
	const t = useTheme();
	const [open, setOpen] = useState(false);
	return (
		<GroupedSection title="Machine">
			<DropdownMenu expanded={open} onDismissRequest={() => setOpen(false)} color={t.bgElevated} modifiers={[fillMaxWidth()]}>
				<DropdownMenu.Trigger>
					<GroupedRow
						headline={project.machine?.label ?? "Machine"}
						leading={<RowIcon name="laptop" />}
						trailing={<Icon source={materialDrawable("expand_more")} size={iconSize.xl} tint={t.textSecondary} />}
						onPress={() => { if (!project.busy) setOpen(true); }}
						disabled={project.busy}
						testID="new-local-project-machine"
					/>
				</DropdownMenu.Trigger>
				<DropdownMenu.Items>
					{project.machines.map((machine) => (
						<DropdownMenuItem
							key={machine.id}
							elementColors={{ textColor: t.textPrimary, trailingIconColor: t.textPrimary }}
							modifiers={[testTag(`new-local-project-machine-${machine.id}`)]}
							onClick={() => {
								setOpen(false);
								project.chooseHost(machine.id);
							}}
						>
							<DropdownMenuItem.Text><ComposeText>{machine.label}</ComposeText></DropdownMenuItem.Text>
							{machine.id === project.hostId ? (
								<DropdownMenuItem.TrailingIcon>
									<Icon source={materialDrawable("check")} size={iconSize.xl} contentDescription="Selected" />
								</DropdownMenuItem.TrailingIcon>
							) : null}
						</DropdownMenuItem>
					))}
				</DropdownMenu.Items>
			</DropdownMenu>
		</GroupedSection>
	);
}

function ChosenFolderSection({ project }: { project: NewLocalProject }) {
	const folder = project.folder ?? "";
	const footer = project.checking ? CHECKING_FOLDER : project.blocked ?? undefined;
	return (
		<GroupedSection title="Folder" footer={footer}>
			<GroupedRow
				headline={placeTitle(folder, project.home)}
				supporting={displayPath(folder, project.home)}
				leading={<RowIcon name="folder" />}
				trailing={<TextButton enabled={!project.busy} onClick={() => project.changeFolder("folder")}><ComposeText>Change</ComposeText></TextButton>}
				testID="new-local-project-folder"
			/>
		</GroupedSection>
	);
}

/** The Git setup Create runs on the machine, in the desktop's order, the running step spinning. */
function SetupSection({ project }: { project: NewLocalProject }) {
	return (
		<GroupedSection title={SETUP_TITLE}>
			{project.setupActions.map((action) => (
				<GroupedRow
					key={action}
					headline={actionLabel(action)}
					leading={project.runningAction === action ? <RowSpinner /> : <RowIcon name="code" />}
					testID={`new-local-project-step-${action}`}
				/>
			))}
		</GroupedSection>
	);
}

/** The project's origin: a new GitHub repository under one of gh's accounts on the machine, or a remote URL. */
function RemoteSection({ project }: { project: NewLocalProject }) {
	const t = useTheme();
	const remote = project.remote;
	const [ownersOpen, setOwnersOpen] = useState(false);
	const footer = remoteFooter(remote, project.remoteResuming);
	return (
		<Column verticalArrangement={{ spacedBy: space.sm }} modifiers={[fillMaxWidth()]}>
			{remote.mode === "github" && remote.owners?.length ? (
				<GroupedSection>
					<DropdownMenu expanded={ownersOpen} onDismissRequest={() => setOwnersOpen(false)} color={t.bgElevated} modifiers={[fillMaxWidth()]}>
						<DropdownMenu.Trigger>
							<GroupedRow
								headline="Owner"
								supporting={remote.owner}
								trailing={<Icon source={materialDrawable("expand_more")} size={iconSize.xl} tint={t.textSecondary} />}
								onPress={() => setOwnersOpen(true)}
								testID="new-local-project-github-owner"
							/>
						</DropdownMenu.Trigger>
						<DropdownMenu.Items>
							{remote.owners.map((owner) => (
								<DropdownMenuItem
									key={owner.login}
									elementColors={{ textColor: t.textPrimary, trailingIconColor: t.textPrimary }}
									onClick={() => {
										setOwnersOpen(false);
										remote.chooseOwner(owner.login);
									}}
								>
									<DropdownMenuItem.Text><ComposeText>{owner.login}</ComposeText></DropdownMenuItem.Text>
									{owner.login === remote.owner ? (
										<DropdownMenuItem.TrailingIcon>
											<Icon source={materialDrawable("check")} size={iconSize.xl} contentDescription="Selected" />
										</DropdownMenuItem.TrailingIcon>
									) : null}
								</DropdownMenuItem>
							))}
						</DropdownMenu.Items>
					</DropdownMenu>
				</GroupedSection>
			) : null}
			{remote.mode === "github" ? (
				<Column modifiers={[fillMaxWidth()]}>
					<FormTextField
						label="Repository"
						value={remote.nameText}
						onChangeText={remote.setName}
						autoCapitalize="none"
						autoCorrect={false}
						returnKeyType="done"
						testID="new-local-project-github-name"
					/>
					<FormToggle label="Private" value={remote.isPrivate} onValueChange={remote.setPrivate} testID="new-local-project-github-private" />
					<Footnote>{footer}</Footnote>
				</Column>
			) : (
				<Column modifiers={[fillMaxWidth()]}>
					<FormTextField
						label="Remote URL"
						value={remote.url}
						onChangeText={remote.setUrl}
						placeholder="git@github.com:owner/repo.git"
						keyboardType="url"
						autoCapitalize="none"
						autoCorrect={false}
						returnKeyType="done"
						error={remote.urlInvalid ? footer : undefined}
						testID="new-local-project-remote-url"
					/>
					{remote.urlInvalid ? null : <Footnote>{footer}</Footnote>}
				</Column>
			)}
		</Column>
	);
}

function DestinationSection({ project }: { project: NewLocalProject }) {
	const shown = displayPath(project.destinationParent, project.home);
	return (
		<GroupedSection title={destinationTitle(project.source)}>
			<GroupedRow
				headline={placeTitle(project.destinationParent, project.home)}
				supporting={shown}
				leading={<RowIcon name="folder" />}
				trailing={<TextButton enabled={!project.busy} onClick={() => project.changeFolder("destination")}><ComposeText>Change</ComposeText></TextButton>}
				testID="new-local-project-destination"
			/>
		</GroupedSection>
	);
}

function BrowserSection({ project, target }: { project: NewLocalProject; target: BrowseTarget }) {
	const listing = project.listing;
	const canCancel = target === "destination" || Boolean(project.folder);
	if (project.listingError && !listing) {
		return (
			<GroupedSection title={browseTitle(target, project.source)} footer={project.listingError}>
				<GroupedRow headline="Try again" leading={<RowIcon name="refresh" accent />} onPress={project.retryListing} testID="new-local-project-retry-folders" />
			</GroupedSection>
		);
	}
	if (!listing) {
		return (
			<GroupedSection title={browseTitle(target, project.source)}>
				<GroupedRow headline="Loading folders…" trailing={<RowSpinner />} />
			</GroupedSection>
		);
	}
	const filtering = project.filter.trim().length > 0;
	const footer = project.listingError
		?? (filtering && project.entries.length === 0 ? noFolderMatchesNote(project.filter) : listing.truncated ? TRUNCATED_NOTE : undefined);
	return (
		<Column verticalArrangement={{ spacedBy: space.sm }} modifiers={[fillMaxWidth()]}>
			<SectionTitle>{browseTitle(target, project.source)}</SectionTitle>
			<FolderFilterField project={project} />
			<GroupedSection footer={footer}>
				<GroupedRow
					headline={displayPath(listing.path, project.home)}
					supporting={project.atRoot ? undefined : "Up one folder"}
					leading={<RowIcon name="arrow_upward" accent={!project.atRoot} />}
					trailing={project.listingBusy ? <RowSpinner /> : canCancel ? <TextButton onClick={project.cancelBrowse}><ComposeText>Cancel</ComposeText></TextButton> : undefined}
					onPress={project.atRoot ? undefined : () => project.openDirectory(listing.parent)}
					testID="new-local-project-browse-path"
				/>
				<GroupedRow headline={chooseHereLabel(target, project.source)} leading={<RowIcon name="check" accent />} onPress={() => { if (!project.listingBusy && !project.busy) project.choose(listing.path); }} disabled={project.listingBusy || project.busy} testID="new-local-project-choose-here" />
				{project.entries.map((entry) => (
					<GroupedRow
						key={entry.path}
						headline={entry.name}
						supporting={entry.gitRepo ? "Git repository" : undefined}
						leading={<RowIcon name="folder" accent={entry.gitRepo} />}
						trailing={target === "folder" && entry.gitRepo ? undefined : <Icon source={materialDrawable("chevron_right")} size={iconSize.xl} />}
						onPress={() => project.pressEntry(entry)}
						testID={`new-local-project-entry-${entry.name}`}
					/>
				))}
			</GroupedSection>
			{project.recentPlaces.length ? (
				<GroupedSection title={RECENT_TITLE}>
					{project.recentPlaces.map((path) => (
						<GroupedRow
							key={path}
							headline={placeTitle(path, project.home)}
							supporting={displayPath(path, project.home)}
							leading={<RowIcon name="history" />}
							onPress={() => project.openDirectory(path)}
							testID={`new-local-project-recent-${baseName(path)}`}
						/>
					))}
				</GroupedSection>
			) : null}
		</Column>
	);
}

function FolderFilterField({ project }: { project: NewLocalProject }) {
	const t = useTheme();
	const text = useNativeState(project.filter);
	const change = useNativeTextInput<string>(project.filter, text, project.setFilter);
	return (
		<TextField
			value={text}
			onValueChange={(next) => change(next)}
			singleLine
			shape={Shape.Pill({})}
			keyboardOptions={{ capitalization: "none", autoCorrectEnabled: false, imeAction: "search" }}
			colors={{ focusedIndicatorColor: "transparent", unfocusedIndicatorColor: "transparent", cursorColor: t.highlight }}
			textSelectionColors={{ handleColor: t.highlight, backgroundColor: t.selection }}
			modifiers={[fillMaxWidth(), testTag("new-local-project-filter")]}
		>
			<TextField.Placeholder><ComposeText>Filter folders</ComposeText></TextField.Placeholder>
			<TextField.LeadingIcon><Icon source={materialDrawable("search")} /></TextField.LeadingIcon>
			{/* Always drawn and only shown with text: Compose doesn't draw a trailing slot that fills in later. */}
			<TextField.TrailingIcon>
				<IconButton onClick={() => { if (project.filter) change("", true); }}>
					<Icon source={materialDrawable("close")} tint={project.filter ? t.textSecondary : "transparent"} contentDescription={project.filter ? "Clear filter" : undefined} />
				</IconButton>
			</TextField.TrailingIcon>
		</TextField>
	);
}

function RowIcon({ name, accent = false }: { name: MaterialDrawableName; accent?: boolean }) {
	const t = useTheme();
	return <Icon source={materialDrawable(name)} size={iconSize.xl} tint={accent ? t.accent : t.textSecondary} />;
}

function RowSpinner() {
	const t = useTheme();
	return <CircularProgressIndicator color={t.textSecondary} strokeWidth={2} modifiers={[size(iconSize.lg, iconSize.lg)]} />;
}

function SectionTitle({ children }: { children: string }) {
	const t = useTheme();
	return <ComposeText color={t.textSecondary} style={{ typography: "titleSmall" }} modifiers={[padding(gutter, space.sm, gutter, space.none)]}>{children}</ComposeText>;
}

/** A field's hint, or nothing. */
function Footnote({ children }: { children?: string }) {
	const t = useTheme();
	if (!children) return null;
	return <ComposeText color={t.textSecondary} style={{ typography: "bodySmall" }} modifiers={[padding(gutter, space.xs, gutter, space.none)]}>{children}</ComposeText>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	banner: { marginHorizontal: gutter, marginTop: space.sm, marginBottom: space.sm, borderRadius: radius.lg, borderCurve: "continuous", overflow: "hidden" },
	scroll: { flex: 1 },
	content: { paddingHorizontal: gutter, paddingTop: space.sm },
	block: { alignSelf: "stretch", marginTop: space.xl },
	blockTight: { alignSelf: "stretch", marginTop: space.md },
	held: { opacity: 0.38 },
});
