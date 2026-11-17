import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { RotateCcw } from "@/components/app-icons";
import { useReopenShellTerminal, type ShellTerminal } from "../hooks/useShellTerminals";
import { apiErrorMessage } from "../lib/api-client";
import type { HostId } from "../lib/hosts";
import { Button } from "./ui/button";

const restoring = new Set<string>();

/**
 * The pane for a shell tab whose process is gone, for example after the app
 * quit or the computer restarted. The first time it is shown it reopens the
 * shell in the same folder with its saved output, resuming the agent that was
 * running in it. Only if that fails does it say the tab ended.
 */
export function EndedShellTerminal({ shell, hostId }: { shell: ShellTerminal; hostId?: HostId }) {
	const { t } = useTranslation();
	const { mutateAsync } = useReopenShellTerminal(hostId);
	const [error, setError] = useState<unknown>();
	const { handleId, resumeHarness } = shell;
	useEffect(() => {
		const key = `${hostId ?? ""}\u0000${handleId}`;
		if (restoring.has(key)) return;
		restoring.add(key);
		void mutateAsync({ handleId, ...(resumeHarness ? { resumeAgent: true } : {}) })
			.catch(setError)
			.finally(() => restoring.delete(key));
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
						setError(undefined);
						mutateAsync({ handleId }).catch(setError);
					}}
				>
					<RotateCcw className="size-icon-base" aria-hidden="true" />
					{t("terminal.reopenShell")}
				</Button>
			</div>
		</div>
	);
}
