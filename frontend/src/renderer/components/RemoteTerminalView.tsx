import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useTerminalSession, type AttachableTerminal } from "../hooks/useTerminalSession";
import { TERMINAL_FONT_SIZE_DEFAULT } from "../lib/design-tokens";
import { createTerminalMux, muxUrlFromApiBase } from "../lib/terminal-mux";
import { useSessionLinkNavigation } from "../lib/use-session-link-navigation";
import { useResolvedTheme } from "../stores/ui-store";
import { XtermTerminal } from "./XtermTerminal";

type Props = {
	hostId: string;
	proxyBase: string;
	terminalHandleId?: string;
	terminalGeneration?: string;
	inputDisabled?: boolean;
};

/** Mount identity includes the host, so an equal handle on another box never inherits its socket or screen. */
export function RemoteTerminalView({ hostId, proxyBase, terminalHandleId, terminalGeneration, inputDisabled }: Props) {
	return <RemoteTerminalAttachment key={`${hostId}:${proxyBase}:${terminalHandleId ?? ""}:${terminalGeneration ?? ""}`} hostId={hostId} proxyBase={proxyBase} terminalHandleId={terminalHandleId} inputDisabled={inputDisabled} />;
}

function RemoteTerminalAttachment({ hostId, proxyBase, terminalHandleId, inputDisabled }: Props) {
	const { t } = useTranslation();
	const theme = useResolvedTheme();
	const openSessionLink = useSessionLinkNavigation(hostId);
	const [terminal, setTerminal] = useState<AttachableTerminal | null>(null);
	const [initError, setInitError] = useState(false);
	const createMux = useCallback(() => createTerminalMux(muxUrlFromApiBase(proxyBase)), [proxyBase]);
	// Mux handles are opaque. The shell-handle path reuses the normal PTY attachment
	// while avoiding session side effects that would target the local daemon.
	const { attach, state, error, replaySettled, syncVisibleSize } = useTerminalSession(undefined, {
		createMux,
		daemonReady: true,
		inputDisabled,
		shellTerminalHandleId: terminalHandleId,
	});
	useEffect(() => {
		if (!terminal || !terminalHandleId) return;
		let current = true;
		let detach: (() => void) | undefined;
		void terminal.prepareForActivation().then(() => {
			if (current) detach = attach(terminal);
		});
		return () => { current = false; detach?.(); };
	}, [attach, terminal, terminalHandleId]);

	return <div className="terminal-surface relative h-full min-h-0 pl-2" data-testid="remote-terminal-view">
		<XtermTerminal
			ariaLabel={t("remote.terminalAria")}
			fontSize={TERMINAL_FONT_SIZE_DEFAULT}
			onError={() => setInitError(true)}
			onSessionLinkOpen={openSessionLink}
			onReady={setTerminal}
			onVisibleSize={syncVisibleSize}
			theme={theme}
		/>
		{!terminalHandleId && <p className="absolute inset-0 grid place-items-center text-sm text-muted-foreground">{t("remote.startingTerminal")}</p>}
		{terminalHandleId && state === "connecting" && !replaySettled && <div className="bg-terminal-opaque absolute inset-0" aria-hidden="true" />}
		{state === "reattaching" && <p className="absolute inset-x-2 top-2 rounded bg-surface px-2 py-1 text-xs text-muted-foreground">{t("remote.reconnectingTerminal")}</p>}
		{(state === "error" || initError) && <p role="alert" className="absolute inset-x-2 top-2 rounded bg-surface px-2 py-1 text-xs text-destructive">{error ?? t("remote.openTerminalFailed")}</p>}
	</div>;
}
