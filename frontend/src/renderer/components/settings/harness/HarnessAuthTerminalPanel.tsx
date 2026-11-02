import { Check, LogIn, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../../api/schema";
import { closeShellTerminal } from "../../../hooks/useShellTerminals";
import type { TerminalSessionState } from "../../../hooks/useTerminalSession";
import type { AgentId } from "../../../lib/agent-options";
import { apiErrorCode, getApiBaseUrl } from "../../../lib/api-client";
import { baseUrlForHost } from "../../../lib/host-clients";
import { useShellMaybe } from "../../../lib/shell-context";
import { createTerminalMux, muxUrlFromApiBase } from "../../../lib/terminal-mux";
import { useResolvedTheme } from "../../../stores/ui-store";
import { TerminalPane } from "../../TerminalPane";
import { Button } from "../../ui/button";

export type AuthTerminalWorkflow = {
	agentId: AgentId;
	action: string;
	terminal: components["schemas"]["ShellTerminalResponse"];
	guidance: string;
	terminalInput?: string;
	phase: "running" | "verifying" | "unauthorized" | "unverified" | "closing" | "cleanup_failed" | "timed_out";
	reason?: string;
	startedAt: number;
};

export async function closeAuthTerminal(handleId: string, hostId?: string): Promise<void> {
	try {
		await closeShellTerminal(handleId, hostId);
	} catch (error) {
		if (apiErrorCode(error) !== "SHELL_TERMINAL_NOT_FOUND") throw error;
	}
}

export function HarnessAuthTerminalPanel({ workflow, hostId, onClose, onRetry, onTerminalState }: {
	workflow: AuthTerminalWorkflow;
	hostId?: string;
	onClose: () => void;
	onRetry: () => void;
	onTerminalState: (state: TerminalSessionState) => void;
}) {
	const { t } = useTranslation();
	const theme = useResolvedTheme();
	const shell = useShellMaybe();
	// Logout can exit before attachment. Own its mux so a shell-list refresh
	// cannot prune the retained cache entry before we receive the exit event.
	const createMux = useCallback(() => {
		const base = hostId ? baseUrlForHost(hostId) : getApiBaseUrl();
		if (hostId && !base) throw new Error("Remote host disconnected");
		return createTerminalMux(muxUrlFromApiBase(base ?? ""));
	}, [hostId]);
	const panelRef = useRef<HTMLDivElement>(null);
	const inputRequestIdRef = useRef(0);
	const activeInputRequestIdRef = useRef<number | null>(null);
	const [terminalState, setTerminalState] = useState<TerminalSessionState>("connecting");
	const [inputRequest, setInputRequest] = useState<{ id: number; data: string }>();
	const [commandPending, setCommandPending] = useState(false);
	const [commandSent, setCommandSent] = useState(false);
	const handlerRef = useRef(onTerminalState);
	handlerRef.current = onTerminalState;
	const handleTerminalState = useCallback((state: TerminalSessionState) => {
		setTerminalState(state);
		handlerRef.current(state);
	}, []);
	useEffect(() => {
		panelRef.current?.scrollIntoView({ behavior: "smooth", block: "nearest" });
	}, [workflow.terminal.handleId]);
	// While the login runs, the terminal speaks for itself. Guidance is shown only
	// when it tells the user to act outside the terminal (the Open login button).
	const status = workflow.phase === "running"
		? workflow.terminalInput ? workflow.guidance : ""
		: workflow.phase === "verifying" ? t(workflow.action === "logout" ? "settings.harness.checkingLogout" : "settings.harness.checkingLogin")
			: workflow.phase === "closing" ? t("settings.harness.authClosing")
				: workflow.reason ?? t("settings.harness.loginUnknown");
	const retryable = workflow.phase === "unauthorized" || workflow.phase === "unverified" || workflow.phase === "timed_out" || workflow.phase === "cleanup_failed";
	const openAuthAction = () => {
		if (!workflow.terminalInput || terminalState !== "attached" || commandPending || commandSent) return;
		inputRequestIdRef.current += 1;
		activeInputRequestIdRef.current = inputRequestIdRef.current;
		setCommandPending(true);
		setInputRequest({ id: inputRequestIdRef.current, data: workflow.terminalInput });
	};
	const handleInputRequestResult = useCallback((id: number, accepted: boolean) => {
		if (activeInputRequestIdRef.current !== id) return;
		activeInputRequestIdRef.current = null;
		setInputRequest(undefined);
		setCommandPending(false);
		if (accepted) setCommandSent(true);
	}, []);
	return (
		<div ref={panelRef} className="mt-1 scroll-my-3 overflow-hidden rounded-md border border-(--color-border-settings-input) bg-terminal" data-testid="harness-auth-terminal">
			<div className="flex min-h-10 items-center justify-between gap-3 border-b border-(--color-border-settings-input) bg-surface/90 px-3 py-2">
				<div className="min-w-0"><p className="truncate text-xs font-medium text-settings-label">{workflow.terminal.title}</p>{status ? <p className="truncate text-caption text-settings-muted" aria-live="polite" role="status">{status}</p> : null}</div>
				<div className="flex shrink-0 items-center gap-2">
					{workflow.terminalInput && workflow.phase === "running" ? <Button type="button" size="sm" variant="outline" disabled={terminalState !== "attached" || commandPending || commandSent} onClick={openAuthAction}>{commandSent ? <Check aria-hidden="true" /> : <LogIn aria-hidden="true" />}{workflow.action === "setup" ? commandSent ? t("settings.harness.setupOpened") : t("settings.harness.openSetup") : commandSent ? t("settings.harness.loginOpened") : t("settings.harness.openLogin")}</Button> : null}
					<button type="button" aria-label={t("settings.close")} className="grid size-7 place-items-center rounded text-settings-muted hover:bg-interactive-hover" disabled={workflow.phase === "closing" || workflow.phase === "verifying"} onClick={onClose}><X className="size-4" aria-hidden="true" /></button>
				</div>
			</div>
			{/* Full-screen setup TUIs need enough rows for their provider pickers. */}
			<div className="h-[min(600px,75vh)] min-h-0" data-settings-inline-edit=""><TerminalPane createMux={hostId || workflow.action === "logout" ? createMux : undefined} daemonReady={hostId ? true : shell ? shell.daemonStatus.state === "ready" : true} focusRequested={workflow.phase === "running" && terminalState === "attached"} fontSize={12} inputRequest={inputRequest} onInputRequestResult={handleInputRequestResult} onTerminalStateChange={handleTerminalState} terminalTarget={{ kind: "shell", handleId: workflow.terminal.handleId, generation: workflow.terminal.createdAt, title: workflow.terminal.title }} theme={theme} /></div>
			{retryable ? <div className="flex items-center justify-end border-t border-(--color-border-settings-input) bg-surface/90 px-3 py-2"><Button type="button" size="sm" variant="outline" onClick={workflow.phase === "cleanup_failed" ? onClose : onRetry}>{workflow.phase === "cleanup_failed" ? t("settings.harness.retry") : workflow.action === "logout" ? t("settings.harness.logout") : workflow.action === "setup" ? t("settings.harness.setup") : t("settings.harness.login")}</Button></div> : null}
		</div>
	);
}
