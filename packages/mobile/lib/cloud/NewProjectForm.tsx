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
import { Stack } from "expo-router";
import { useMemo, useRef } from "react";
import { StyleSheet, View } from "react-native";
import { formModifiers, FormTextField, sectionModifiers } from "../formFields";
import { InlineBanner } from "../InlineBanner";
import { SettingsSection } from "../SettingsForm";
import { HarnessImage } from "../spawn-composer-controls.ios";
import { Spinner } from "../Spinner";
import { symbols, type IOSSymbol } from "../symbols";
import { appFont } from "../swiftUIFont";
import { useTheme, useThemeState } from "../ThemeProvider";
import { rowIconWidth, space } from "../tokens";
import { useNativeTextInput } from "../useNativeTextInput";
import { cloudAgentLabel, isCloudAgent, type CloudAgent } from "./agents";
import { PROJECT_NAME_MAX, repositoryDetail, repositorySearchHint, type ProjectSource } from "./newProject";
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
import type { CloudGitHubRepository } from "./types";
import type { NewCloudProject } from "./useNewCloudProject";

export { NEW_PROJECT_TITLE, type NewProjectBarProps, type NewProjectFormProps } from "./NewProjectForm.types";

// iOS: a SwiftUI Form under the sheet's native bar, which holds the title,
// Cancel and Create. The repository is searched for in place, in its own
// section: nothing is listed until something is typed.

/**
 * Cancel and Create in the sheet's native bar, as a form sheet's are. Memoized
 * on what the bar shows, so typing does not register the header again on
 * every key; the buttons call the latest handlers. Create is semibold rather
 * than the done style, which iOS 26 fills with the tint, near white in the
 * dark theme. While the project is being made a spinner holds its place.
 */
export function NewProjectBar({ canCreate, busy, onCancel, onCreate }: NewProjectBarProps) {
	const cancelRef = useRef(onCancel);
	cancelRef.current = onCancel;
	const createRef = useRef(onCreate);
	createRef.current = onCreate;
	return useMemo(() => (
		<>
			<Stack.Toolbar placement="left">
				<Stack.Toolbar.Button onPress={() => cancelRef.current()}>Cancel</Stack.Toolbar.Button>
			</Stack.Toolbar>
			<Stack.Toolbar placement="right">
				{busy ? (
					<Stack.Toolbar.View hidesSharedBackground>
						<Spinner accessibilityLabel="Creating project" />
					</Stack.Toolbar.View>
				) : (
					<Stack.Toolbar.Button accessibilityLabel="Create project" disabled={!canCreate} style={{ fontWeight: "600" }} onPress={() => createRef.current()}>Create</Stack.Toolbar.Button>
				)}
			</Stack.Toolbar>
		</>
	), [busy, canCreate]);
}

export function NewProjectForm({ project, onConnectAgent }: NewProjectFormProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const github = project.source === "github";
	return (
		<View style={[styles.screen, { backgroundColor: t.bgBase }]}>
			{/* Over the form rather than in it, so it shows however far the form has scrolled. */}
			{project.error ? <InlineBanner tone="danger" icon="error" title={project.error} accessibilityRole="alert" onDismiss={project.dismissError} /> : null}
			<Host style={styles.form} colorScheme={scheme}>
				{/* Every row's text in the app's family, scaled with Dynamic Type. */}
				<Form modifiers={[...formModifiers(t), appFont("body")]}>
					{project.githubUnavailable ? null : (
						// The segmented control on its own, on the page rather than in a
						// card: no row fill and no row insets, so it spans the cards' width.
						<SettingsSection>
							<Picker
								label="Source"
								selection={project.source}
								onSelectionChange={(next: string) => {
									const option = SOURCE_OPTIONS.find((item) => item.key === next);
									if (option) project.chooseSource(option.key);
								}}
								modifiers={[pickerStyle("segmented"), listRowBackground("transparent"), listRowInsets({ top: 0, leading: 0, bottom: 0, trailing: 0 }), accessibilityIdentifier("new-project-source")]}
							>
								{SOURCE_OPTIONS.map((option) => <Text key={option.key} modifiers={[tag(option.key)]}>{option.label}</Text>)}
							</Picker>
						</SettingsSection>
					)}
					{github ? <RepositorySection project={project} /> : null}
					<SettingsSection footer={nameNote(project.source)} modifiers={sectionModifiers(t)}>
						<FormTextField
							label="Name"
							value={project.name}
							onChangeText={project.setName}
							placeholder={namePlaceholder(project.source, project.repo)}
							maxLength={PROJECT_NAME_MAX}
							returnKeyType="done"
							testID="new-project-name"
						/>
					</SettingsSection>
					<AgentsSection project={project} onConnectAgent={onConnectAgent} />
				</Form>
			</Host>
		</View>
	);
}

/** The repository's own name is the project's when none is typed, so it shows there. */
function namePlaceholder(source: ProjectSource, repo: CloudGitHubRepository | undefined): string {
	if (source === "standalone") return "My project";
	return repo?.name ?? "Optional";
}

function RepositorySection({ project }: { project: NewCloudProject }) {
	const t = useTheme();
	const rows = sectionModifiers(t);
	if (project.loadingRepos && !project.repos) {
		return (
			<SettingsSection title="Repository" modifiers={rows}>
				<HStack spacing={space.sm}>
					<ProgressView />
					<Text modifiers={[foregroundStyle(t.textSecondary)]}>Loading repositories…</Text>
				</HStack>
			</SettingsSection>
		);
	}
	if (project.reposFailed && !project.repos) {
		return (
			<SettingsSection title="Repository" footer={REPOSITORIES_FAILED_NOTE} modifiers={rows}>
				<Button label="Try again" systemImage={symbols.refresh.ios} onPress={project.retryRepos} testID="new-project-retry-repos" />
			</SettingsSection>
		);
	}
	if (!project.hasInstallation) {
		return (
			<SettingsSection title="Repository" footer={GITHUB_INSTALL_NOTE} modifiers={rows}>
				<GitHubRow title="Connect GitHub" systemImage={symbols.openExternal.ios} busy={project.connecting} onPress={() => void project.connectGitHub()} testID="new-project-connect-github" />
			</SettingsSection>
		);
	}
	const chosen = project.searching ? undefined : project.repo;
	const hint = chosen || project.searching ? undefined : repositorySearchHint(project.repos?.length ?? 0);
	return (
		<SettingsSection title="Repository" footer={hint} modifiers={rows}>
			{chosen ? (
				// The choice, once made; Change goes back to searching.
				<HStack spacing={space.md} modifiers={[accessibilityIdentifier("new-project-repo")]}>
					<RepositoryLabel repo={chosen} />
					<Button label="Change" onPress={project.changeRepo} modifiers={[buttonStyle("borderless"), accessibilityLabel(`Change repository from ${chosen.fullName}`)]} />
				</HStack>
			) : (
				<>
					<RepositorySearchRow project={project} />
					{project.searching && project.matches.length === 0 ? (
						<Text modifiers={[foregroundStyle(t.textSecondary)]}>{noMatchesNote(project.query)}</Text>
					) : null}
					{project.matches.map((item) => (
						<Button
							key={item.githubRepositoryId}
							onPress={() => project.chooseRepo(item.githubRepositoryId)}
							testID={`new-project-repo-${item.githubRepositoryId}`}
						>
							<RepositoryLabel repo={item} />
						</Button>
					))}
				</>
			)}
			<GitHubRow title="Add repositories on GitHub" systemImage={symbols.add.ios} busy={project.connecting} onPress={() => void project.connectGitHub()} testID="new-project-add-repos" />
		</SettingsSection>
	);
}

/**
 * The search, as a row of its own: the glass, the field, and at its end a
 * clear button while something is typed, Refresh otherwise. Borderless, or a
 * Form row hands every tap in it to the button.
 */
function RepositorySearchRow({ project }: { project: NewCloudProject }) {
	const t = useTheme();
	const text = useNativeState(project.query);
	const change = useNativeTextInput<string>(project.query, text, project.setQuery);
	const iconButton = [labelStyle("iconOnly"), buttonStyle("borderless")];
	return (
		<HStack spacing={space.sm}>
			<Image systemName={symbols.search.ios} color={t.textTertiary} modifiers={[frame({ width: rowIconWidth }), accessibilityHidden(true)]} />
			<TextField
				text={text}
				placeholder="Search repositories"
				autoFocus={project.reselecting}
				onTextChange={(next) => change(next)}
				testID="new-project-repo-search"
				modifiers={[
					textInputAutocapitalization("never"),
					autocorrectionDisabled(true),
					submitLabel("search"),
					// The caret and selection take the tint; the app's other fields use this colour for both.
					tint(t.highlight),
					accessibilityLabel("Search repositories"),
				]}
			/>
			{project.searching ? (
				<Button systemImage="xmark.circle.fill" label="Clear search" onPress={() => change("", true)} modifiers={[...iconButton, tint(t.textTertiary)]} />
			) : project.loadingRepos ? (
				<ProgressView />
			) : (
				<Button systemImage={symbols.refresh.ios} label="Refresh repositories" onPress={() => void project.refreshRepos()} modifiers={[...iconButton, tint(t.textSecondary)]} />
			)}
		</HStack>
	);
}

function repositoryGlyph(repo: CloudGitHubRepository): IOSSymbol {
	return repo.isPrivate ? "lock" : symbols.globe.ios;
}

/** A repository's row face: whether it is private, its full name, and its visibility and branch under it. */
function RepositoryLabel({ repo }: { repo: CloudGitHubRepository }) {
	const t = useTheme();
	return (
		<HStack spacing={space.md}>
			<Image systemName={repositoryGlyph(repo)} color={t.textSecondary} modifiers={[frame({ width: rowIconWidth }), accessibilityHidden(true)]} />
			<VStack alignment="leading" spacing={space.hair}>
				<Text modifiers={[foregroundStyle(t.textPrimary), lineLimit(1)]}>{repo.fullName}</Text>
				<Text modifiers={[appFont("footnote"), foregroundStyle(t.textSecondary), lineLimit(1)]}>{repositoryDetail(repo)}</Text>
			</VStack>
			<Spacer />
		</HStack>
	);
}

/**
 * A row that opens GitHub's install page, in the tint. While the phone waits
 * for GitHub to report the installation it says so, with a spinner, and holds.
 */
function GitHubRow({ title, systemImage, busy, onPress, testID }: { title: string; systemImage: IOSSymbol; busy: boolean; onPress(): void; testID: string }) {
	return (
		<Button onPress={onPress} testID={testID} modifiers={[disabledModifier(busy)]}>
			<HStack spacing={space.sm}>
				<Label title={busy ? WAITING_FOR_GITHUB : title} systemImage={systemImage} />
				<Spacer />
				{busy ? <ProgressView /> : null}
			</HStack>
		</Button>
	);
}

function AgentsSection({ project, onConnectAgent }: NewProjectFormProps) {
	const t = useTheme();
	if (!project.available.length) {
		return (
			<SettingsSection title="Agents" footer={NO_AGENT_NOTE} modifiers={sectionModifiers(t)}>
				<Button label="Connect a coding agent" systemImage={symbols.add.ios} onPress={onConnectAgent} testID="new-project-connect-agent" />
			</SettingsSection>
		);
	}
	return (
		<SettingsSection title="Agents" modifiers={sectionModifiers(t)}>
			<AgentPicker label="Workers run" agents={project.available} value={project.workerAgent} onChange={project.chooseWorkerAgent} testID="new-project-worker-agent" />
			<AgentPicker label="Orchestrator runs" agents={project.available} value={project.orchestratorAgent} onChange={project.chooseOrchestratorAgent} testID="new-project-orchestrator-agent" />
		</SettingsSection>
	);
}

/**
 * A menu picker row: the system draws the current agent in the tint with the
 * up-down chevrons, each choice with its brand mark, and the checkmark on the
 * chosen one.
 */
function AgentPicker({ label, agents, value, onChange, testID }: { label: string; agents: readonly CloudAgent[]; value?: CloudAgent; onChange(agent: CloudAgent): void; testID: string }) {
	const t = useTheme();
	return (
		<Picker
			label={label}
			selection={value ?? ""}
			onSelectionChange={(next: string) => { if (isCloudAgent(next) && next !== value) onChange(next); }}
			modifiers={[pickerStyle("menu"), tint(t.accent), accessibilityIdentifier(testID)]}
		>
			{agents.map((agent) => (
				<Label key={agent} title={cloudAgentLabel(agent)} icon={<HarnessImage harness={agent} />} modifiers={[tag(agent)]} />
			))}
		</Picker>
	);
}

const styles = StyleSheet.create({
	screen: { flex: 1 },
	form: { flex: 1 },
});
