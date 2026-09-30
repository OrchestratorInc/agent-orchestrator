import { useEffect, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import type { CLOUD_AGENT_PROVIDERS } from "../../lib/cloud-agents";
import { cn } from "../../lib/utils";
import { useCloudCp } from "../../hooks/useCloudCp";
import { providerConnectionsQueryKey } from "../../hooks/useProviderConnections";
import { aoBridge } from "../../lib/bridge";

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
export type CloudHarness = (typeof CLOUD_AGENT_PROVIDERS)[number];

const CLOUD_LOGIN_METHODS: Record<CloudHarness, ReadonlyArray<{ value: string; label: string }>> = {
	"claude-code": [
		{ value: BROWSER_LOGIN, label: "Log in with Anthropic" },
		{ value: "oauth_token", label: "Setup token" },
		{ value: "api_key", label: "API key" },
	],
	codex: [
		{ value: BROWSER_LOGIN, label: "Log in with ChatGPT" },
		{ value: "api_key", label: "API key" },
	],
	cursor: [{ value: "api_key", label: "API key" }],
};

// Logs one harness in for cloud sessions, expanded inline under its row on the
// Harness settings page (the only place cloud agent logins are made). Every
// credential is personal (PUT /me/providers/{agent}): it runs the caller's
// cloud sessions in every org they belong to.
export function CloudHarnessLoginPanel({ agent, onClose }: { agent: CloudHarness; onClose: () => void }) {
	const { t } = useTranslation();
	const { client, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const panelRef = useRef<HTMLDivElement>(null);
	const methods = CLOUD_LOGIN_METHODS[agent];
	const [credentialType, setCredentialType] = useState<string>(methods[0].value);
	const [secret, setSecret] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	// Set when the user cancels the browser login, so the resulting rejection is
	// treated as a cancellation rather than shown as an error.
	const loginCancelledRef = useRef(false);

	const selectedMethod = methods.find((entry) => entry.value === credentialType) ?? methods[0];
	const needsSecret = credentialType !== BROWSER_LOGIN;
	const browserLoginPending = busy && !needsSecret;
	const canSubmit = !busy && needsSecret && secret.trim() !== "";

	useEffect(() => {
		panelRef.current?.scrollIntoView({ behavior: "smooth", block: "nearest" });
	}, []);

	const cancelBrowserLogin = () => {
		loginCancelledRef.current = true;
		void aoBridge.cloud.cancelProviderAuth();
	};

	// Closing mid-login abandons the login rather than leaving the agent CLI
	// waiting in the background.
	const close = () => {
		if (browserLoginPending) cancelBrowserLogin();
		onClose();
	};

	// On success the panel closes; the row's status line shows the connection.
	const finish = async () => {
		await queryClient.invalidateQueries({ queryKey: providerConnectionsQueryKey });
		onClose();
	};

	const submit = async () => {
		if (!canSubmit) return;
		setBusy(true);
		setError(null);
		try {
			const { providerConnection } = await client.putUserAgentConnection(agent, {
				credentialType,
				secret: secret.trim(),
			});
			if (providerConnection.validationState !== "valid") {
				setError(t("cloudCredential.invalid", { state: providerConnection.validationState }));
				return;
			}
			setSecret("");
			await finish();
		} catch (err) {
			setError(err instanceof Error ? err.message : t("cloudCredential.failed"));
		} finally {
			setBusy(false);
		}
	};

	const loginWithBrowser = async () => {
		if (busy) return;
		setBusy(true);
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
			await finish();
		} catch (err) {
			if (!loginCancelledRef.current) setError(err instanceof Error ? err.message : t("cloudCredential.failed"));
		} finally {
			setBusy(false);
		}
	};

	const otherMethods = methods.filter((entry) => entry.value !== credentialType);
	const hintClass = "text-xs leading-5 text-settings-muted";
	const cancelButton = (
		<Button type="button" size="sm" variant="ghost" disabled={busy && needsSecret} onClick={browserLoginPending ? cancelBrowserLogin : close}>
			{t("cloudCredential.cancel")}
		</Button>
	);

	return (
		<div ref={panelRef} className="flex scroll-my-3 flex-col gap-2 pb-1" data-testid="cloud-harness-login">
			{needsSecret ? (
				<>
					{agent === "claude-code" && credentialType === "oauth_token" ? (
						<ol className={cn(hintClass, "list-decimal pl-4")}>
							{SETUP_TOKEN_STEPS.map((key) => (
								<li key={key}>
									<Trans
										i18nKey={key}
										values={{ command: "claude setup-token", prefix: "sk-ant-oat" }}
										components={{ code: <code className="rounded bg-muted px-1 py-px font-mono text-[11px] text-settings-label" /> }}
									/>
								</li>
							))}
						</ol>
					) : null}
					<div className="flex items-center gap-2">
						<Input
							aria-label={selectedMethod.label}
							type="password"
							autoComplete="off"
							spellCheck={false}
							autoFocus
							className="h-8 min-w-0 flex-1 text-[13px]"
							placeholder={t("cloudCredential.tokenPlaceholder")}
							disabled={busy}
							value={secret}
							onChange={(e) => setSecret(e.target.value)}
							onKeyDown={(e) => {
								if (e.key === "Enter") void submit();
								if (e.key === "Escape") close();
							}}
						/>
						{cancelButton}
						<Button type="button" size="sm" disabled={!canSubmit} onClick={() => void submit()}>
							{busy ? t("cloudCredential.connecting") : t("cloudCredential.connect")}
						</Button>
					</div>
				</>
			) : (
				<div className="flex items-center justify-between gap-3">
					<p className={hintClass}>
						{agent === "claude-code"
							? t("cloudCredential.anthropicLoginDescription")
							: t("cloudCredential.chatgptLoginDescription")}
					</p>
					<div className="flex shrink-0 items-center gap-2">
						{cancelButton}
						<Button type="button" size="sm" disabled={busy} onClick={() => void loginWithBrowser()}>
							{busy
								? t("cloudCredential.waitingForBrowser")
								: agent === "claude-code"
									? t("cloudCredential.loginWithAnthropic")
									: t("cloudCredential.loginWithChatGPT")}
						</Button>
					</div>
				</div>
			)}

			{error ? (
				<p role="alert" className="text-xs leading-5 text-error">
					{error}
				</p>
			) : null}

			{otherMethods.length > 0 && !busy ? (
				<p className={hintClass}>
					{t("cloudCredential.otherMethods")}{" "}
					{otherMethods.map((entry, index) => (
						<span key={entry.value}>
							{index > 0 ? " · " : null}
							<button
								type="button"
								className="text-settings-label underline-offset-2 hover:underline"
								onClick={() => {
									setCredentialType(entry.value);
									setSecret("");
									setError(null);
								}}
							>
								{entry.label}
							</button>
						</span>
					))}
				</p>
			) : null}
		</div>
	);
}
