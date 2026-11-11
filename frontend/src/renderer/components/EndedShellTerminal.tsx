import { agentLabel } from "@aoagents/product-ui";
import { useTranslation } from "react-i18next";
import { RotateCcw } from "@/components/app-icons";
import { useReopenShellTerminal, type ShellTerminal } from "../hooks/useShellTerminals";
import { apiErrorMessage } from "../lib/api-client";
import type { HostId } from "../lib/hosts";
import { cn } from "../lib/utils";
import { AgentAvatar } from "./AgentAvatar";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/**
 * The pane for a shell tab whose process is gone, for example after the
 * computer restarted. There is no PTY to attach to, so instead of a terminal
 * it says the tab ended and offers to reopen it. Reopening is always the
 * user's choice and starts a fresh shell: nothing the old one ran is replayed.
 * The tab's own close control removes it.
 */
export function EndedShellTerminal({ shell, hostId }: { shell: ShellTerminal; hostId?: HostId }) {
	const { t } = useTranslation();
	const reopen = useReopenShellTerminal(hostId);
	const agent = shell.resumeHarness ? agentLabel(shell.resumeHarness) : undefined;
	const pendingResume = reopen.isPending && Boolean(reopen.variables?.resumeAgent);
	const pendingPlain = reopen.isPending && !reopen.variables?.resumeAgent;
	return (
		<div
			aria-label={t("terminal.shellEnded")}
			className="terminal-surface flex h-full items-center justify-center p-6"
			data-testid="ended-shell-terminal"
			role="region"
		>
			<div className="flex max-w-content-max flex-col items-start gap-3">
				<div>
					<h2 className="text-sm font-medium text-foreground">{t("terminal.shellEnded")}</h2>
					<p className="mt-1 text-xs text-muted-foreground">{t("terminal.shellEndedHelp")}</p>
				</div>
				<div className="flex flex-wrap items-center gap-2">
					<Button
						type="button"
						variant="primary"
						disabled={reopen.isPending}
						onClick={() => reopen.mutate({ handleId: shell.handleId })}
					>
						<RotateCcw className={cn("size-icon-base", pendingPlain && "animate-spin")} aria-hidden="true" />
						{t("terminal.reopenShell")}
					</Button>
					{shell.resumeHarness && agent ? (
						<Tooltip>
							<TooltipTrigger asChild>
								<Button
									type="button"
									variant="secondary"
									disabled={reopen.isPending}
									onClick={() => reopen.mutate({ handleId: shell.handleId, resumeAgent: true })}
								>
									<AgentAvatar
										className={cn("size-icon-sm!", pendingResume && "animate-pulse")}
										decorative
										provider={shell.resumeHarness}
									/>
									{t("terminal.resumeShellAgent", { agent })}
								</Button>
							</TooltipTrigger>
							<TooltipContent side="bottom">{t("terminal.resumeShellAgentHelp", { agent })}</TooltipContent>
						</Tooltip>
					) : null}
				</div>
				{reopen.error ? (
					<p className="text-xs text-destructive" role="alert">
						{apiErrorMessage(reopen.error)}
					</p>
				) : null}
			</div>
		</div>
	);
}
