import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { RotateCcw } from "@/components/app-icons";
import { useReopenShellTerminal, type ShellTerminal } from "../hooks/useShellTerminals";
import { apiErrorCode, apiErrorMessage } from "../lib/api-client";
import type { HostId } from "../lib/hosts";
import { Button } from "./ui/button";

const restoring = new Map<string, Promise<unknown>>();

export function EndedShellTerminal({ shell, hostId }: { shell: ShellTerminal; hostId?: HostId }) {
	const { t } = useTranslation();
	const { mutateAsync } = useReopenShellTerminal(hostId);
	const [error, setError] = useState<unknown>();
	const { handleId, resumeHarness } = shell;
	useEffect(() => {
		const key = `${hostId ?? ""}\u0000${handleId}`;
		let attempt = restoring.get(key);
		if (!attempt) {
			attempt = mutateAsync({ handleId, ...(resumeHarness ? { resumeAgent: true } : {}) });
			restoring.set(key, attempt);
			const settled = () => restoring.delete(key);
			attempt.then(settled, settled);
		}
		attempt.catch(setError);
	}, [handleId, hostId, mutateAsync, resumeHarness]);

	if (!error) {
		return <div aria-busy="true" className="terminal-surface h-full" data-testid="ended-shell-terminal" />;
	}
	return (
		<div
			aria-label={t("terminal.shellEnded")}
			className="terminal-surface flex h-full items-center justify-center p-6"
			data-testid="ended-shell-terminal"
			role="region"
		>
			<div className="flex flex-col items-start gap-3">
				<h2 className="text-sm font-medium text-foreground">{t("terminal.shellEnded")}</h2>
				<p className="max-w-content-max text-xs text-destructive" role="alert">
					{apiErrorMessage(error)}
				</p>
				<Button
					type="button"
					variant="primary"
					onClick={() => {
						const resumeAgent = Boolean(resumeHarness) && !apiErrorCode(error)?.startsWith("SHELL_TERMINAL_AGENT_");
						setError(undefined);
						mutateAsync({ handleId, ...(resumeAgent ? { resumeAgent: true } : {}) }).catch(setError);
					}}
				>
					<RotateCcw className="size-icon-base" aria-hidden="true" />
					{t("terminal.reopenShell")}
				</Button>
			</div>
		</div>
	);
}
