import { useEffect, useMemo, useRef, useState } from "react";
import { X } from "lucide-react";
import { Trans, useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { AgentAvatar } from "./AgentAvatar";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
} from "./ui/dialog";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import type { CloudCpAgentProvider } from "../lib/cloud-cp";
import {
	centeredOnboardingDialogClass,
	onboardingFieldErrorClass,
	onboardingFieldHintClass,
	onboardingFooterActionsEndClass,
	onboardingFormLabelClass,
} from "../lib/onboarding-ui";
import { useCloudCp } from "../hooks/useCloudCp";
import { providerConnectionsQueryKey } from "../hooks/useProviderConnections";
import { useCredentialDialogStore } from "../stores/credential-dialog-store";
import { cn } from "../lib/utils";
import { aoBridge } from "../lib/bridge";

const BROWSER_LOGIN = "browser_login";
const SETUP_TOKEN_STEPS = [
	"cloudCredential.setupTokenStep1",
	"cloudCredential.setupTokenStep2",
	"cloudCredential.setupTokenStep3",
] as const;

// The cloud harnesses and the ways each can log in. Browser login is the
// default where the harness has one: the desktop app runs the agent CLI's own
// login locally and securely sends the resulting credential to the control
// plane, so no token is pasted or displayed. Pasting a Claude setup token or a
// provider API key remains available as a fallback.
const AGENTS = [
	{
		agent: "claude-code",
		label: "Claude Code",
		creds: [
			{ value: BROWSER_LOGIN, label: "Log in with Anthropic" },
			{ value: "oauth_token", label: "Setup token" },
			{ value: "api_key", label: "API key" },
		],
	},
	{
		agent: "codex",
		label: "Codex",
		creds: [
			{ value: BROWSER_LOGIN, label: "Log in with ChatGPT" },
			{ value: "api_key", label: "API key" },
		],
	},
	{
		agent: "cursor",
		label: "Cursor",
		creds: [{ value: "api_key", label: "API key" }],
	},
] as const;

type Phase = "idle" | "submitting" | "success";

// Logs one harness in for cloud sessions. Opened from that harness's row on the
// Harnesses settings page, the only place cloud agent logins are made. Every
// credential is personal (PUT /me/providers/{agent}): it runs the caller's
// cloud sessions in every org they belong to.
export function CloudCredentialDialog() {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const open = useCredentialDialogStore((s) => s.open);
	const closeDialog = useCredentialDialogStore((s) => s.closeDialog);
	const targetAgent = useCredentialDialogStore((s) => s.targetAgent);

	const selectedAgent = AGENTS.find((entry) => entry.agent === targetAgent) ?? AGENTS[0];
	const agent: CloudCpAgentProvider = selectedAgent.agent;
	const creds = selectedAgent.creds;
	const [credentialType, setCredentialType] = useState<string>(creds[0].value);
	const [secret, setSecret] = useState("");
	const [phase, setPhase] = useState<Phase>("idle");
	const [error, setError] = useState<string | null>(null);
	// Set when the user cancels the browser login, so the resulting rejection is
	// treated as a cancellation rather than shown as an error.
	const loginCancelledRef = useRef(false);

	const credentialOptions = useMemo(
		() => creds.map((entry) => ({ value: entry.value, label: entry.label })),
		[creds],
	);
	const selectedCredential = creds.find((entry) => entry.value === credentialType) ?? creds[0];
	const needsSecret = credentialType !== BROWSER_LOGIN;

	// Reset the whole form each time the dialog opens so a reopen never shows a
	// stale secret or a previous error/success.
	useEffect(() => {
		if (!open) return;
		setCredentialType(selectedAgent.creds[0].value);
		setSecret("");
		setPhase("idle");
		setError(null);
	}, [open, selectedAgent]);

	const canSubmit = phase !== "submitting" && needsSecret && secret.trim() !== "";
	const busy = phase === "submitting";
	const browserLoginPending = busy && !needsSecret;

	const cancelBrowserLogin = () => {
		loginCancelledRef.current = true;
		void aoBridge.cloud.cancelProviderAuth();
	};

	// Closing the dialog mid-login (X, Escape) abandons the login rather than
	// leaving the agent CLI waiting in the background.
	const onOpenChange = (next: boolean) => {
		if (next) return;
		if (browserLoginPending) cancelBrowserLogin();
		closeDialog();
	};

	const submit = async () => {
		if (!canSubmit) return;
		setPhase("submitting");
		setError(null);
		try {
			const { providerConnection } = await client.putUserAgentConnection(agent, {
				credentialType,
				secret: secret.trim(),
			});
			if (providerConnection.validationState !== "valid") {
				setPhase("idle");
				setError(t("cloudCredential.invalid", { state: providerConnection.validationState }));
				return;
			}
			await queryClient.invalidateQueries({ queryKey: providerConnectionsQueryKey });
			setPhase("success");
			setSecret("");
		} catch (err) {
			setPhase("idle");
			setError(err instanceof Error ? err.message : t("cloudCredential.failed"));
		}
	};

	const loginWithBrowser = async () => {
		if (phase === "submitting") return;
		setPhase("submitting");
		setError(null);
		loginCancelledRef.current = false;
		try {
			// One login for local and cloud: the credential becomes the caller's
			// personal cloud connection and, for Claude Code, is also persisted
			// locally so local sessions use the same login.
			await aoBridge.cloud.connectProviderAuth({
				baseUrl,
				provider: agent,
				persistLocalClaudeToken: agent === "claude-code",
			});
			await queryClient.invalidateQueries({ queryKey: providerConnectionsQueryKey });
			setPhase("success");
		} catch (err) {
			setPhase("idle");
			if (loginCancelledRef.current) return;
			setError(err instanceof Error ? err.message : t("cloudCredential.failed"));
		}
	};

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className={centeredOnboardingDialogClass} showCloseButton={false}>
				<DialogClose asChild>
					<button
						type="button"
						className="settings-dialog-close-button settings-close-button"
						aria-label={t("common.close")}
						disabled={busy && !browserLoginPending}
					>
						<X className="size-icon-base" aria-hidden="true" />
					</button>
				</DialogClose>

				<DialogTitle className="px-4 pr-12 pt-3 text-balance text-[18px] font-semibold text-[var(--color-text-import-title)]">
					<span className="inline-flex items-center gap-2">
						<AgentAvatar provider={agent} className="size-5" decorative />
						{t("cloudCredential.title", { agent: selectedAgent.label })}
					</span>
				</DialogTitle>
				<DialogDescription className="px-4 pr-12 pt-1 text-pretty text-[13px] leading-5 text-muted-foreground">
					{t("cloudCredential.description", { agent: selectedAgent.label })}
				</DialogDescription>

				{phase === "success" ? (
					<div className="min-h-0 overflow-y-auto px-4 pb-1 pt-4">
						<p role="status" className="text-control leading-4 text-success">
							{t("cloudCredential.connected")}
						</p>
					</div>
				) : (
					<div className="flex min-h-0 flex-col gap-4 overflow-y-auto px-4 pb-1 pt-4">
						{creds.length > 1 ? (
						<div className="space-y-2">
							<Label htmlFor="cloud-cred-type" className={onboardingFormLabelClass}>
								{t("cloudCredential.typeLabel")}
							</Label>
							<SettingsOptionMenu
								aria-label={t("cloudCredential.typeLabel")}
								value={credentialType}
								options={credentialOptions}
								disabled={busy}
								menuAlign="start"
								onChange={(val) => {
									setCredentialType(val);
									setSecret("");
									setError(null);
								}}
								triggerClassName="composer-chip composer-toolbar-option h-control-form w-full justify-between"
								renderTrigger={() => (
									<span className="min-w-0 truncate text-control text-foreground" title={selectedCredential.label}>
										{selectedCredential.label}
									</span>
								)}
							/>
						</div>
						) : null}

						{agent === "claude-code" && credentialType === "oauth_token" ? (
							<div className={onboardingFieldHintClass}>
								<p>{t("cloudCredential.setupTokenIntro")}</p>
								<ol className="mt-1 list-decimal space-y-0.5 pl-5">
									{SETUP_TOKEN_STEPS.map((key) => (
										<li key={key}>
											<Trans
												i18nKey={key}
												values={{ command: "claude setup-token", prefix: "sk-ant-oat" }}
												components={{ code: <code className="rounded bg-muted px-1 py-px font-mono text-[11px] text-foreground" /> }}
											/>
										</li>
									))}
								</ol>
							</div>
						) : null}

						{needsSecret ? (
							<div className="space-y-2">
							<Label htmlFor="cloud-cred-secret" className={onboardingFormLabelClass}>
								{selectedCredential.label}
							</Label>
							<Input
								id="cloud-cred-secret"
								type="password"
								autoComplete="off"
								spellCheck={false}
								className="text-[13px]"
								placeholder={t("cloudCredential.tokenPlaceholder")}
								disabled={busy}
								value={secret}
								onChange={(e) => setSecret(e.target.value)}
								onKeyDown={(e) => {
									if (e.key === "Enter") void submit();
								}}
							/>
							<p className={onboardingFieldHintClass}>{t("cloudCredential.tokenHint")}</p>
						</div>
						) : (
							<p className={onboardingFieldHintClass}>
								{agent === "claude-code"
									? t("cloudCredential.anthropicLoginDescription")
									: t("cloudCredential.chatgptLoginDescription")}
							</p>
						)}

						{error ? (
							<p role="alert" className={onboardingFieldErrorClass}>
								{error}
							</p>
						) : null}
					</div>
				)}

				<div className={cn(onboardingFooterActionsEndClass, "px-4 pb-4")}>
					{browserLoginPending ? (
						<Button type="button" variant="outline" onClick={cancelBrowserLogin}>
							{t("cloudCredential.cancel")}
						</Button>
					) : (
						<DialogClose asChild>
							<Button type="button" variant="outline" disabled={busy}>
								{phase === "success" ? t("cloudCredential.done") : t("cloudCredential.cancel")}
							</Button>
						</DialogClose>
					)}
					{phase !== "success" && needsSecret ? (
						<Button type="button" variant="primary" disabled={!canSubmit} onClick={() => void submit()}>
							{phase === "submitting" ? t("cloudCredential.connecting") : t("cloudCredential.connect")}
						</Button>
					) : null}
					{phase !== "success" && !needsSecret ? (
						<Button type="button" variant="primary" disabled={busy} onClick={() => void loginWithBrowser()}>
							{phase === "submitting" ? t("cloudCredential.connecting") : (agent === "claude-code" ? t("cloudCredential.loginWithAnthropic") : t("cloudCredential.loginWithChatGPT"))}
						</Button>
					) : null}
				</div>
			</DialogContent>
		</Dialog>
	);
}
