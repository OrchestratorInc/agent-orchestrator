import { Feather } from "@expo/vector-icons";
import { CloudApiError, type GitHubInstallation, type GitHubRepository } from "@aoagents/cloud-client";
import * as Crypto from "expo-crypto";
import { useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Platform, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AgentLogo } from "../lib/AgentLogo";
import { backOr } from "../lib/backNavigation";
import { readyHarnesses } from "../lib/cloud/agentReadiness";
import { useCloudAuth } from "../lib/cloud/authStore";
import { loadGrantedRepositories } from "../lib/cloud/githubProjectRepositories";
import { cloudProjectInput, initialProjectCreationStep, validGitHubRepositoryURL } from "../lib/cloud/projectCreation";
import { haptics } from "../lib/haptics";
import { openGitHub } from "../lib/openGitHub";
import { useApp } from "../lib/store";
import type { Theme } from "../lib/theme";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { Button, HeaderIconButton } from "../lib/ui";

export { SheetErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

type Step = "loading" | "unavailable" | "github" | "github-token" | "repository" | "agents";

const AGENT_LABELS: Record<string, string> = {
	"claude-code": "Claude Code",
	codex: "Codex",
	cursor: "Cursor",
};

export default function CreateCloudProject() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { environment, refresh, setActiveProject } = useApp();
	const { client, signedIn, orgId, orgLoading, orgError, retryOrgResolution } = useCloudAuth();
	const [step, setStep] = useState<Step>("loading");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [token, setToken] = useState("");
	const [repositoryUrl, setRepositoryUrl] = useState("");
	const [displayName, setDisplayName] = useState("");
	const [defaultBranch, setDefaultBranch] = useState("main");
	const [readOnly, setReadOnly] = useState(false);
	const [agents, setAgents] = useState<string[]>([]);
	const [workerAgent, setWorkerAgent] = useState("");
	const [orchestratorAgent, setOrchestratorAgent] = useState("");
	const [installations, setInstallations] = useState<GitHubInstallation[]>([]);
	const [repositories, setRepositories] = useState<GitHubRepository[]>([]);
	const [repositorySearch, setRepositorySearch] = useState("");
	const [selectedRepositoryId, setSelectedRepositoryId] = useState("");
	const [projectSource, setProjectSource] = useState<"app" | "manual">("app");
	const [githubError, setGithubError] = useState<string | null>(null);
	const [connecting, setConnecting] = useState(false);
	const [openingGitHub, setOpeningGitHub] = useState(false);
	const [loadingRepositories, setLoadingRepositories] = useState(false);
	const [hasSavedToken, setHasSavedToken] = useState(false);
	const creationKey = useRef<string | null>(null);
	const connectAttempt = useRef(0);
	const connectionBaseline = useRef<Map<string, string> | null>(null);
	const requestAbort = useRef<AbortController | null>(null);
	const syncAttempted = useRef<Set<string>>(new Set());
	const selectedRepository = repositories.find((repository) => repository.githubRepositoryId === selectedRepositoryId);
	const activeInstallations = installations.filter((installation) => installation.status === "active");
	const readyInstallations = activeInstallations.filter((installation) => installation.syncStatus === "ready");
	const waitingForGitHub = connecting && (openingGitHub || connectionBaseline.current !== null);
	const githubReadyToImport = readyInstallations.length > 0 && !waitingForGitHub;
	const githubStatusText = openingGitHub ? "Opening GitHub…"
		: activeInstallations.some((installation) => installation.syncStatus === "pending") ? `Syncing repositories from ${activeInstallations.map((item) => item.accountLogin).join(", ")}`
		: waitingForGitHub ? "Waiting for GitHub approval…"
		: readyInstallations.length > 0 ? `Connected to ${readyInstallations.map((item) => item.accountLogin).join(", ")}`
		: connecting ? `Syncing repositories from ${activeInstallations.map((item) => item.accountLogin).join(", ")}`
		: `Repository sync needs attention for ${activeInstallations.map((item) => item.accountLogin).join(", ")}`;
	const visibleRepositories = useMemo(() => repositories.filter((repository) => repository.fullName.toLowerCase().includes(repositorySearch.trim().toLowerCase())), [repositories, repositorySearch]);
	const shownRepositories = visibleRepositories.slice(0, 30);

	const refreshGitHub = useCallback(async (signal: AbortSignal) => {
		if (!orgId) return false;
		const nextInstallations = await client.listGitHubInstallations(orgId, { signal });
		if (signal.aborted) return false;
		setInstallations(nextInstallations);
		let active = nextInstallations.filter((installation) => installation.status === "active");
		let ready = active.some((installation) => installation.syncStatus === "ready");
		if (!ready) {
			const needsSync = active.find((installation) => installation.syncStatus === "pending" && !syncAttempted.current.has(installation.id));
			if (needsSync) {
				syncAttempted.current.add(needsSync.id);
				await client.syncGitHubInstallation(orgId, needsSync.id, { signal });
				if (signal.aborted) return false;
				const updated = await client.listGitHubInstallations(orgId, { signal });
				if (signal.aborted) return false;
				setInstallations(updated);
				active = updated.filter((installation) => installation.status === "active");
				ready = active.some((installation) => installation.syncStatus === "ready");
			}
		}
		const connectionReady = ready && (connectionBaseline.current === null || active.some((installation) =>
			installation.syncStatus === "ready" && connectionBaseline.current?.get(installation.id) !== installation.updatedAt,
		));
		if (!signal.aborted && active.length > 0) {
			if (connectionReady) {
				connectionBaseline.current = null;
				setConnecting(false);
			}
			else if (active.some((installation) => installation.syncStatus === "failed")) {
				connectionBaseline.current = null;
				setConnecting(false);
				setGithubError(active.find((installation) => installation.syncStatus === "failed")?.lastError ?? "GitHub repository sync failed. Try again.");
			} else setConnecting(true);
		}
		if (ready) {
			setLoadingRepositories(true);
			try {
				const granted = await loadGrantedRepositories(client, orgId, signal);
				if (!signal.aborted) {
					setRepositories(granted);
					setSelectedRepositoryId((previous) => granted.some((repository) => repository.githubRepositoryId === previous) ? previous : "");
					setGithubError(null);
				}
			} finally {
				if (!signal.aborted) setLoadingRepositories(false);
			}
		} else if (!signal.aborted) {
			setRepositories([]);
			setSelectedRepositoryId("");
		}
		return connectionReady;
	}, [client, orgId]);

	useEffect(() => {
		if (environment !== "cloud") {
			setError("Switch to Cloud before adding a project.");
			setStep("unavailable");
			return;
		}
		if (signedIn === null || orgLoading) {
			setError(null);
			setStep("loading");
			return;
		}
		if (!signedIn) {
			setError("Sign in to AO Cloud before adding a project.");
			setStep("unavailable");
			return;
		}
		if (!orgId) {
			setError(orgError ?? "Could not load your AO Cloud workspace.");
			setStep("unavailable");
			return;
		}
		let active = true;
		const abort = new AbortController();
		requestAbort.current?.abort();
		requestAbort.current = abort;
		syncAttempted.current.clear();
		setError(null);
		setGithubError(null);
		setProjectSource("app");
		setSelectedRepositoryId("");
		setRepositories([]);
		setInstallations([]);
		setConnecting(false);
		setOpeningGitHub(false);
		connectAttempt.current += 1;
		connectionBaseline.current = null;
		creationKey.current = null;
		setStep("loading");
		Promise.allSettled([client.listUserProviderConnections({ signal: abort.signal }), client.listProviderConnections(orgId, { signal: abort.signal }), refreshGitHub(abort.signal)]).then((results) => {
			if (!active) return;
			const userConnections = results[0].status === "fulfilled" ? results[0].value : [];
			const available = [results[0], results[1]].flatMap((result) => result.status === "fulfilled" ? result.value : []);
			const harnesses = readyHarnesses(available);
			setAgents(harnesses);
			setWorkerAgent(harnesses[0] ?? "");
			setOrchestratorAgent(harnesses[0] ?? "");
			setHasSavedToken(initialProjectCreationStep(userConnections) === "repository");
			setStep("github");
			if (results[0].status === "rejected") {
				setGithubError(messageFor(results[0].reason, "Could not check saved GitHub access."));
			}
			if (results[2].status === "rejected") {
				setGithubError(messageFor(results[2].reason, "Could not load GitHub repositories."));
			}
		}).catch((cause) => {
			if (!active) return;
			setError(messageFor(cause, "Could not load Cloud project setup."));
			setStep("github");
		});
		return () => { active = false; abort.abort(); };
	}, [client, environment, signedIn, orgId, orgLoading, orgError, refreshGitHub]);

	useEffect(() => {
		if (!connecting || openingGitHub || !orgId || step !== "github") return;
		const interval = setInterval(() => {
			const signal = requestAbort.current?.signal;
			if (!signal || signal.aborted) return;
			void refreshGitHub(signal).then((ready) => { if (ready) setConnecting(false); }).catch((cause) => {
				if (!signal.aborted) setGithubError(messageFor(cause, "Could not finish connecting GitHub."));
			});
		}, 2500);
		const timeout = setTimeout(() => {
			connectAttempt.current += 1;
			connectionBaseline.current = null;
			setConnecting(false);
			setGithubError("GitHub did not finish connecting. Try again, or set up manually.");
		}, 5 * 60_000);
		return () => { clearInterval(interval); clearTimeout(timeout); };
	}, [connecting, openingGitHub, orgId, step, refreshGitHub]);

	const connectGitHub = async () => {
		if (!orgId || connecting) return;
		const attempt = ++connectAttempt.current;
		setGithubError(null);
		setConnecting(true);
		setOpeningGitHub(true);
		try {
			const signal = requestAbort.current?.signal;
			const before = await client.listGitHubInstallations(orgId, { signal });
			if (signal?.aborted || connectAttempt.current !== attempt) return;
			connectionBaseline.current = new Map(before.map((installation) => [installation.id, installation.updatedAt]));
			const { installationUrl } = await client.startGitHubInstallation(orgId, { signal });
			if (signal?.aborted || connectAttempt.current !== attempt) return;
			setOpeningGitHub(false);
			await openGitHub(installationUrl);
			if (signal?.aborted || connectAttempt.current !== attempt) return;
			if (await refreshGitHub(signal ?? new AbortController().signal)) setConnecting(false);
		} catch (cause) {
			if (requestAbort.current?.signal.aborted || connectAttempt.current !== attempt) return;
			setGithubError(messageFor(cause, "Could not connect GitHub."));
			connectionBaseline.current = null;
			setConnecting(false);
			setOpeningGitHub(false);
		}
	};

	const stopWaiting = () => {
		connectAttempt.current += 1;
		connectionBaseline.current = null;
		setConnecting(false);
		setOpeningGitHub(false);
	};

	const syncRepositories = async () => {
		if (!orgId || busy) return;
		setBusy(true);
		setGithubError(null);
		try {
			const signal = requestAbort.current?.signal ?? new AbortController().signal;
			for (const installation of activeInstallations.filter((item) => item.syncStatus !== "ready")) {
				await client.syncGitHubInstallation(orgId, installation.id, { signal });
			}
			if (!await refreshGitHub(signal)) setConnecting(true);
		} catch (cause) {
			setGithubError(messageFor(cause, "Could not refresh GitHub repositories."));
		} finally { setBusy(false); }
	};

	const saveToken = async () => {
		if (busy || token.trim().length < 8) {
			setError("Enter a GitHub personal access token.");
			return;
		}
		setBusy(true);
		setError(null);
		try {
			await client.putGitHubPAT({ secret: token.trim() });
			setToken("");
			setHasSavedToken(true);
			creationKey.current = null;
			setStep("repository");
		} catch (cause) {
			setError(messageFor(cause, "Could not save the GitHub token."));
		} finally {
			setBusy(false);
		}
	};

	const checkRepository = async () => {
		if (busy) return;
		if (!validGitHubRepositoryURL(repositoryUrl)) {
			setError("Enter a GitHub repository URL like https://github.com/owner/repo.");
			return;
		}
		if (!displayName.trim() || !defaultBranch.trim()) {
			setError("Project name and default branch are required.");
			return;
		}
		setBusy(true);
		setError(null);
		setReadOnly(false);
		try {
			const access = await client.validateSavedRepositoryAccess({ repositoryUrl: repositoryUrl.trim() });
			if (access.writeAccess) setStep("agents");
			else setReadOnly(true);
		} catch (cause) {
			if (cause instanceof CloudApiError && cause.code === "token_missing") {
				setStep("github-token");
				setError("Add a GitHub token before importing this repository.");
			} else {
				setError(messageFor(cause, "Could not verify repository access."));
			}
		} finally {
			setBusy(false);
		}
	};

	const createProject = async () => {
		if (busy || !orgId) return;
		if (projectSource === "app" && !selectedRepository) {
			setError("This repository is no longer available. Choose another repository.");
			setStep("github");
			return;
		}
		if (!workerAgent || !orchestratorAgent) {
			setError("Configure a Cloud agent credential in the desktop app first.");
			return;
		}
		setBusy(true);
		setError(null);
		try {
			creationKey.current ??= Crypto.randomUUID();
			const config = { worker: { agent: workerAgent }, orchestrator: { agent: orchestratorAgent } };
			const { project } = projectSource === "app" && selectedRepository
				? await client.createProjectFromGitHub(orgId, { githubRepositoryId: selectedRepository.githubRepositoryId, displayName: displayName.trim(), config }, { idempotencyKey: creationKey.current })
				: await client.createProject(orgId, cloudProjectInput({ displayName, repositoryUrl, defaultBranch, workerAgent, orchestratorAgent }), { idempotencyKey: creationKey.current });
			setActiveProject(project.id);
			await refresh().catch(() => {});
			router.replace({ pathname: "/project/[id]", params: { id: project.id } });
		} catch (cause) {
			if (projectSource === "manual" && cause instanceof CloudApiError && (cause.code === "repository_unreachable" || cause.code === "read_only_token" || cause.code === "token_missing")) {
				setStep(cause.code === "token_missing" ? "github-token" : "repository");
			}
			setError(messageFor(cause, "Could not create the project."));
			setBusy(false);
		}
	};

	const back = () => {
		haptics.tap();
		if (step === "agents") { setError(null); setStep(projectSource === "app" ? "github" : "repository"); }
		else if (step === "github-token" || step === "repository") { setError(null); setStep("github"); }
		else backOr(router);
	};

	return (
		<View style={styles.screen} collapsable={false}>
			<View style={styles.header}>
				<HeaderIconButton icon="back" label="Back" onPress={back} />
				<Text style={styles.headerTitle}>Add Cloud project</Text>
				<HeaderIconButton icon="close" label="Close" onPress={() => backOr(router)} />
			</View>
			<ScrollView contentInsetAdjustmentBehavior="automatic" automaticallyAdjustKeyboardInsets={Platform.OS === "ios"} keyboardShouldPersistTaps="handled" contentContainerStyle={{ paddingBottom: insets.bottom + 28 }}>
				<View style={styles.content}>
				{step === "loading" ? <ActivityIndicator color={t.accent} style={styles.loading} /> : null}
				{step === "unavailable" ? (
					<View style={styles.group}>
						<Text style={styles.title}>Can't add a project yet</Text>
						<Text style={styles.description}>{error}</Text>
						{signedIn && orgError ? <Button title="Retry" onPress={() => void retryOrgResolution()} /> : null}
						<Button title="Close" variant="ghost" onPress={() => backOr(router)} />
					</View>
				) : null}
				{step === "github" ? (
					<View style={styles.group}>
						<Text style={styles.title}>Choose a repository</Text>
						<Text style={styles.description}>Import a repository you’ve granted AO access to on GitHub.</Text>
						{activeInstallations.length === 0 ? (
							<View style={styles.connectCard}>
								<View style={styles.connectHeading}><Feather name="github" size={22} color={t.textPrimary} /><Text style={styles.connectTitle}>Connect GitHub</Text></View>
								<Text style={styles.hint}>GitHub will open in a browser so you can choose which repositories AO may access. No token to copy or paste.</Text>
								<Button title={openingGitHub ? "Opening GitHub…" : connecting ? "Waiting for GitHub approval…" : "Connect GitHub"} loading={connecting} disabled={connecting} onPress={() => void connectGitHub()} />
								{connecting ? <Button title="Stop waiting" variant="ghost" onPress={stopWaiting} /> : null}
							</View>
						) : (
							<>
								<View style={styles.connectedRow}>
									<Feather name={githubReadyToImport ? "check-circle" : connecting ? "loader" : "alert-circle"} size={17} color={githubReadyToImport ? t.green : t.amber} />
									<Text style={styles.connectedText}>{githubStatusText}</Text>
								</View>
								{activeInstallations.some((item) => item.syncStatus !== "ready") ? <Button title={connecting ? "Syncing repositories…" : "Retry repository sync"} variant="ghost" loading={busy} disabled={connecting} onPress={() => void syncRepositories()} /> : null}
								{connecting ? <Button title="Stop waiting" variant="ghost" onPress={stopWaiting} /> : null}
								{loadingRepositories ? <ActivityIndicator color={t.accent} /> : null}
								{repositories.length > 0 ? (
									<>
										<TextInput style={styles.input} value={repositorySearch} onChangeText={setRepositorySearch} placeholder="Search repositories" placeholderTextColor={t.textTertiary} autoCapitalize="none" autoCorrect={false} accessibilityLabel="Search repositories" />
										<View style={styles.repositoryList}>
											{visibleRepositories.length === 0 ? <Text style={styles.emptyText}>No matching repositories</Text> : shownRepositories.map((repository) => (
												<Pressable key={repository.githubRepositoryId} accessibilityRole="radio" accessibilityState={{ selected: selectedRepositoryId === repository.githubRepositoryId }} accessibilityLabel={`Select ${repository.fullName}`} onPress={() => { haptics.select(); setProjectSource("app"); setSelectedRepositoryId(repository.githubRepositoryId); setDisplayName(repository.name); setRepositoryUrl(repository.htmlUrl); setDefaultBranch(repository.defaultBranch); creationKey.current = null; }} style={[styles.repositoryOption, selectedRepositoryId === repository.githubRepositoryId && styles.repositorySelected]}>
													<View style={styles.repositoryIdentity}><Feather name="book" size={18} color={t.textSecondary} /><View style={styles.repositoryCopy}><Text style={styles.repositoryName} numberOfLines={1}>{repository.fullName}</Text><Text style={styles.repositoryMeta}>{repository.isPrivate ? "Private" : "Public"} · {repository.defaultBranch}</Text></View></View>
													{selectedRepositoryId === repository.githubRepositoryId ? <Feather name="check" size={18} color={t.accent} /> : null}
												</Pressable>
											))}
										</View>
										{visibleRepositories.length > shownRepositories.length ? <Text style={styles.hint}>Showing the first 30 repositories. Search to narrow the list.</Text> : null}
										<Button title="Continue" disabled={!selectedRepository} onPress={() => { setError(null); setStep("agents"); }} />
									</>
								) : !loadingRepositories && readyInstallations.length > 0 ? (
									<View style={styles.group}>
										<Text style={styles.hint}>No repositories are available to import. Grant AO access to a repository in GitHub, then refresh this list.</Text>
										<Button title="Manage GitHub access" variant="ghost" icon="external-link" disabled={connecting} onPress={() => void connectGitHub()} />
									</View>
								) : null}
								<Button title="Refresh repositories" variant="ghost" onPress={() => { const signal = requestAbort.current?.signal; if (signal) void refreshGitHub(signal).catch((cause) => setGithubError(messageFor(cause, "Could not refresh repositories."))); }} />
							</>
						)}
						{githubError ? <Text accessibilityRole="alert" style={styles.error}>{githubError}</Text> : null}
						<Button title="Set up manually with a token" variant="ghost" onPress={() => { setError(null); stopWaiting(); setProjectSource("manual"); setSelectedRepositoryId(""); setRepositoryUrl(""); setDisplayName(""); creationKey.current = null; setStep(hasSavedToken ? "repository" : "github-token"); }} />
					</View>
				) : null}
				{step === "github-token" ? (
					<View style={styles.group}>
						<Text style={styles.title}>Connect GitHub</Text>
						<Text style={styles.description}>For manual setup, add a fine-grained token with access to the repository. AO Cloud stores it securely and never shows it again.</Text>
						<Text style={styles.label}>GitHub access token</Text>
						<TextInput style={styles.input} value={token} onChangeText={setToken} placeholder="github_pat_…" placeholderTextColor={t.textTertiary} secureTextEntry autoCapitalize="none" autoCorrect={false} textContentType="password" accessibilityLabel="GitHub access token" />
						<Text style={styles.hint}>Use a fine-grained token with Contents read and write access to the repository.</Text>
						<Button title="Create a GitHub token" variant="ghost" icon="external-link" onPress={() => void openGitHub("https://github.com/settings/personal-access-tokens/new")} />
						<Button title="Save token and continue" loading={busy} disabled={token.trim().length < 8} onPress={() => void saveToken()} />
					</View>
				) : null}
				{step === "repository" ? (
					<View style={styles.group}>
						<Text style={styles.title}>Import a repository</Text>
						<Text style={styles.description}>Enter the repository URL and branch for this project.</Text>
						<Text style={styles.label}>Repository URL</Text>
						<TextInput style={styles.input} value={repositoryUrl} onChangeText={(value) => { setRepositoryUrl(value); setReadOnly(false); creationKey.current = null; }} placeholder="https://github.com/owner/repo" placeholderTextColor={t.textTertiary} autoCapitalize="none" autoCorrect={false} keyboardType="url" accessibilityLabel="Repository URL" />
						<Text style={styles.label}>Project name</Text>
						<TextInput style={styles.input} value={displayName} onChangeText={(value) => { setDisplayName(value); creationKey.current = null; }} placeholder="My project" placeholderTextColor={t.textTertiary} accessibilityLabel="Project name" />
						<Text style={styles.label}>Default branch</Text>
						<TextInput style={styles.input} value={defaultBranch} onChangeText={(value) => { setDefaultBranch(value); creationKey.current = null; }} placeholder="main" placeholderTextColor={t.textTertiary} autoCapitalize="none" autoCorrect={false} accessibilityLabel="Default branch" />
						{readOnly ? <Text style={styles.warning}>Your GitHub token can read this repository but cannot push changes. Workers may be unable to publish their work.</Text> : null}
						<Button title={readOnly ? "Continue anyway" : "Check repository access"} loading={busy} onPress={() => readOnly ? (setReadOnly(false), setStep("agents")) : void checkRepository()} />
						<Button title="Change GitHub token" variant="ghost" onPress={() => { setError(null); setStep("github-token"); }} />
					</View>
				) : null}
				{step === "agents" ? (
					<View style={styles.group}>
						<Text style={styles.title}>Choose your agents</Text>
						{selectedRepository ? <View style={styles.selectedSummary}><Feather name="book" size={18} color={t.textSecondary} /><View><Text style={styles.repositoryName}>{selectedRepository.fullName}</Text><Text style={styles.repositoryMeta}>Default branch · {selectedRepository.defaultBranch}</Text></View></View> : null}
						{selectedRepository ? <><Text style={styles.label}>Project name</Text><TextInput style={styles.input} value={displayName} onChangeText={(value) => { setDisplayName(value); creationKey.current = null; }} placeholder="Project name" placeholderTextColor={t.textTertiary} accessibilityLabel="Project name" /></> : null}
						<Text style={styles.description}>Choose the agents that build and coordinate this project.</Text>
						{agents.length === 0 ? <Text style={styles.warning}>No Cloud agent credential is ready. Add a Claude Code, Codex, or Cursor credential in desktop Settings, then reopen this form.</Text> : (
							<>
								<Text style={styles.label}>Worker agent</Text>
								{agents.map((agent) => <AgentOption key={`worker-${agent}`} agent={agent} selected={workerAgent === agent} onPress={() => { setWorkerAgent(agent); creationKey.current = null; }} styles={styles} color={t.accent} />)}
								<Text style={styles.label}>Orchestrator agent</Text>
								{agents.map((agent) => <AgentOption key={`orchestrator-${agent}`} agent={agent} selected={orchestratorAgent === agent} onPress={() => { setOrchestratorAgent(agent); creationKey.current = null; }} styles={styles} color={t.accent} />)}
							</>
						)}
						<Button title="Create project" loading={busy} disabled={agents.length === 0 || !displayName.trim()} onPress={() => void createProject()} />
					</View>
				) : null}
				{error && step !== "unavailable" ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
				</View>
			</ScrollView>
		</View>
	);
}

function AgentOption({ agent, selected, onPress, styles, color }: {
	agent: string;
	selected: boolean;
	onPress: () => void;
	styles: ReturnType<typeof makeStyles>;
	color: string;
}) {
	return <Pressable accessibilityRole="radio" accessibilityState={{ selected }} onPress={() => { haptics.select(); onPress(); }} style={[styles.agentOption, selected && styles.agentSelected]}>
		<View style={styles.agentIdentity}>
			<AgentLogo harness={agent} size={24} />
			<Text style={styles.agentText}>{AGENT_LABELS[agent] ?? agent}</Text>
		</View>
		{selected ? <Feather name="check" size={19} color={color} /> : null}
	</Pressable>;
}

function messageFor(cause: unknown, fallback: string): string {
	return cause instanceof Error && cause.message ? cause.message : fallback;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgSurface },
	header: { minHeight: 66, flexDirection: "row", alignItems: "center", paddingHorizontal: 10, borderBottomWidth: 1, borderBottomColor: t.borderSubtle },
	headerTitle: { flex: 1, textAlign: "center", color: t.textPrimary, fontSize: 18, fontWeight: "700" },
	content: { paddingHorizontal: 22, paddingTop: 24, gap: 18 },
	loading: { marginTop: 56 },
	group: { gap: 13 },
	connectCard: { gap: 13, padding: 18, borderWidth: 1, borderColor: t.borderDefault, borderRadius: 16, backgroundColor: t.bgElevated },
	connectHeading: { flexDirection: "row", alignItems: "center", gap: 10 },
	connectTitle: { color: t.textPrimary, fontSize: 17, fontWeight: "600" },
	connectedRow: { flexDirection: "row", alignItems: "center", gap: 8, paddingVertical: 4 },
	connectedText: { color: t.textSecondary, fontSize: 14, flexShrink: 1 },
	repositoryList: { borderWidth: 1, borderColor: t.borderDefault, borderRadius: 14, overflow: "hidden", backgroundColor: t.bgElevated },
	repositoryOption: { minHeight: 63, paddingHorizontal: 14, paddingVertical: 10, flexDirection: "row", alignItems: "center", justifyContent: "space-between", borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: t.borderSubtle },
	repositorySelected: { backgroundColor: t.accentTint },
	repositoryIdentity: { flexDirection: "row", alignItems: "center", gap: 12, flex: 1 },
	repositoryCopy: { flex: 1, gap: 3 },
	repositoryName: { color: t.textPrimary, fontSize: 15, fontWeight: "600" },
	repositoryMeta: { color: t.textTertiary, fontSize: 12 },
	emptyText: { color: t.textSecondary, fontSize: 14, padding: 18 },
	selectedSummary: { flexDirection: "row", alignItems: "center", gap: 12, padding: 14, borderRadius: 12, backgroundColor: t.bgElevated },
	title: { fontSize: 25, fontWeight: "700", color: t.textPrimary },
	description: { color: t.textSecondary, fontSize: 14, lineHeight: 21, marginBottom: 8 },
	label: { color: t.textPrimary, fontSize: 14, fontWeight: "600", marginTop: 7 },
	input: { minHeight: 51, paddingHorizontal: 14, borderWidth: 1, borderColor: t.borderDefault, borderRadius: 12, backgroundColor: t.bgElevated, color: t.textPrimary, fontSize: 15 },
	hint: { color: t.textSecondary, fontSize: 13, lineHeight: 19 },
	warning: { color: t.amber, fontSize: 14, lineHeight: 21, marginVertical: 7 },
	error: { color: t.red, fontSize: 14, lineHeight: 21 },
	agentOption: { minHeight: 52, paddingHorizontal: 15, borderWidth: 1, borderColor: t.borderDefault, borderRadius: 12, backgroundColor: t.bgElevated, flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
	agentIdentity: { flexDirection: "row", alignItems: "center", gap: 12 },
	agentSelected: { borderColor: t.accent, backgroundColor: t.accentTint },
	agentText: { color: t.textPrimary, fontSize: 15, fontWeight: "600" },
});
