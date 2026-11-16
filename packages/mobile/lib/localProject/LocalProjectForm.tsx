import { Host } from "@expo/ui";
import { Button, Form, HStack, Image, Label, Picker, ProgressView, Spacer, Text, TextField, useNativeState, VStack } from "@expo/ui/swift-ui";
import {
	accessibilityHidden,
	accessibilityIdentifier,
	accessibilityLabel,
	autocorrectionDisabled,
	buttonStyle,
	disabled as disabledModifier,
	foregroundStyle,
	frame,
	labelStyle,
	lineLimit,
	listRowBackground,
	listRowInsets,
	pickerStyle,
	submitLabel,
	tag,
	textInputAutocapitalization,
	tint,
} from "@expo/ui/swift-ui/modifiers";
import { StyleSheet, View } from "react-native";
import { repositoryDetail } from "../cloud/newProject";
import type { CloudGitHubRepository } from "../cloud/types";
import { formModifiers, FormTextField, FormToggle, sectionModifiers } from "../formFields";
import { InlineBanner } from "../InlineBanner";
import { SettingsSection } from "../SettingsForm";
import { symbols, type IOSSymbol } from "../symbols";
import { appFont } from "../swiftUIFont";
import { useTheme, useThemeState } from "../ThemeProvider";
import { rowIconWidth, space } from "../tokens";
import { useGlassBarInset } from "../useHeaderOverlap";
import { useNativeTextInput } from "../useNativeTextInput";
import type { DirectoryEntry } from "./api";
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
	REMOTE_MODE_OPTIONS,
	REMOTE_TITLE,
	remoteFooter,
	SETUP_TITLE,
	TRUNCATED_NOTE,
	type LocalProjectFormProps,
} from "./LocalProjectForm.types";
import { actionLabel } from "./importRules";
import { PlacementPicker } from "./PlacementPicker";
import { baseName, clonePathPreview, displayPath, LOCAL_PROJECT_NAME_MAX, newProjectPathPreview, placeTitle, repositoryNameFromUrl } from "./newLocalProject";
import type { BrowseTarget, NewLocalProject } from "./useNewLocalProject";

// iOS: a SwiftUI Form under the sheet's native bar, as the cloud project's.
// The machine's folders are browsed in place, in their own section: a
// repository row picks itself, any other folder opens, and Use this folder
// picks the one shown. A new project is named first, as its name is its folder.

export function LocalProjectForm({ project, placement }: LocalProjectFormProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const browsing = project.browseTarget;
	// The sheet's clear bar covers the page's top; the Form insets itself, the banner over it does not.
	const barInset = useGlassBarInset();
	const nameSection = <NameSection project={project} />;
	const destination = browsing === "destination" ? <BrowserSection project={project} target="destination" /> : <DestinationSection project={project} />;
	const setup = (
		<>
			{project.setupActions.length ? <SetupSection project={project} /> : null}
			{project.remoteNeeded ? <RemoteSection project={project} /> : null}
		</>
	);
	return (
		<View style={[styles.screen, { backgroundColor: t.bgBase }]}>
			{project.error ? <View style={{ paddingTop: barInset }}><InlineBanner tone="danger" icon="error" title={project.error} accessibilityRole="alert" onDismiss={project.dismissError} /></View> : null}
			<Host style={styles.form} colorScheme={scheme}>
				<Form modifiers={[...formModifiers(t), appFont("body")]}>
					{placement ? <PlacementPicker placement={placement} /> : null}
					<MachineSection project={project} />
					<SettingsSection>
						<Picker
							label="Source"
							selection={project.source}
							onSelectionChange={(next: string) => {
								const option = LOCAL_SOURCE_OPTIONS.find((item) => item.key === next);
								if (option) project.chooseSource(option.key);
							}}
							modifiers={[pickerStyle("segmented"), listRowBackground("transparent"), listRowInsets({ top: 0, leading: 0, bottom: 0, trailing: 0 }), disabledModifier(project.busy), accessibilityIdentifier("new-local-project-source")]}
						>
							{LOCAL_SOURCE_OPTIONS.map((option) => <Text key={option.key} modifiers={[tag(option.key)]}>{option.label}</Text>)}
						</Picker>
					</SettingsSection>
					{project.source === "folder" ? (
						<>
							{browsing === "folder" ? <BrowserSection project={project} target="folder" /> : <ChosenFolderSection project={project} />}
							{browsing === "folder" ? null : setup}
							{nameSection}
						</>
					) : project.source === "clone" ? (
						<>
							<SettingsSection title="Repository" footer={cloneTargetNote(clonePathPreview(project.remoteUrl, displayPath(project.destinationParent, project.home)))} modifiers={sectionModifiers(t)}>
								<FormTextField
									label="URL"
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
							</SettingsSection>
							{project.github.available ? <GitHubSection project={project} /> : null}
							{destination}
							{setup}
							{nameSection}
						</>
					) : (
						<>
							{nameSection}
							{destination}
							{setup}
						</>
					)}
				</Form>
			</Host>
		</View>
	);
}

/** The folder's or the repository's own name is the project's when none is typed; a new one needs one. */
function namePlaceholder(project: NewLocalProject): string {
	if (project.source === "new") return NEW_NAME_PLACEHOLDER;
	if (project.source === "folder") return project.folder ? baseName(project.folder) : "Optional";
	return repositoryNameFromUrl(project.remoteUrl) || "Optional";
}

function NameSection({ project }: { project: NewLocalProject }) {
	const t = useTheme();
	const newPath = newProjectPathPreview(project.name, displayPath(project.destinationParent, project.home));
	return (
		<SettingsSection footer={nameNote(project.source, newPath)} modifiers={sectionModifiers(t)}>
			<FormTextField
				label="Name"
				value={project.name}
				onChangeText={project.setName}
				placeholder={namePlaceholder(project)}
				maxLength={LOCAL_PROJECT_NAME_MAX}
				returnKeyType="done"
				testID="new-local-project-name"
			/>
		</SettingsSection>
	);
}

/** Which paired machine holds the project: a menu picker when there is a choice. */
function MachineSection({ project }: { project: NewLocalProject }) {
	const t = useTheme();
	if (project.machines.length < 2) return null;
	return (
		<SettingsSection modifiers={sectionModifiers(t)}>
			<Picker
				label="Machine"
				selection={project.hostId ?? ""}
				onSelectionChange={(next: string) => project.chooseHost(next)}
				modifiers={[pickerStyle("menu"), tint(t.accent), disabledModifier(project.busy), accessibilityIdentifier("new-local-project-machine")]}
			>
				{project.machines.map((machine) => <Label key={machine.id} title={machine.label} systemImage={symbols.laptop.ios} modifiers={[tag(machine.id)]} />)}
			</Picker>
		</SettingsSection>
	);
}

/** The folder, once chosen; Change goes back to browsing. */
function ChosenFolderSection({ project }: { project: NewLocalProject }) {
	const t = useTheme();
	const folder = project.folder ?? "";
	const footer = project.checking ? CHECKING_FOLDER : project.blocked ?? undefined;
	return (
		<SettingsSection title="Folder" footer={footer} modifiers={sectionModifiers(t)}>
			<HStack spacing={space.md} modifiers={[accessibilityIdentifier("new-local-project-folder")]}>
				<PathLabel title={placeTitle(folder, project.home)} detail={displayPath(folder, project.home)} systemImage={symbols.folder.ios} />
				<Button label="Change" onPress={() => project.changeFolder("folder")} modifiers={[buttonStyle("borderless"), disabledModifier(project.busy), accessibilityLabel(`Change folder from ${baseName(folder)}`)]} />
			</HStack>
		</SettingsSection>
	);
}

/** The Git setup Create runs on the machine, in the desktop's order, the running step spinning. */
function SetupSection({ project }: { project: NewLocalProject }) {
	const t = useTheme();
	return (
		<SettingsSection title={SETUP_TITLE} modifiers={sectionModifiers(t)}>
			{project.setupActions.map((action) => (
				<HStack key={action} spacing={space.md} modifiers={[accessibilityIdentifier(`new-local-project-step-${action}`)]}>
					{project.runningAction === action
						? <ProgressView modifiers={[frame({ width: rowIconWidth })]} />
						: <Image systemName="circle.dashed" color={t.textTertiary} modifiers={[frame({ width: rowIconWidth }), accessibilityHidden(true)]} />}
					<Text modifiers={[foregroundStyle(t.textPrimary)]}>{actionLabel(action)}</Text>
					<Spacer />
				</HStack>
			))}
		</SettingsSection>
	);
}

/**
 * The project's origin, as the desktop asks for it: a new GitHub repository
 * under one of gh's accounts on the machine, private unless turned off, or a
 * remote URL.
 */
function RemoteSection({ project }: { project: NewLocalProject }) {
	const t = useTheme();
	const remote = project.remote;
	return (
		<SettingsSection title={REMOTE_TITLE} footer={remoteFooter(remote, project.remoteResuming)} modifiers={sectionModifiers(t)}>
			<Picker
				label="Remote"
				selection={remote.mode}
				onSelectionChange={(next: string) => {
					const option = REMOTE_MODE_OPTIONS.find((item) => item.key === next);
					if (option) remote.chooseMode(option.key);
				}}
				modifiers={[pickerStyle("segmented"), accessibilityIdentifier("new-local-project-remote-mode")]}
			>
				{REMOTE_MODE_OPTIONS.map((option) => <Text key={option.key} modifiers={[tag(option.key)]}>{option.label}</Text>)}
			</Picker>
			{remote.mode === "github" ? (
				<>
					{remote.owners?.length ? (
						<Picker
							label="Owner"
							selection={remote.owner}
							onSelectionChange={(next: string) => remote.chooseOwner(next)}
							modifiers={[pickerStyle("menu"), tint(t.accent), accessibilityIdentifier("new-local-project-github-owner")]}
						>
							{remote.owners.map((owner) => <Text key={owner.login} modifiers={[tag(owner.login)]}>{owner.login}</Text>)}
						</Picker>
					) : null}
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
				</>
			) : (
				<FormTextField
					label="URL"
					value={remote.url}
					onChangeText={remote.setUrl}
					placeholder="git@github.com:owner/repo.git"
					keyboardType="url"
					autoCapitalize="none"
					autoCorrect={false}
					returnKeyType="done"
					testID="new-local-project-remote-url"
				/>
			)}
		</SettingsSection>
	);
}

/** Where a clone or a new project goes: ~/ao/projects, or where the last one went, unless another is chosen. */
function DestinationSection({ project }: { project: NewLocalProject }) {
	const t = useTheme();
	const shown = displayPath(project.destinationParent, project.home);
	return (
		<SettingsSection title={destinationTitle(project.source)} modifiers={sectionModifiers(t)}>
			<HStack spacing={space.md} modifiers={[accessibilityIdentifier("new-local-project-destination")]}>
				<PathLabel title={placeTitle(project.destinationParent, project.home)} detail={shown} systemImage={symbols.folder.ios} />
				<Button label="Change" onPress={() => project.changeFolder("destination")} modifiers={[buttonStyle("borderless"), disabledModifier(project.busy), accessibilityLabel(`Change where the project is made, from ${shown}`)]} />
			</HStack>
		</SettingsSection>
	);
}

/**
 * The repositories the Orchestrator GitHub App reaches, searched in place: a
 * match fills the URL. Nothing is listed until something is typed.
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
		<SettingsSection title="From GitHub" footer={footer} modifiers={sectionModifiers(t)}>
			<HStack spacing={space.sm}>
				<Image systemName={symbols.search.ios} color={t.textTertiary} modifiers={[frame({ width: rowIconWidth }), accessibilityHidden(true)]} />
				<TextField
					text={text}
					placeholder="Search repositories"
					onTextChange={(next) => change(next)}
					testID="new-local-project-github-search"
					modifiers={[textInputAutocapitalization("never"), autocorrectionDisabled(true), submitLabel("search"), tint(t.highlight), accessibilityLabel("Search GitHub repositories")]}
				/>
				{github.searching ? (
					<Button systemImage="xmark.circle.fill" label="Clear search" onPress={() => change("", true)} modifiers={[labelStyle("iconOnly"), buttonStyle("borderless"), tint(t.textTertiary)]} />
				) : null}
			</HStack>
			{github.matches.map((repo) => (
				<Button key={repo.githubRepositoryId} onPress={() => project.takeRepository(repo)} testID={`new-local-project-github-${repo.githubRepositoryId}`}>
					<RepositoryLabel repo={repo} />
				</Button>
			))}
		</SettingsSection>
	);
}

function RepositoryLabel({ repo }: { repo: CloudGitHubRepository }) {
	return <PathLabel title={repo.fullName} detail={repositoryDetail(repo)} systemImage={repo.isPrivate ? "lock" : symbols.globe.ios} />;
}

/**
 * The machine's folders, in place: the folder shown with Up and, when there is
 * a choice to return to, Cancel; a filter; the choose-this-folder row; where
 * recent projects sat; then its subfolders, repositories first.
 */
function BrowserSection({ project, target }: { project: NewLocalProject; target: BrowseTarget }) {
	const t = useTheme();
	const rows = sectionModifiers(t);
	const listing = project.listing;
	const title = browseTitle(target, project.source);
	const canCancel = target === "destination" || Boolean(project.folder);
	if (project.listingError && !listing) {
		return (
			<SettingsSection title={title} footer={project.listingError} modifiers={rows}>
				<Button label="Try again" systemImage={symbols.refresh.ios} onPress={project.retryListing} testID="new-local-project-retry-folders" />
			</SettingsSection>
		);
	}
	if (!listing) {
		return (
			<SettingsSection title={title} modifiers={rows}>
				<HStack spacing={space.sm}>
					<ProgressView />
					<Text modifiers={[foregroundStyle(t.textSecondary)]}>Loading folders…</Text>
				</HStack>
			</SettingsSection>
		);
	}
	const filtering = project.filter.trim().length > 0;
	const footer = project.listingError
		?? (filtering && project.entries.length === 0 ? noFolderMatchesNote(project.filter) : listing.truncated ? TRUNCATED_NOTE : undefined);
	const iconButton = [labelStyle("iconOnly"), buttonStyle("borderless")];
	return (
		<>
			<SettingsSection title={title} footer={footer} modifiers={rows}>
				<HStack spacing={space.sm} modifiers={[accessibilityIdentifier("new-local-project-browse-path")]}>
					<Button systemImage="arrow.up" label="Up one folder" onPress={() => project.openDirectory(listing.parent)} modifiers={[...iconButton, disabledModifier(project.atRoot || project.listingBusy), tint(t.accent)]} />
					<Text modifiers={[foregroundStyle(t.textPrimary), lineLimit(1)]}>{displayPath(listing.path, project.home)}</Text>
					<Spacer />
					{project.listingBusy ? <ProgressView /> : null}
					{canCancel ? <Button label="Cancel" onPress={project.cancelBrowse} modifiers={[buttonStyle("borderless")]} /> : null}
				</HStack>
				<FolderFilterRow project={project} />
				<Button onPress={() => { if (!project.listingBusy && !project.busy) project.choose(listing.path); }} modifiers={[disabledModifier(project.listingBusy || project.busy)]} testID="new-local-project-choose-here">
					<Label title={chooseHereLabel(target, project.source)} systemImage={symbols.checkmark.ios} />
				</Button>
				{project.entries.map((entry) => (
					<Button key={entry.path} onPress={() => project.pressEntry(entry)} testID={`new-local-project-entry-${entry.name}`}>
						<EntryLabel entry={entry} picks={target === "folder" && entry.gitRepo} />
					</Button>
				))}
			</SettingsSection>
			{project.recentPlaces.length ? (
				<SettingsSection title={RECENT_TITLE} modifiers={rows}>
					{project.recentPlaces.map((path) => (
						<Button key={path} onPress={() => project.openDirectory(path)} testID={`new-local-project-recent-${baseName(path)}`}>
							<PathLabel title={placeTitle(path, project.home)} detail={displayPath(path, project.home)} systemImage="clock.arrow.circlepath" />
						</Button>
					))}
				</SettingsSection>
			) : null}
		</>
	);
}

function FolderFilterRow({ project }: { project: NewLocalProject }) {
	const t = useTheme();
	const text = useNativeState(project.filter);
	const change = useNativeTextInput<string>(project.filter, text, project.setFilter);
	return (
		<HStack spacing={space.sm}>
			<Image systemName={symbols.search.ios} color={t.textTertiary} modifiers={[frame({ width: rowIconWidth }), accessibilityHidden(true)]} />
			<TextField
				text={text}
				placeholder="Filter folders"
				onTextChange={(next) => change(next)}
				testID="new-local-project-filter"
				modifiers={[textInputAutocapitalization("never"), autocorrectionDisabled(true), submitLabel("search"), tint(t.highlight), accessibilityLabel("Filter folders")]}
			/>
			{project.filter ? (
				<Button systemImage="xmark.circle.fill" label="Clear filter" onPress={() => change("", true)} modifiers={[labelStyle("iconOnly"), buttonStyle("borderless"), tint(t.textTertiary)]} />
			) : null}
		</HStack>
	);
}

/**
 * A subfolder's row: a repository, which picks itself, says so under its name;
 * any other folder opens, with the chevron.
 */
function EntryLabel({ entry, picks }: { entry: DirectoryEntry; picks: boolean }) {
	const t = useTheme();
	return (
		<HStack spacing={space.md}>
			<Image systemName={entry.gitRepo ? "arrow.triangle.branch" : symbols.folder.ios} color={entry.gitRepo ? t.accent : t.textSecondary} modifiers={[frame({ width: rowIconWidth }), accessibilityHidden(true)]} />
			<VStack alignment="leading" spacing={space.hair}>
				<Text modifiers={[foregroundStyle(t.textPrimary), lineLimit(1)]}>{entry.name}</Text>
				{entry.gitRepo ? <Text modifiers={[appFont("footnote"), foregroundStyle(t.textSecondary), lineLimit(1)]}>Git repository</Text> : null}
			</VStack>
			<Spacer />
			{picks ? null : <Image systemName="chevron.right" color={t.textTertiary} modifiers={[accessibilityHidden(true)]} />}
		</HStack>
	);
}

function PathLabel({ title, detail, systemImage }: { title: string; detail: string; systemImage: IOSSymbol }) {
	const t = useTheme();
	return (
		<HStack spacing={space.md}>
			<Image systemName={systemImage} color={t.textSecondary} modifiers={[frame({ width: rowIconWidth }), accessibilityHidden(true)]} />
			<VStack alignment="leading" spacing={space.hair}>
				<Text modifiers={[foregroundStyle(t.textPrimary), lineLimit(1)]}>{title}</Text>
				<Text modifiers={[appFont("footnote"), foregroundStyle(t.textSecondary), lineLimit(1)]}>{detail}</Text>
			</VStack>
			<Spacer />
		</HStack>
	);
}

const styles = StyleSheet.create({
	screen: { flex: 1 },
	form: { flex: 1 },
});
