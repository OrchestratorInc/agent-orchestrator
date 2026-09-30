import { ChevronDown, KeyRound } from "lucide-react";
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { AgentAvatar } from "../AgentAvatar";
import { GitHubTokenField } from "../onboarding/GitHubTokenField";
import { Button } from "../ui/button";
import { useCloudGate } from "../../hooks/useCloudGate";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudOrg } from "../../hooks/useCloudOrg";
import { CLOUD_AGENT_PROVIDERS } from "../../lib/cloud-agents";
import { useProviderConnections } from "../../hooks/useProviderConnections";
import { useCloudSession } from "../../lib/cloud-session";
import { useCredentialDialogStore } from "../../stores/credential-dialog-store";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

// Proper nouns; deliberately not translated.
const AGENT_LABELS: Record<string, string> = {
	"claude-code": "Claude Code",
	codex: "Codex",
	cursor: "Cursor",
	opencode: "OpenCode",
};

/**
 * Cloud coding-agent credentials in global settings. The outer component only
 * reads the daemon settings gate (a query the settings page already runs), so
 * a local-only app renders nothing and never mounts the cloud hooks — the
 * inner component is what subscribes to the cloud session/org/connection
 * queries. The connect flow reuses the globally mounted CloudCredentialDialog
 * via its shared open-store.
 */
export function CloudCredentialsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { cloudEnabled } = useCloudGate();
	if (!cloudEnabled) return null;
	return <CloudCredentialsSectionInner titleHidden={titleHidden} />;
}

function CloudCredentialsSectionInner({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const { status } = useCloudSession();
	const { org, error: orgError } = useCloudOrg();
	const { client } = useCloudCp();
	const queryClient = useQueryClient();
	const connections = useProviderConnections(org?.id);
	const userConnections = useQuery({
		queryKey: ["cloud-user-provider-connections"],
		enabled: status === "authenticated",
		queryFn: async () => (await client.listUserProviderConnections()).providerConnections,
	});
	const openCredentialDialog = useCredentialDialogStore((s) => s.openDialog);
	const [repositoryAccessOpen, setRepositoryAccessOpen] = useState(false);
	const [githubPAT, setGitHubPAT] = useState("");
	const [githubPATBusy, setGitHubPATBusy] = useState(false);
	const [githubPATError, setGitHubPATError] = useState<string | null>(null);

	useEffect(() => {
		if (status === "authenticated") return;
		setGitHubPAT("");
		setGitHubPATError(null);
		setRepositoryAccessOpen(false);
	}, [status]);

	// Managing credentials needs the signed-in org. The Cloud settings page is
	// reachable while signed out, so say why it is empty instead of rendering a
	// blank pane.
	if (status !== "authenticated") {
		return (
			<SettingsSection title={t("settings.cloudAgents")} sectionId="cloud-agents" titleHidden={titleHidden}>
				<p className="px-3 text-xs leading-relaxed text-muted-foreground">{t("settings.cloudAgents.signIn")}</p>
			</SettingsSection>
		);
	}

	const rows = (connections.data ?? []).filter((connection) => connection.provider !== "github");
	const connectionsError = orgError || connections.isError;
	const connectionsLoading = !connectionsError && (org === undefined || connections.isPending);
	const githubPATConnected = (userConnections.data ?? []).some(
		(connection) => connection.provider === "github" && connection.label === "default" && connection.validationState === "valid",
	);
	const saveGitHubPAT = async () => {
		if (githubPAT.trim() === "") return;
		setGitHubPATBusy(true);
		setGitHubPATError(null);
		try {
			await client.putGitHubPAT({ secret: githubPAT.trim() });
			setGitHubPAT("");
			await queryClient.invalidateQueries({ queryKey: ["cloud-user-provider-connections"] });
		} catch (error) {
			setGitHubPATError(error instanceof Error ? error.message : t("settings.cloudAgents.github.errorSave"));
		} finally {
			setGitHubPATBusy(false);
		}
	};
	const removeGitHubPAT = async () => {
		setGitHubPATBusy(true);
		setGitHubPATError(null);
		try {
			await client.deleteGitHubPAT();
			await queryClient.invalidateQueries({ queryKey: ["cloud-user-provider-connections"] });
		} catch (error) {
			setGitHubPATError(error instanceof Error ? error.message : t("settings.cloudAgents.github.errorRemove"));
		} finally {
			setGitHubPATBusy(false);
		}
	};
	return (
		<SettingsSection title={t("settings.cloudAgents")} sectionId="cloud-agents" titleHidden={titleHidden}>
			<div className="flex w-full flex-col gap-1.5">
				<div className="flex items-center justify-between gap-4 px-3 pb-2">
					<p className="text-xs leading-relaxed text-muted-foreground">{t("settings.cloudAgents.description")}</p>
					<Button type="button" variant="footer" disabled={!org} onClick={() => openCredentialDialog()}>
						{t("settings.cloudAgents.connect")}
					</Button>
				</div>
				{connectionsLoading ? <p role="status" className="px-3 text-xs text-muted-foreground">{t("settings.cloudAgents.loading")}</p> : null}
				{connectionsError ? <p role="alert" className="px-3 text-xs text-error">{t("settings.cloudAgents.loadError")}</p> : null}
				{rows.map((connection) => {
					const agentName = AGENT_LABELS[connection.provider] ?? connection.provider;
					const credentialType = connection.config.credentialType;
					const methods: Record<string, string> = {
						oauth_token: t("settings.cloudAgents.method.anthropicToken"),
						auth_json: connection.provider === "codex" ? t("settings.cloudAgents.method.chatgpt") : t("settings.cloudAgents.method.account"),
						access_token: t("settings.cloudAgents.method.account"),
						api_key: t("settings.cloudAgents.method.apiKey"),
						opencode_api_key: t("settings.cloudAgents.method.opencodeKey"),
						anthropic_api_key: t("settings.cloudAgents.method.anthropicKey"),
						openai_api_key: t("settings.cloudAgents.method.openaiKey"),
						openrouter_api_key: t("settings.cloudAgents.method.openrouterKey"),
					};
					const method = typeof credentialType === "string" ? methods[credentialType] : undefined;
					return (
						<div key={connection.id} className="settings-row-bar h-auto min-h-14 flex-wrap gap-3 py-2">
							<AgentAvatar provider={connection.provider} className="size-6 shrink-0" decorative />
							<div className="min-w-0 flex-1">
								<p className="text-sm text-settings-label">{agentName}</p>
								{method ? <p className="text-xs text-settings-muted">{method}</p> : null}
							</div>
							<span className={connection.validationState === "valid" ? "text-xs text-success" : "text-xs text-settings-muted"}>
								{connection.validationState === "valid" ? t("settings.cloudAgents.valid")
									: connection.validationState === "pending" ? t("settings.cloudAgents.checking")
										: t("settings.cloudAgents.needsAttention")}
							</span>
							{CLOUD_AGENT_PROVIDERS.some((provider) => provider === connection.provider) ? (
								<Button type="button" variant="footer" aria-label={t("settings.cloudAgents.manageAgent", { agent: agentName })} onClick={() => openCredentialDialog(connection.provider, typeof credentialType === "string" ? credentialType : undefined)}>
									{t("settings.cloudAgents.manage")}
								</Button>
							) : null}
						</div>
					);
				})}
				{connections.isSuccess && rows.length === 0 ? (
					<p className="px-3 text-xs leading-relaxed text-muted-foreground">{t("settings.cloudAgents.empty")}</p>
				) : null}
				<div className="mt-3 border-t border-border px-3 pt-3">
					<button
						type="button"
						className="flex w-full items-center justify-between gap-3 rounded py-1 text-left text-sm text-settings-label focus-visible:outline focus-visible:outline-2 focus-visible:outline-ring"
						aria-expanded={repositoryAccessOpen}
						aria-controls={repositoryAccessOpen ? "cloud-repository-access" : undefined}
						onClick={() => {
							if (repositoryAccessOpen) {
								setGitHubPAT("");
								setGitHubPATError(null);
							}
							setRepositoryAccessOpen(!repositoryAccessOpen);
						}}
					>
						{t("settings.cloudAgents.github.advanced")}
						<ChevronDown className={`size-4 shrink-0 transition-transform ${repositoryAccessOpen ? "rotate-180" : ""}`} aria-hidden="true" />
					</button>
					{repositoryAccessOpen ? (
						<div id="cloud-repository-access" className="pt-3">
							<p className="mb-2 text-xs leading-relaxed text-muted-foreground">{t("settings.cloudAgents.github.description")}</p>
							<SettingsRow icon={KeyRound} label={t("settings.cloudAgents.github.title")}>
								<span className="text-xs text-settings-muted">{userConnections.isPending ? t("settings.cloudAgents.loading") : userConnections.isError ? t("settings.cloudAgents.github.loadError") : githubPATConnected ? t("settings.cloudAgents.github.connected") : t("settings.cloudAgents.github.notConnected")}</span>
							</SettingsRow>
							<GitHubTokenField
								id="settings-github-pat"
								bare
								className="mt-2"
								label={t("settings.cloudAgents.github.tokenLabel")}
								hint={t("settings.cloudAgents.github.tokenHint")}
								value={githubPAT}
								disabled={githubPATBusy}
								error={githubPATError}
								submitLabel={githubPATBusy ? t("settings.cloudAgents.github.saving") : t("settings.cloudAgents.github.save")}
								submitVariant="outline"
								submitDisabled={githubPATBusy}
								onChange={setGitHubPAT}
								onSubmit={() => void saveGitHubPAT()}
							/>
							{githubPATConnected ? (
								<div className="mt-2 flex justify-end">
									<Button type="button" variant="footer" disabled={githubPATBusy} onClick={() => void removeGitHubPAT()}>
										{t("settings.cloudAgents.github.remove")}
									</Button>
								</div>
							) : null}
						</div>
					) : null}
				</div>
			</div>
		</SettingsSection>
	);
}
