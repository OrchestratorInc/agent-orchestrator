import { useFocusEffect, useRouter } from "expo-router";
import { useCallback, useEffect, useRef, useState, type ReactElement } from "react";
import { StyleSheet, View } from "react-native";
import { AgentLogo } from "../../../lib/AgentLogo";
import { agentConnection, agentConnectionState, CLOUD_AGENT_LABELS, CLOUD_AGENTS, credentialTypeLabel, type CloudAgent } from "../../../lib/cloud/agents";
import type { CloudClient } from "../../../lib/cloud/client";
import { CloudSignIn } from "../../../lib/cloud/components";
import { useCloud } from "../../../lib/cloud/CloudProvider";
import { CloudError, cloudErrorMessage } from "../../../lib/cloud/errors";
import { installGitHubApp } from "../../../lib/cloud/githubInstall";
import { cloudAgentsOffered, cloudPlanView, freePlanSummary } from "../../../lib/cloud/plan";
import { useCloudPlan, type CloudPlanState } from "../../../lib/cloud/planStore";
import type { CloudGitHubInstallation, CloudOffering, CloudProviderConnection } from "../../../lib/cloud/types";
import { haptics } from "../../../lib/haptics";
import { SettingsButtonRow, SettingsForm, SettingsMeterRow, SettingsSection, SettingsStatusRow, SettingsValueRow, type SettingsTone } from "../../../lib/SettingsForm";
import { Spinner } from "../../../lib/Spinner";
import { useTheme } from "../../../lib/ThemeProvider";
import { rowIconWidth } from "../../../lib/tokens";
import { Button, EmptyState } from "../../../lib/ui";
import { useGlassBarInset } from "../../../lib/useHeaderOverlap";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

/** An agent's logo stands in the glyph column, so its name lines up with the other rows' titles. */
const AGENT_LOGO_SIZE = rowIconWidth;

/** A coding agent's line under its name. */
function agentStatus(connections: readonly CloudProviderConnection[], loaded: boolean, failed: boolean, agent: CloudAgent): { label: string; tone: SettingsTone } {
	if (!loaded) return failed ? { label: "Couldn't check", tone: "bad" } : { label: "Checking…", tone: "default" };
	switch (agentConnectionState(connections, agent)) {
		case "connected": {
			const how = credentialTypeLabel(agentConnection(connections, agent));
			return { label: how ? `Connected · ${how}` : "Connected", tone: "good" };
		}
		case "invalid":
			return { label: "Sign in again", tone: "bad" };
		case "pending":
			return { label: "Checking…", tone: "default" };
		default:
			return { label: "Not connected", tone: "default" };
	}
}

/** The person's role in the organization, as a word: "owner" reads "Owner". */
function roleLabel(role: string | undefined): string | undefined {
	const trimmed = role?.trim();
	return trimmed ? trimmed[0].toUpperCase() + trimmed.slice(1).replace(/_/g, " ") : undefined;
}

/** An installation's line: what the app can reach, or why it can't. */
function installationStatus(installation: CloudGitHubInstallation): { value: string; tone: SettingsTone } {
	if (installation.status === "suspended") return { value: "Suspended on GitHub", tone: "bad" };
	if (installation.syncStatus === "failed") return { value: "Couldn't sync", tone: "bad" };
	return { value: installation.repositorySelection === "all" ? "All repositories" : "Selected repositories", tone: "default" };
}

type GitHubAccess =
	| { kind: "loading" }
	/** This deployment runs without the GitHub App. */
	| { kind: "unavailable" }
	| { kind: "failed"; message: string }
	| { kind: "ready"; installations: CloudGitHubInstallation[] };

/**
 * The organization's Orchestrator GitHub App installations, and the two things
 * done with them here: install it or add repositories on GitHub, and sync what
 * GitHub granted. The control plane owns the app; the phone only opens its
 * install page and waits for the installation to change.
 */
function useGitHubAccess(client: CloudClient, orgId: string | undefined) {
	const [access, setAccess] = useState<GitHubAccess>({ kind: "loading" });
	const [connecting, setConnecting] = useState(false);
	const [syncing, setSyncing] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; };
	}, []);
	// What the page shows belongs to one organization: another one starts it
	// over, and an answer for the one before is dropped.
	const shownOrg = useRef(orgId);
	const stillShown = (asked: string | undefined) => mounted.current && shownOrg.current === asked;
	useEffect(() => {
		if (shownOrg.current === orgId) return;
		shownOrg.current = orgId;
		setAccess({ kind: "loading" });
		setError(null);
	}, [orgId]);

	const load = useCallback(async () => {
		if (!orgId) return;
		try {
			const { installations } = await client.listGitHubInstallations(orgId);
			if (stillShown(orgId)) setAccess({ kind: "ready", installations });
		} catch (cause) {
			if (!stillShown(orgId)) return;
			// The GitHub App only exists on deployments that configure it.
			if (cause instanceof CloudError && cause.status === 404) setAccess({ kind: "unavailable" });
			// A failed refresh keeps what the page already shows.
			else setAccess((current) => (current.kind === "ready" ? current : { kind: "failed", message: cloudErrorMessage(cause) }));
		}
	}, [client, orgId]);

	const retry = useCallback(() => {
		setAccess({ kind: "loading" });
		void load();
	}, [load]);

	const installations = access.kind === "ready" ? access.installations : [];

	/** Open GitHub's install page, signed in, then wait for the new or updated installation. */
	const connect = async () => {
		if (!orgId || connecting) return;
		setConnecting(true);
		setError(null);
		try {
			await installGitHubApp(client, orgId, installations, () => stillShown(orgId));
			if (stillShown(orgId)) await load();
		} catch (cause) {
			if (!stillShown(orgId)) return;
			haptics.error();
			setError(cloudErrorMessage(cause));
		} finally {
			if (mounted.current) setConnecting(false);
		}
	};

	/** Ask the control plane to read the installations' repositories from GitHub again. */
	const sync = async () => {
		if (!orgId || syncing) return;
		setSyncing(true);
		setError(null);
		try {
			await Promise.all(installations.filter((item) => item.status === "active").map((item) => client.syncGitHubInstallation(orgId, item.id)));
			if (!stillShown(orgId)) return;
			await load();
			haptics.success();
		} catch (cause) {
			if (!stillShown(orgId)) return;
			haptics.error();
			setError(cloudErrorMessage(cause));
		} finally {
			if (mounted.current) setSyncing(false);
		}
	};

	return { access, connecting, syncing, error, load, retry, connect, sync };
}

/**
 * Orchestrator Cloud's page, opened from its row in Settings' machine list:
 * the organization it works in, the coding agents it can run with the
 * person's own credentials, and the repositories it can reach on GitHub.
 * Signing in and out stays in Settings' Account section.
 */
export default function CloudSettingsScreen() {
	const t = useTheme();
	const router = useRouter();
	const glassInset = useGlassBarInset();
	const { status, org, error, client, connections, connectionsLoaded, refresh, refreshConnections } = useCloud();
	const github = useGitHubAccess(client, org?.id);
	const plan = useCloudPlan(client, org?.id);
	const [retrying, setRetrying] = useState(false);
	const { load: loadGitHub } = github;
	const reloadPlan = plan?.reload;

	// Read again on every return, so an agent connected on its page, or on
	// another device, shows at once; the plan's usage moves while sessions run.
	useFocusEffect(useCallback(() => {
		void refreshConnections();
		void loadGitHub();
		void reloadPlan?.();
	}, [loadGitHub, refreshConnections, reloadPlan]));

	if (status === "signed-out") {
		return <View style={styles.fill}><CloudSignIn /></View>;
	}
	if (status === "error") {
		return (
			<View style={[styles.fill, styles.center, { backgroundColor: t.bgBase, paddingTop: glassInset }]}>
				<EmptyState
					icon="error"
					title="Can't reach Orchestrator Cloud"
					message={error ?? "Try again in a moment."}
					action={<Button title="Retry" icon="refresh" loading={retrying} onPress={() => { setRetrying(true); void refresh().finally(() => setRetrying(false)); }} />}
				/>
			</View>
		);
	}
	if (status !== "ready" || !org) {
		return (
			<View style={[styles.fill, styles.center, { backgroundColor: t.bgBase }]}>
				<Spinner size="large" accessibilityLabel="Loading Orchestrator Cloud" />
			</View>
		);
	}

	const role = roleLabel(org.role);
	const agentsOffered = cloudAgentsOffered(plan, org.offering);
	return (
		<View style={[styles.fill, { backgroundColor: t.bgBase }]}>
			<SettingsForm testID="cloud-settings-form">
				<SettingsSection title="Organization">
					<SettingsValueRow icon="organization" label={org.displayName} value={role} testID="cloud-settings-organization" />
				</SettingsSection>

				{planSection(plan, org.offering)}

				{/* Agents' sign-ins, GitHub and new projects only matter where the plan runs cloud agents. */}
				{agentsOffered ? (
					<SettingsSection title="Coding agents" footer={!connectionsLoaded && error ? error : undefined}>
						{CLOUD_AGENTS.map((agent) => {
							const line = agentStatus(connections, connectionsLoaded, Boolean(error), agent);
							return (
								<SettingsStatusRow
									key={agent}
									icon="terminal"
									leading={<AgentLogo harness={agent} size={AGENT_LOGO_SIZE} />}
									label={CLOUD_AGENT_LABELS[agent]}
									status={line.label}
									tone={line.tone}
									onPress={() => router.push({ pathname: "/settings/cloud/agent/[agent]", params: { agent } })}
									testID={`cloud-settings-agent-${agent}`}
								/>
							);
						})}
					</SettingsSection>
				) : null}

				{agentsOffered ? githubSection(github) : null}

				{agentsOffered ? (
					<SettingsSection>
						<SettingsButtonRow icon="add" label="New cloud project" onPress={() => router.push("/cloud/new-project")} testID="cloud-settings-new-project" />
					</SettingsSection>
				) : null}
			</SettingsForm>
		</View>
	);
}

/**
 * The organization's plan and its usage, as the desktop's Plans page shows
 * them; nothing where plans don't apply (no Stripe, or nothing billed).
 * Placed as the section itself, as GitHub's is, so the form never holds an
 * empty slot.
 */
function planSection(plan: CloudPlanState | undefined, offering: CloudOffering | undefined): ReactElement | null {
	if (!plan) return null;
	if (plan.status === "loading") {
		return (
			<SettingsSection title="Plan">
				<SettingsValueRow icon="plan" label="Plan" loading testID="cloud-settings-plan-loading" />
			</SettingsSection>
		);
	}
	if (plan.status === "failed" || !plan.summary) {
		return (
			<SettingsSection title="Plan" footer={cloudErrorMessage(plan.error)}>
				<SettingsButtonRow icon="refresh" label="Check the plan again" onPress={() => void plan.reload()} testID="cloud-settings-plan-retry" />
			</SettingsSection>
		);
	}
	const view = cloudPlanView(plan.summary);
	if (view.kind === "none") return null;
	if (view.kind === "free") {
		return (
			<SettingsSection title="Plan">
				<SettingsValueRow icon="plan" label="Free" value={freePlanSummary(offering)} testID="cloud-settings-plan" />
			</SettingsSection>
		);
	}
	return (
		<SettingsSection title="Plan">
			{/* A meter row without a bar, so every row of the card shares one layout. */}
			<SettingsMeterRow icon="plan" label={view.name} value={view.status ?? ""} tone={view.tone === "bad" ? "bad" : undefined} testID="cloud-settings-plan" />
			{view.meters.map((meter) => (
				<SettingsMeterRow
					key={meter.key}
					icon={meter.key === "window" ? "usageWindow" : "usageWeek"}
					label={meter.label}
					value={meter.value}
					fraction={meter.fraction}
					severity={meter.severity}
					detail={meter.detail}
					testID={`cloud-settings-plan-${meter.key}`}
				/>
			))}
			{view.running ? (
				<SettingsMeterRow
					icon="runningSessions"
					label="Running sessions"
					value={view.running.value}
					tone={view.running.full ? "bad" : undefined}
					testID="cloud-settings-plan-running"
				/>
			) : null}
			{view.total ? (
				<SettingsMeterRow
					icon="totalSessions"
					label="Total sessions"
					value={view.total.value}
					tone={view.total.full ? "bad" : undefined}
					testID="cloud-settings-plan-total"
				/>
			) : null}
		</SettingsSection>
	);
}

/**
 * GitHub access, or nothing on a deployment without the GitHub App. Placed as
 * the section itself rather than a component that may draw nothing, so the
 * form never holds an empty slot.
 */
function githubSection({ access, connecting, syncing, error, retry, connect, sync }: ReturnType<typeof useGitHubAccess>): ReactElement | null {
	if (access.kind === "unavailable") return null;
	if (access.kind === "loading") {
		return (
			<SettingsSection title="GitHub">
				<SettingsValueRow icon="github" label="GitHub App" loading testID="cloud-settings-github-loading" />
			</SettingsSection>
		);
	}
	if (access.kind === "failed") {
		return (
			<SettingsSection title="GitHub" footer={access.message}>
				<SettingsButtonRow icon="refresh" label="Check GitHub access again" onPress={retry} testID="cloud-settings-github-retry" />
			</SettingsSection>
		);
	}
	// A removed installation is gone from GitHub; nothing to say about it.
	const listed = access.installations.filter((item) => item.status !== "removed");
	const installed = listed.some((item) => item.status === "active");
	return (
		<SettingsSection title="GitHub" footer={error ?? undefined}>
			{listed.map((installation) => {
				const line = installationStatus(installation);
				return (
					<SettingsValueRow
						key={installation.id}
						icon="github"
						label={installation.accountLogin}
						value={line.value}
						tone={line.tone}
						testID={`cloud-settings-github-${installation.id}`}
					/>
				);
			})}
			<SettingsButtonRow
				icon={installed ? "openExternal" : "github"}
				label={connecting ? "Waiting for GitHub…" : installed ? "Add repositories on GitHub" : "Connect GitHub"}
				loading={connecting}
				disabled={syncing}
				onPress={() => void connect()}
				testID="cloud-settings-github-connect"
			/>
			{installed ? (
				<SettingsButtonRow
					icon="refresh"
					label="Refresh repositories"
					loading={syncing}
					disabled={connecting}
					onPress={() => void sync()}
					testID="cloud-settings-github-sync"
				/>
			) : null}
		</SettingsSection>
	);
}

const styles = StyleSheet.create({
	fill: { flex: 1 },
	center: { alignItems: "center", justifyContent: "center" },
});
