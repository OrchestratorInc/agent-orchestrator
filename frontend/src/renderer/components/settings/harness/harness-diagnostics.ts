import type { QueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import type { InstallJob } from "../../../hooks/useHarnessUpdates";
import { fetchSessionMemory, formatCPU, formatMemory, sessionMemoryQueryOptions } from "../../../hooks/useSessionMemory";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import type { clientForSessionHost } from "../../../lib/host-clients";
import type { HarnessDetails } from "./describe-harness";

// The copied reports stay in English: they are read by whoever fixes the bug.

export function diagnosticsText(agentId: AgentId, job: InstallJob): string {
	return [
		`${agentLabel(agentId)} installation diagnostics`,
		job.method ? `Method: ${job.method}` : "",
		job.expectedDestination ? `Expected destination: ${job.expectedDestination}` : "",
		job.error ? `Error: ${job.error}` : "",
		job.output ? `Output:\n${job.output}` : "",
	].filter(Boolean).join("\n");
}

/**
	* What the machine looked like when the diagnostics were copied: an install
	* that dies on a host with no memory left reads very differently from one
	* that dies on an idle laptop. Fetched once on the click rather than polled,
	* and left out entirely where the daemon cannot measure the host.
	*/
export async function machineText(queryClient: QueryClient, remoteClient?: ReturnType<typeof clientForSessionHost>): Promise<string> {
	let reading: Pick<Awaited<ReturnType<typeof fetchSessionMemory>>, "app" | "system" | "sessions">;
	try {
		reading = remoteClient ? await fetchRemoteMachine(remoteClient) : await queryClient.fetchQuery(sessionMemoryQueryOptions());
	} catch {
		return "";
	}
	const { app, system, sessions } = reading;
	if (!app && !system) return "";
	const lines = ["Machine"];
	if (app && system) {
		lines.push(`Memory: AO ${formatMemory(app.rssBytes)} · available ${formatMemory(system.availableBytes)} of ${formatMemory(system.totalBytes)}`);
	} else if (app) {
		lines.push(`Memory: AO ${formatMemory(app.rssBytes)}`);
	}
	if (system) {
		// Windows has no load average and reports the sentinel -1 rather than a
		// fabricated 0; leave the figure out of the report entirely there.
		const load = system.load1 >= 0 ? ` · load ${system.load1.toFixed(2)}` : "";
		lines.push(`CPU: ${formatCPU(system.cpuPercent)} of ${system.cpuCount} cores${load}`);
		if (system.swapBytesPerSec > 0) lines.push(`Swapping: ${formatMemory(system.swapBytesPerSec)}/s`);
	}
	if (sessions.length > 0) {
		lines.push(`Live sessions: ${sessions.length} · ${formatMemory(sessions.reduce((sum, s) => sum + s.rssBytes, 0))}`);
	}
	return lines.join("\n");
}

/** A remote install's diagnostics describe that host, so its numbers come
	* from its own daemon. Fetched directly: the shared query and the CPU graph
	* hold this computer's readings only. */
async function fetchRemoteMachine(client: ReturnType<typeof clientForSessionHost>) {
	const { data, error } = await client.GET("/api/v1/usage/sessions/memory", { params: { query: {} } });
	if (error) throw error;
	return { sessions: data?.sessions ?? [], system: data?.system, app: data?.app };
}

/** Where the harness's credential was found, for display: never a CLI
	* executable or a missing file that was merely checked. Current probe
	* evidence precedes cached identity. */
export function detectedCredential(details: HarnessDetails, t: TFunction): string | undefined {
	const auth = details.readinessAgent?.authentication;
	const source = auth?.diagnostics?.find((step) => step.source && step.method === "provider_validation" && step.code !== "credential_missing")?.source
		|| auth?.diagnostics?.find((step) => step.source && step.method === "selected_credential" && step.code === "credential_present")?.source;
	if (source) return source;
	const account = auth?.account;
	if (account?.sourceKind === "keychain") return t("settings.harness.health.keychain");
	if (account?.sourceKind === "file" || account?.sourceKind === "environment") return account.source || undefined;
	return account?.method === "api_key" ? t("settings.harness.usingApiKey")
		: account?.method === "token" ? t("settings.harness.usingToken")
		: account?.method === "account" ? t("settings.harness.signedInWithAccount") : undefined;
}

export function healthText(details: HarnessDetails, { hostId, authCheckError, t }: { hostId?: string; authCheckError?: string; t: TFunction }): string {
	const { agentId, readinessAgent, advisory } = details;
	const credential = detectedCredential(details, t);
	return [
		`${agentLabel(agentId)} health`,
		`Host: ${hostId ?? "local"}`,
		`Installation: ${readinessAgent?.installation.state ?? "unknown"}${readinessAgent?.installation.checkedAt ? ` (checked ${readinessAgent.installation.checkedAt})` : ""}`,
		readinessAgent?.installation.reason ? `Installation reason: ${readinessAgent.installation.reason}` : "",
		`Authentication: ${readinessAgent?.authentication.state ?? "unknown"}`,
		credential ? `Detected credential: ${credential}` : "",
		t("settings.harness.health.authContext"),
		readinessAgent?.authentication.reason ? `Authentication reason: ${readinessAgent.authentication.reason}` : "",
		readinessAgent?.authentication.reasonCode ? `Authentication reason code: ${readinessAgent.authentication.reasonCode}` : "",
		readinessAgent?.authentication.freshness ? `Authentication freshness: ${readinessAgent.authentication.freshness}` : "",
		readinessAgent?.authentication.checkedAt ? `Authentication checked: ${readinessAgent.authentication.checkedAt}` : "",
		readinessAgent?.authentication.attemptedAt ? `Authentication attempted: ${readinessAgent.authentication.attemptedAt}` : "",
		authCheckError ? `Authentication request error: ${authCheckError}` : "",
		...(readinessAgent?.authentication.diagnostics ?? []).map((step) => [
			`Auth check: ${step.method} (${step.code})`,
			`Observed: ${step.checkedAt} · ${step.cached ? "cached" : "fresh"} · ${step.durationMs} ms`,
			step.provider ? `Provider: ${step.provider}` : "",
			step.source ? `Source: ${step.source}` : "",
			step.httpStatus ? `HTTP status: ${step.httpStatus}` : "",
			step.exitCode !== undefined ? `Exit code: ${step.exitCode}` : "",
			step.reason,
			step.nextStep ? `Next step: ${step.nextStep}` : "",
		].filter(Boolean).join("\n")),
		`Installed version: ${details.currentVersion ?? "unknown"}`,
		`Latest version: ${advisory?.latestVersion ?? "unknown"}`,
		advisory?.checkedAt ? `Update checked: ${advisory.checkedAt}` : "",
		advisory?.status ? `Update status: ${advisory.status}${advisory.reason ? ` (${advisory.reason})` : ""}` : "",
		`Maintenance method: ${details.methodId || "unverified"}`,
		details.advisory?.binaryPath ? `Executable: ${details.advisory.binaryPath}` : "",
	].filter(Boolean).join("\n");
}
