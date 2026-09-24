import { Feather } from "@expo/vector-icons";
import { CloudApiError } from "@aoagents/cloud-client";
import * as Crypto from "expo-crypto";
import { useRouter } from "expo-router";
import { useEffect, useRef, useState } from "react";
import { ActivityIndicator, Platform, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AgentLogo } from "../lib/AgentLogo";
import { backOr } from "../lib/backNavigation";
import { readyHarnesses } from "../lib/cloud/agentReadiness";
import { useCloudAuth } from "../lib/cloud/authStore";
import { cloudProjectInput, initialProjectCreationStep, validGitHubRepositoryURL } from "../lib/cloud/projectCreation";
import { haptics } from "../lib/haptics";
import { openGitHub } from "../lib/openGitHub";
import { useApp } from "../lib/store";
import type { Theme } from "../lib/theme";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { Button, HeaderIconButton } from "../lib/ui";

export { SheetErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

type Step = "loading" | "unavailable" | "github-token" | "repository" | "agents";

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
	const creationKey = useRef<string | null>(null);

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
		setError(null);
		setStep("loading");
		Promise.allSettled([client.listUserProviderConnections(), client.listProviderConnections(orgId)]).then((results) => {
			if (!active) return;
			const userConnections = results[0].status === "fulfilled" ? results[0].value : [];
			const available = results.flatMap((result) => result.status === "fulfilled" ? result.value : []);
			const harnesses = readyHarnesses(available);
			setAgents(harnesses);
			setWorkerAgent(harnesses[0] ?? "");
			setOrchestratorAgent(harnesses[0] ?? "");
			setStep(initialProjectCreationStep(userConnections));
			if (results[0].status === "rejected") {
				setError(messageFor(results[0].reason, "Could not check GitHub access."));
			}
		}).catch((cause) => {
			if (!active) return;
			setError(messageFor(cause, "Could not load Cloud project setup."));
			setStep("repository");
		});
		return () => { active = false; };
	}, [client, environment, signedIn, orgId, orgLoading, orgError]);

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
		if (!workerAgent || !orchestratorAgent) {
			setError("Configure a Cloud agent credential in the desktop app first.");
			return;
		}
		setBusy(true);
		setError(null);
		try {
			creationKey.current ??= Crypto.randomUUID();
			const { project } = await client.createProject(
				orgId,
				cloudProjectInput({ displayName, repositoryUrl, defaultBranch, workerAgent, orchestratorAgent }),
				{ idempotencyKey: creationKey.current },
			);
			setActiveProject(project.id);
			await refresh().catch(() => {});
			router.replace({ pathname: "/project/[id]", params: { id: project.id } });
		} catch (cause) {
			if (cause instanceof CloudApiError && (cause.code === "repository_unreachable" || cause.code === "read_only_token" || cause.code === "token_missing")) {
				setStep(cause.code === "token_missing" ? "github-token" : "repository");
			}
			setError(messageFor(cause, "Could not create the project."));
			setBusy(false);
		}
	};

	const back = () => {
		haptics.tap();
		if (step === "agents") { setError(null); setStep("repository"); }
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
				{step === "github-token" ? (
					<View style={styles.group}>
						<Text style={styles.title}>Connect GitHub</Text>
						<Text style={styles.description}>AO uses a GitHub personal access token to clone private repositories and push worker changes. Your token is saved securely in AO Cloud and never shown again.</Text>
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
						<Text style={styles.description}>Enter the same GitHub repository details used when creating a Cloud project on desktop.</Text>
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
						<Text style={styles.description}>Pick the agent that builds in workers and the agent that coordinates the project. You can change these later on desktop.</Text>
						{agents.length === 0 ? <Text style={styles.warning}>No Cloud agent credential is ready. Add a Claude Code, Codex, or Cursor credential in desktop Settings, then reopen this form.</Text> : (
							<>
								<Text style={styles.label}>Worker agent</Text>
								{agents.map((agent) => <AgentOption key={`worker-${agent}`} agent={agent} selected={workerAgent === agent} onPress={() => { setWorkerAgent(agent); creationKey.current = null; }} styles={styles} color={t.accent} />)}
								<Text style={styles.label}>Orchestrator agent</Text>
								{agents.map((agent) => <AgentOption key={`orchestrator-${agent}`} agent={agent} selected={orchestratorAgent === agent} onPress={() => { setOrchestratorAgent(agent); creationKey.current = null; }} styles={styles} color={t.accent} />)}
							</>
						)}
						<Button title="Create project" loading={busy} disabled={agents.length === 0} onPress={() => void createProject()} />
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
