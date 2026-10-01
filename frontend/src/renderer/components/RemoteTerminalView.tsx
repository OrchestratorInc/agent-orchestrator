import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useTerminalSession, type AttachableTerminal } from "../hooks/useTerminalSession";
import { clampTerminalFontSize, initialTerminalFontSize, terminalFontSizeStorageKey } from "../lib/terminal-font-size";
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
	fontSize?: number;
	onChangeFontSize?: (delta: number) => void;
	isFullscreen?: boolean;
	onToggleFullscreen?: () => void | Promise<void>;
};

/** Mount identity includes the host, so an equal handle on another box never inherits its socket or screen. */
export function RemoteTerminalView({ hostId, proxyBase, terminalHandleId, terminalGeneration, ...presentation }: Props) {
	return <RemoteTerminalAttachment key={`${hostId}:${proxyBase}:${terminalHandleId ?? ""}:${terminalGeneration ?? ""}`} hostId={hostId} proxyBase={proxyBase} terminalHandleId={terminalHandleId} {...presentation} />;
}

function RemoteTerminalAttachment({ hostId, proxyBase, terminalHandleId, inputDisabled, fontSize, onChangeFontSize, isFullscreen, onToggleFullscreen }: Props) {
	const { t } = useTranslation();
	const theme = useResolvedTheme();
	const openSessionLink = useSessionLinkNavigation(hostId);
	const surfaceRef = useRef<HTMLDivElement>(null);
	const [savedFontSize, setSavedFontSize] = useState(initialTerminalFontSize);
	const [surfaceFullscreen, setSurfaceFullscreen] = useState(false);
	useEffect(() => {
		const onChange = () => setSurfaceFullscreen(document.fullscreenElement === surfaceRef.current);
		document.addEventListener("fullscreenchange", onChange);
		return () => document.removeEventListener("fullscreenchange", onChange);
	}, []);
	const changeFontSize = useCallback((delta: number) => {
		if (onChangeFontSize) return onChangeFontSize(delta);
		setSavedFontSize((current) => {
			const next = clampTerminalFontSize(current + delta);
			window.localStorage?.setItem(terminalFontSizeStorageKey, String(next));
			return next;
		});
	}, [onChangeFontSize]);
	const toggleFullscreen = useCallback(async () => {
		if (onToggleFullscreen) return onToggleFullscreen();
		const surface = surfaceRef.current;
		if (!surface) return;
		try {
			if (document.fullscreenElement === surface) await document.exitFullscreen();
			else await surface.requestFullscreen();
		} catch (error) {
			console.warn("Unable to toggle terminal fullscreen", error);
		}
	}, [onToggleFullscreen]);
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

	return <div className="terminal-surface relative h-full min-h-0 pl-2" data-testid="remote-terminal-view" ref={surfaceRef}>
		<XtermTerminal
			ariaLabel={t("remote.terminalAria")}
			fontSize={fontSize ?? savedFontSize}
			isFullscreen={isFullscreen ?? surfaceFullscreen}
			onChangeFontSize={changeFontSize}
			onError={() => setInitError(true)}
			onSessionLinkOpen={openSessionLink}
			onReady={setTerminal}
			onToggleFullscreen={toggleFullscreen}
			onVisibleSize={syncVisibleSize}
			theme={theme}
		/>
		{!terminalHandleId && <p className="absolute inset-0 grid place-items-center text-sm text-muted-foreground">{t("remote.startingTerminal")}</p>}
		{terminalHandleId && state === "connecting" && !replaySettled && <div className="bg-terminal-opaque absolute inset-0" aria-hidden="true" />}
		{state === "reattaching" && <p className="absolute inset-x-2 top-2 rounded bg-surface px-2 py-1 text-xs text-muted-foreground">{t("remote.reconnectingTerminal")}</p>}
		{(state === "error" || initError) && <p role="alert" className="absolute inset-x-2 top-2 rounded bg-surface px-2 py-1 text-xs text-destructive">{error ?? t("remote.openTerminalFailed")}</p>}
	</div>;
}
