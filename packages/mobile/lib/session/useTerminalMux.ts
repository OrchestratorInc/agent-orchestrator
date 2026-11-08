import { useCallback, useEffect, useLayoutEffect, useRef, type RefObject } from "react";
import { isConfigured, type ServerConfig } from "../config";
import { MuxClient, type MuxStatus } from "../mux";

/**
 * RIS, the terminal's full reset: clears the screen, the scrollback and every
 * mode. Written ahead of a reattach because the daemon answers each fresh
 * attach with its init handshake, a scrollback replay and a full repaint
 * (backend/internal/terminal/attachment.go). Without it the replay would be
 * appended to what the xterm already shows.
 */
export const TERMINAL_RESET = new Uint8Array([0x1b, 0x63]);

type TerminalWriter = { write(data: Uint8Array): void };

export type TerminalMuxEvents = {
	onStatus(status: MuxStatus): void;
	onExited(code: number): void;
	onError(message: string): void;
	onResize(cols: number, rows: number): void;
};

/**
 * The terminal's attachment to the daemon, one mux socket per verified
 * connection.
 *
 * Two facts are kept apart: whether the xterm WebView is up (`xtermReadyRef`,
 * reset only when the WebView is replaced) and whether the current socket has
 * the PTY open (`openedRef`, reset with every socket). The screen stays mounted
 * while the connection drops on an app switch and comes back, possibly on
 * another endpoint, and that is a new socket under an xterm that is still up:
 * it must open the terminal itself, since the WebView will not initialize
 * again. Folding both facts into one flag left the new socket with no PTY and
 * the output buffered for a WebView that never re-initialized.
 */
export function useTerminalMux(args: {
	config: ServerConfig | null;
	terminalHandleId: string;
	projectId: string | undefined;
	xtermRef: RefObject<TerminalWriter | null>;
	events: TerminalMuxEvents;
}) {
	const { config, terminalHandleId, projectId, xtermRef } = args;
	const xtermReadyRef = useRef(false);
	// Bytes that arrive before the xterm can take them (first load, or a theme
	// change replacing the WebView), kept in wire order.
	const pendingOutputRef = useRef<Uint8Array[]>([]);
	const muxRef = useRef<MuxClient | null>(null);
	const openedRef = useRef(false);
	// Last grid size reported by the WebView's FitAddon, so it reaches the PTY
	// the moment the terminal opens (dims may arrive before or after open).
	const lastDimsRef = useRef<{ cols: number; rows: number } | null>(null);
	const eventsRef = useRef(args.events);
	useLayoutEffect(() => {
		eventsRef.current = args.events;
	});

	const write = useCallback((bytes: Uint8Array) => {
		if (!xtermReadyRef.current || !xtermRef.current) {
			pendingOutputRef.current.push(bytes.slice());
			return;
		}
		xtermRef.current.write(bytes);
	}, [xtermRef]);

	/**
	 * Opens the PTY on the current socket, at most once per socket, at the last
	 * grid the WebView reported. The size rides on the open itself: it is sent
	 * when the socket opens, so it survives being asked for while connecting.
	 */
	const openTerminal = useCallback(() => {
		const mux = muxRef.current;
		if (!mux || openedRef.current) return;
		openedRef.current = true;
		mux.openTerminal(terminalHandleId, projectId, lastDimsRef.current ?? undefined);
	}, [terminalHandleId, projectId]);

	/** The WebView's xterm is up: drain what arrived early and attach. */
	const markXtermReady = useCallback(() => {
		xtermReadyRef.current = true;
		const pending = pendingOutputRef.current;
		pendingOutputRef.current = [];
		for (const bytes of pending) xtermRef.current?.write(bytes);
	}, [xtermRef]);

	// One socket per verified connection. Rebuilt when the endpoint changes,
	// not only when the session does: the app re-races its endpoints when the
	// network moves and on every return from the background, and a mux still
	// pointed at the previous address stays disconnected on a blank screen. The
	// store keeps the config object when endpoint and credential are unchanged.
	useEffect(() => {
		// A selected machine without a verified connection has no terminal.
		if (!config || !isConfigured(config)) return;
		const mux = new MuxClient(config, {
			onStatus: (status) => eventsRef.current.onStatus(status),
			onTerminalData: (tid, bytes) => {
				if (tid === terminalHandleId) write(bytes);
			},
			onTerminalExited: (tid, code) => {
				if (tid === terminalHandleId) eventsRef.current.onExited(code);
			},
			onTerminalError: (tid, message) => {
				if (tid === terminalHandleId) eventsRef.current.onError(message);
			},
			onTerminalResize: (tid, cols, rows) => {
				if (tid === terminalHandleId) eventsRef.current.onResize(cols, rows);
			},
		});
		muxRef.current = mux;
		mux.connect();
		// The xterm is already up: this is a reconnect (back from the background,
		// or onto another endpoint), and the WebView will not initialize again.
		if (xtermReadyRef.current) {
			write(TERMINAL_RESET);
			openTerminal();
		}
		return () => {
			mux.disconnect();
			if (muxRef.current === mux) muxRef.current = null;
			openedRef.current = false;
			pendingOutputRef.current = [];
		};
	}, [terminalHandleId, config, write, openTerminal]);

	return { muxRef, openedRef, lastDimsRef, xtermReadyRef, markXtermReady, openTerminal };
}
