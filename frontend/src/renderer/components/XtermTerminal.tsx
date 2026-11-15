import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Terminal, type ILink, type ILinkProvider } from "@xterm/xterm";
import { useTranslation } from "react-i18next";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { terminalFontSizeDelta as shortcutFontSizeDelta } from "../../shared/shortcuts";
import type {
	AttachableTerminal,
	TerminalUserInputSource,
} from "../hooks/useTerminalSession";
import { aoBridge } from "../lib/bridge";
import { isDialogOrMenuOpenOutside } from "../lib/dom-selectors";
import { TERMINAL_FONT_SIZE_DEFAULT } from "../lib/design-tokens";
import { isWebLink, openLinkInSystemBrowser } from "../lib/external-link-policy";
import { findSessionLinks } from "../lib/session-links";
import { droppedPathsText } from "../lib/terminal-drop";
import {
	forcesTerminalSelection,
	isForwardableMouseReport,
	isMouseHoverReport,
	parseOsc52Write,
	shouldForceTerminalSelection,
	type TerminalClickMode,
} from "../lib/terminal-mouse";
import { LoaderCircle, TriangleAlert } from "@/components/app-icons";
import { isMacPlatform } from "../lib/platform";
import { applyDocumentTheme, applyDocumentThemeStyle } from "../lib/theme";
import {
	buildCursorColorSchemeNotification,
	cursorColorSchemeReplyForOutput,
} from "../lib/cursor-color-scheme";
import {
	buildOscColorReports,
	createCursorPositionReportForwarder,
	createOscColorReportForwarder,
	type OscTerminalColors,
} from "../lib/osc-color-report";
import { buildTerminalThemes } from "../lib/terminal-themes";
import { leaseWebglRenderer, type WebglLease } from "../lib/terminal-webgl-pool";
import { useUiStore, type Theme, type ThemeStyle } from "../stores/ui-store";
import { TerminalSearch } from "./TerminalSearch";
import { useLinkPreview } from "../hooks/useLinkPreview";
import { LinkPreviewHoverCard } from "./LinkPreviewCard";
import { CloseButton } from "./ui/close-button";
import { HoverCard, HoverCardTrigger } from "./ui/hover-card";
import { Pressable } from "./ui/pressable";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";

type DropFailure = { name: string; reason?: string; maxMegabytes?: number };
type DropNotice = { kind: "placing"; names: string[] } | { kind: "failed"; failures: DropFailure[] };
const LOCAL_DROP_NOTICE_DELAY_MS = 400;
const DROP_FAILURE_NOTICE_MS = 10_000;

export type XtermTerminalProps = {
	ariaLabel?: string;
	saveDroppedFile?: ((file: { name: string; bytes: Uint8Array }) => Promise<string | null | undefined>) | null;
	droppedFileMaxBytes?: number;
	className?: string;
	fontSize?: number;
	isFullscreen?: boolean;
	theme: Theme;
	onChangeFontSize?: (delta: number) => void;
	onToggleFullscreen?: () => void | Promise<void>;
	paneScrollsByKeyboard?: boolean;
	onError?: (error: unknown) => void;
	onVisibleContent?: () => void;
	onLinkOpen?: (uri: string) => void;
	onSessionLinkOpen?: (uri: string) => void;
	onVisibleSize?: (cols: number, rows: number) => void;
	onGridChange?: (cols: number, rows: number) => void;
	isVisible?: boolean;
	supportsCursorColorScheme?: boolean;
	focusRequested?: boolean;
	onReady?: (terminal: AttachableTerminal) => void;
};

const SUPPRESS_NATIVE_PASTE_MS = 100;
const COPY_TOAST_MS = 1400;
const LINK_PREVIEW_OPEN_MS = 300;
const LINK_PREVIEW_CLOSE_MS = 300;
const AUTOFOCUS_RETRY_FRAMES = 6;
const COLOR_SCHEME_UPDATE_MODE = 2031;
const COLOR_SCHEME_QUERY = 996;

function preparePastedText(text: string): string {
	return text.replace(/\r?\n/g, "\r");
}

function bracketPastedText(text: string, bracketedPasteMode: boolean): string {
	return bracketedPasteMode ? `\x1b[200~${text}\x1b[201~` : text;
}

function joinVisuallyContinuousLines(term: Terminal, selection: string, columnSelection: boolean): string {
	if (columnSelection) return selection;
	const range = term.getSelectionPosition();
	if (!range) return selection;
	const lineBreak = selection.includes("\r\n") ? "\r\n" : "\n";
	const lines = selection.split(lineBreak);
	const buffer = term.buffer.active;
	let joined = lines[0] ?? "";
	let index = 0;
	for (let row = range.start.y + 1; row <= range.end.y; row++) {
		if (buffer.getLine(row)?.isWrapped) continue;
		const text = lines[++index];
		if (text === undefined) break;
		const previous = buffer.getLine(row - 1);
		const continuous = !!previous && !/\s$/.test(previous.translateToString(false, 0, term.cols));
		joined += `${continuous ? "" : lineBreak}${text}`;
	}
	return joined;
}

function isTerminalCopyShortcut(event: KeyboardEvent): boolean {
	if (event.key === "Insert") return event.ctrlKey && !event.altKey && !event.metaKey;
	if (event.key.toLowerCase() !== "c") return false;
	if (event.metaKey) return true;
	if (event.ctrlKey && event.shiftKey && !event.altKey) return true;
	return isWindowsPlatform() && event.ctrlKey && !event.shiftKey && !event.altKey && !event.metaKey;
}

function isWindowsPlatform(): boolean {
	const platform =
		(navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform ?? navigator.platform;
	return platform.toLowerCase().startsWith("win");
}

function isTerminalPasteShortcut(event: KeyboardEvent): boolean {
	if (event.key === "Insert") return event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey;
	if (event.key.toLowerCase() !== "v") return false;
	if (event.metaKey) return true;
	if (event.ctrlKey && event.shiftKey && !event.altKey) return true;
	return isWindowsPlatform() && event.ctrlKey && !event.shiftKey && !event.altKey && !event.metaKey;
}

function isTerminalSearchShortcut(event: KeyboardEvent): boolean {
	if (event.altKey || event.shiftKey || event.key.toLowerCase() !== "f") return false;
	return isMacPlatform()
		? event.metaKey && !event.ctrlKey
		: event.ctrlKey && !event.metaKey;
}

function consumeTerminalShortcut(event: KeyboardEvent): void {
	event.preventDefault();
	event.stopPropagation();
}

function terminalFontSizeDelta(event: KeyboardEvent): -1 | 0 | 1 {
	return shortcutFontSizeDelta(
		{
			key: event.key,
			code: event.code,
			ctrl: event.ctrlKey,
			meta: event.metaKey,
			shift: event.shiftKey,
			alt: event.altKey,
		},
		isMacPlatform(),
	);
}

function normalizedTerminalShortcut(event: KeyboardEvent): string | null {
	if (event.shiftKey) return null;

	if (event.metaKey && !event.ctrlKey && !event.altKey && isMacPlatform()) {
		switch (event.key) {
			case "ArrowLeft":
				return "\x01";
			case "ArrowRight":
				return "\x05";
			case "Backspace":
				return "\x15";
			default:
				return null;
		}
	}

	if (event.metaKey) return null;

	if (event.altKey && !event.ctrlKey) {
		switch (event.key) {
			case "ArrowLeft":
				return "\x1bb";
			case "ArrowRight":
				return "\x1bf";
			case "Backspace":
				return "\x1b\x7f";
			case "Delete":
				return "\x1bd";
			default:
				return null;
		}
	}

	if (event.ctrlKey && !event.altKey) {
		switch (event.key) {
			case "ArrowLeft":
				return "\x1b[1;5D";
			case "ArrowRight":
				return "\x1b[1;5C";
			case "Backspace":
				return "\x1b\x7f";
			case "Delete":
				return "\x1bd";
			default:
				return null;
		}
	}

	return null;
}

function isTerminalSearchEvent(event: Event): boolean {
	return event.target instanceof Element && event.target.closest('[role="search"]') !== null;
}

function terminalHasFocus(host: HTMLElement): boolean {
	const activeElement = document.activeElement;
	return !!activeElement && host.contains(activeElement);
}

function canAutoFocusTerminal(host: HTMLElement): boolean {
	if (isDialogOrMenuOpenOutside(host)) return false;
	const activeElement = document.activeElement;
	if (!(activeElement instanceof HTMLElement) || activeElement === document.body || !activeElement.isConnected) return true;
	if (host.contains(activeElement)) return true;
	return (
		activeElement.matches("button[aria-current='page']") ||
		activeElement.matches("button[data-terminal-focus-handoff='true']") ||
		(activeElement.matches("button[role='tab']") &&
			activeElement.closest('[data-testid="session-workspace-topbar"]') !== null) ||
		activeElement.matches("button[data-terminal-tab-action='true']")
	);
}

type XtermInternal = Terminal & {
	_core?: {
		element?: HTMLElement;
		_selectionService?: {
			enable: () => void;
			disable?: () => void;
			shouldForceSelection: (event: MouseEvent) => boolean;
			_mouseMoveListener?: EventListener;
			_dragScrollAmount?: number;
		};
	};
};

type DevXtermHost = HTMLDivElement & {
	__aoXtermForTest?: Terminal;
};

type TerminalContextMenuState = {
	canCopy: boolean;
	open: boolean;
	x: number;
	y: number;
	link: string | null;
};

type TerminalContextMenuAction = "copy" | "paste" | "selectAll";

type TerminalContextMenuActions = Record<TerminalContextMenuAction, () => void>;

const SGR_WHEEL_UP = 64;
const SGR_WHEEL_DOWN = 65;

function sgrWheelReport(button: number, count: number, column: number, row: number): string {
	return `\x1b[<${button};${column};${row}M`.repeat(count);
}

const PAGE_UP = "\x1b[5~";
const PAGE_DOWN = "\x1b[6~";
const MAC_TERMINAL_SCROLLBAR_WIDTH = 7;
const MAC_TERMINAL_SCROLLBAR_IDLE_MS = 700;

function pageKeyReport(lines: number): string {
	return lines < 0 ? PAGE_UP : PAGE_DOWN;
}

function forceSelectionMode(term: Terminal): void {
	const internal = term as XtermInternal;
	const selectionService = internal._core?._selectionService;
	const element = internal._core?.element;
	if (!selectionService || !element) return;
	selectionService.shouldForceSelection = () => true;
	selectionService.enable();
	element.classList.remove("enable-mouse-events");
}

function routeTerminalClicks(
	term: Terminal,
	options: () => { mode: TerminalClickMode; mac: boolean; overLink: boolean },
): void {
	const selectionService = (term as XtermInternal)._core?._selectionService;
	if (!selectionService) return;
	selectionService.shouldForceSelection = (event: MouseEvent) => shouldForceTerminalSelection(event, options());
}

function applyTerminalClickMode(term: Terminal, mode: TerminalClickMode): void {
	const internal = term as XtermInternal;
	const selectionService = internal._core?._selectionService;
	const element = internal._core?.element;
	if (!selectionService || !element) return;
	if (mode === "select") {
		selectionService.enable();
		element.classList.remove("enable-mouse-events");
		return;
	}
	if (term.modes.mouseTrackingMode === "none") return;
	selectionService.disable?.();
	element.classList.add("enable-mouse-events");
}

const OSC52_GESTURE_WINDOW_MS = 1500;
let lastLocalTerminalCopyAt = Number.NEGATIVE_INFINITY;
function markLocalTerminalCopy(): void {
	lastLocalTerminalCopyAt = performance.now();
}
const MOUSE_REPORT_EVENT_WINDOW_MS = 250;
const DRAG_HINT_THRESHOLD_PX = 4;
const SELECT_HINT_MS = 15_000;

function confineDragSelectionToTerminalWidth(term: Terminal): void {
	const internal = term as XtermInternal;
	const selectionService = internal._core?._selectionService;
	const element = internal._core?.element;
	const originalMouseMoveListener = selectionService?._mouseMoveListener;
	if (!selectionService || !element || !originalMouseMoveListener) return;

	selectionService._mouseMoveListener = (event: Event) => {
		if (!(event instanceof MouseEvent)) return;
		const { left, right } = element.getBoundingClientRect();
		if (event.clientX < left || event.clientX > right) {
			selectionService._dragScrollAmount = 0;
			return;
		}
		originalMouseMoveListener(event);
	};
}

function mapStringOffsetToBuffer(
	term: Terminal,
	lineIndex: number,
	columnIndex: number,
	stringOffset: number,
): [number, number] | undefined {
	const buffer = term.buffer.active;
	const cell = buffer.getNullCell();
	let startColumn = columnIndex;
	while (stringOffset > 0) {
		const line = buffer.getLine(lineIndex);
		if (!line) return undefined;
		for (let column = startColumn; column < line.length; column += 1) {
			line.getCell(column, cell);
			if (cell.getWidth() > 0) stringOffset -= cell.getChars().length || 1;
			if (stringOffset < 0) return [lineIndex, column];
		}
		lineIndex += 1;
		startColumn = 0;
	}
	return [lineIndex, startColumn];
}

export function sessionLinkProvider(
	term: Terminal,
	activate: (event: MouseEvent, uri: string) => void,
	hover?: { hover: () => void; leave: () => void },
): ILinkProvider {
	return {
		provideLinks(lineNumber, callback) {
			const buffer = term.buffer.active;
			let firstLine = lineNumber - 1;
			while (firstLine > 0 && buffer.getLine(firstLine)?.isWrapped) firstLine -= 1;
			const lines = [];
			for (let line = firstLine; line < buffer.length; line += 1) {
				if (line > firstLine && !buffer.getLine(line)?.isWrapped) break;
				lines.push(buffer.getLine(line)?.translateToString(false) ?? "");
			}
			const text = lines.join("");
			const links: ILink[] = findSessionLinks(text).flatMap((match) => {
				const start = mapStringOffsetToBuffer(term, firstLine, 0, match.start);
				if (!start) return [];
				const end = mapStringOffsetToBuffer(term, start[0], start[1], match.text.length);
				if (!end) return [];
				const [startLine, startColumn] = start;
				let [endLine, endColumn] = end;
				if (endColumn === 0 && endLine > startLine) {
					endLine -= 1;
					endColumn = term.cols;
				}
				if (lineNumber - 1 < startLine || lineNumber - 1 > endLine) return [];
				return [
					{
						text: match.text,
						range: {
							start: { x: startColumn + 1, y: startLine + 1 },
							end: { x: endColumn, y: endLine + 1 },
						},
						activate: (event) => activate(event, match.text),
						hover: hover?.hover,
						leave: hover?.leave,
					},
				];
			});
			callback(links.length > 0 ? links : undefined);
		},
	};
}

export function XtermTerminal(props: XtermTerminalProps) {
	const { t } = useTranslation();
	const themeStyle = useUiStore((state) => state.themeStyle);
	const windowTranslucency = useUiStore((state) => state.windowTranslucency);
	const macPlatform = isMacPlatform();
	const translucent = macPlatform && windowTranslucency;
	const shellRef = useRef<HTMLDivElement | null>(null);
	const hostRef = useRef<HTMLDivElement | null>(null);
	const saveDroppedFileRef = useRef(props.saveDroppedFile);
	saveDroppedFileRef.current = props.saveDroppedFile;
	const droppedFileMaxBytesRef = useRef(props.droppedFileMaxBytes);
	droppedFileMaxBytesRef.current = props.droppedFileMaxBytes;
	const scrollbarTrackRef = useRef<HTMLDivElement | null>(null);
	const scrollbarThumbRef = useRef<HTMLDivElement | null>(null);
	const termRef = useRef<Terminal | null>(null);
	const webglLeaseRef = useRef<WebglLease | null>(null);
	const gridMeasuredRef = useRef(false);
	const notifyCursorSchemeRef = useRef<(scheme: Theme, force?: boolean, retry?: boolean) => void>(() => {});
	const announcedCursorSchemeRef = useRef<Theme | null>(null);
	const searchAddonRef = useRef<SearchAddon | null>(null);
	const fitRef = useRef<(() => void) | null>(null);
	const colorSchemeReporterRef = useRef<
		((theme: Theme, themeStyle: ThemeStyle, force?: boolean) => void) | null
	>(null);
	const contextMenuActionsRef = useRef<TerminalContextMenuActions | null>(null);
	const [contextMenu, setContextMenu] = useState<TerminalContextMenuState>({
		canCopy: false,
		open: false,
		x: 0,
		y: 0,
		link: null,
	});
	const [copiedToast, setCopiedToast] = useState(false);
	const [selectHint, setSelectHint] = useState(false);
	const selectHintTimerRef = useRef<number | undefined>(undefined);
	const showSelectHintRef = useRef<() => void>(() => undefined);
	const hideSelectHintRef = useRef<() => void>(() => undefined);
	const [dropNotice, setDropNotice] = useState<DropNotice | null>(null);
	const dropNoticeTimerRef = useRef<number | undefined>(undefined);
	const dropNoticeRef = useRef({
		next: 0,
		active: new Map<number, string[]>(),
		failures: [] as DropFailure[],
		begin(names: string[], delayMs: number): number {
			const id = ++this.next;
			this.active.set(id, names);
			if (dropNoticeTimerRef.current !== undefined) window.clearTimeout(dropNoticeTimerRef.current);
			const show = () => {
				dropNoticeTimerRef.current = undefined;
				if (this.active.size > 0) setDropNotice({ kind: "placing", names: [...this.active.values()].flat() });
			};
			if (delayMs > 0) dropNoticeTimerRef.current = window.setTimeout(show, delayMs);
			else show();
			return id;
		},
		end(id: number, failures: DropFailure[]): void {
			this.active.delete(id);
			this.failures = [...this.failures, ...failures];
			if (dropNoticeTimerRef.current !== undefined) window.clearTimeout(dropNoticeTimerRef.current);
			dropNoticeTimerRef.current = undefined;
			if (this.active.size > 0) {
				setDropNotice({ kind: "placing", names: [...this.active.values()].flat() });
				return;
			}
			const shown = this.failures;
			this.failures = [];
			if (shown.length === 0) {
				setDropNotice(null);
				return;
			}
			setDropNotice({ kind: "failed", failures: shown });
			dropNoticeTimerRef.current = window.setTimeout(() => {
				dropNoticeTimerRef.current = undefined;
				setDropNotice(null);
			}, DROP_FAILURE_NOTICE_MS);
		},
	});
	const [searchOpen, setSearchOpen] = useState(false);
	const copiedToastTimerRef = useRef<number | undefined>(undefined);
	const showCopiedToastRef = useRef<() => void>(() => undefined);
	const hoveredLinkRef = useRef<string | null>(null);
	const [linkPreview, setLinkPreview] = useState<{ url: string; x: number; y: number } | null>(null);
	const linkPreviewTimerRef = useRef<number | undefined>(undefined);
	const linkPreviewControlsRef = useRef<{
		show: (url: string, x: number, y: number) => void;
		hide: (immediately?: boolean) => void;
		cancelHide: () => void;
	}>({ show: () => undefined, hide: () => undefined, cancelHide: () => undefined });
	const linkPreviewQuery = useLinkPreview(linkPreview?.url ?? "", linkPreview !== null);
	const callbacksRef = useRef(props);

	const setContextMenuOpen = useCallback((open: boolean) => {
		setContextMenu((current) => ({ ...current, open }));
	}, []);

	const runContextMenuAction = useCallback(
		(action: TerminalContextMenuAction) => {
			contextMenuActionsRef.current?.[action]();
			setContextMenuOpen(false);
		},
		[setContextMenuOpen],
	);
	const focusTerminal = useCallback(() => {
		try {
			termRef.current?.focus();
		} catch {
		}
	}, []);
	const restoreFocusFrameIdsRef = useRef<number[]>([]);
	const cancelPendingFocusRestore = useCallback(() => {
		for (const id of restoreFocusFrameIdsRef.current) cancelAnimationFrame(id);
		restoreFocusFrameIdsRef.current = [];
	}, []);
	const restoreTerminalFocus = useCallback(() => {
		cancelPendingFocusRestore();
		const frameA = requestAnimationFrame(() => {
			const frameB = requestAnimationFrame(() => {
				restoreFocusFrameIdsRef.current = [];
				const host = hostRef.current;
				if (!host || callbacksRef.current.isVisible === false || !canAutoFocusTerminal(host)) return;
				focusTerminal();
			});
			restoreFocusFrameIdsRef.current = [frameB];
		});
		restoreFocusFrameIdsRef.current = [frameA];
	}, [cancelPendingFocusRestore, focusTerminal]);
	const toggleFullscreenAndRestoreFocus = useCallback(async () => {
		try {
			await callbacksRef.current.onToggleFullscreen?.();
		} finally {
			restoreTerminalFocus();
		}
	}, [restoreTerminalFocus]);

	callbacksRef.current = props;
	showCopiedToastRef.current = () => {
		if (callbacksRef.current.isVisible === false) return;
		setCopiedToast(true);
		if (copiedToastTimerRef.current !== undefined) window.clearTimeout(copiedToastTimerRef.current);
		copiedToastTimerRef.current = window.setTimeout(() => {
			setCopiedToast(false);
			copiedToastTimerRef.current = undefined;
		}, COPY_TOAST_MS);
	};
	hideSelectHintRef.current = () => {
		if (selectHintTimerRef.current !== undefined) window.clearTimeout(selectHintTimerRef.current);
		selectHintTimerRef.current = undefined;
		setSelectHint(false);
	};
	showSelectHintRef.current = () => {
		if (callbacksRef.current.isVisible === false) return;
		const ui = useUiStore.getState();
		if (ui.terminalSelectHintShown) return;
		ui.markTerminalSelectHintShown();
		setSelectHint(true);
		selectHintTimerRef.current = window.setTimeout(() => hideSelectHintRef.current(), SELECT_HINT_MS);
	};
	linkPreviewControlsRef.current = {
		show: (url, x, y) => {
			if (callbacksRef.current.isVisible === false) return;
			if (linkPreviewTimerRef.current !== undefined) window.clearTimeout(linkPreviewTimerRef.current);
			linkPreviewTimerRef.current = window.setTimeout(() => {
				linkPreviewTimerRef.current = undefined;
				setLinkPreview({ url, x, y });
			}, LINK_PREVIEW_OPEN_MS);
		},
		hide: (immediately = false) => {
			if (linkPreviewTimerRef.current !== undefined) window.clearTimeout(linkPreviewTimerRef.current);
			if (immediately) {
				linkPreviewTimerRef.current = undefined;
				setLinkPreview(null);
				return;
			}
			linkPreviewTimerRef.current = window.setTimeout(() => {
				linkPreviewTimerRef.current = undefined;
				setLinkPreview(null);
			}, LINK_PREVIEW_CLOSE_MS);
		},
		cancelHide: () => {
			if (linkPreviewTimerRef.current !== undefined) {
				window.clearTimeout(linkPreviewTimerRef.current);
				linkPreviewTimerRef.current = undefined;
			}
		},
	};

	useEffect(
		() => () => {
			if (dropNoticeTimerRef.current !== undefined) window.clearTimeout(dropNoticeTimerRef.current);
			if (copiedToastTimerRef.current !== undefined) window.clearTimeout(copiedToastTimerRef.current);
			if (linkPreviewTimerRef.current !== undefined) window.clearTimeout(linkPreviewTimerRef.current);
			if (selectHintTimerRef.current !== undefined) window.clearTimeout(selectHintTimerRef.current);
		},
		[],
	);

	useEffect(() => {
		applyDocumentTheme(props.theme);
		applyDocumentThemeStyle(themeStyle);
		const term = termRef.current;
		if (!term) return;
		const { dark, light } = buildTerminalThemes(translucent);
		term.options.theme = props.theme === "dark" ? dark : light;
		webglLeaseRef.current?.setEnabled(!translucent);
		colorSchemeReporterRef.current?.(props.theme, themeStyle);
	}, [props.theme, themeStyle, translucent]);

	useEffect(() => {
		if (!termRef.current || !props.supportsCursorColorScheme) return;
		announcedCursorSchemeRef.current = null;
		notifyCursorSchemeRef.current(props.theme, true, true);
	}, [props.theme, props.supportsCursorColorScheme]);

	useEffect(() => {
		const term = termRef.current;
		if (!term || !props.fontSize) return undefined;
		term.options.fontSize = props.fontSize;
		fitRef.current?.();
		const timer = window.setTimeout(() => fitRef.current?.(), 50);
		return () => window.clearTimeout(timer);
	}, [props.fontSize]);

	useEffect(() => {
		const shell = shellRef.current;
		const host = hostRef.current;
		if (!shell || !host) return undefined;
		let reportedFocused = false;
		let reportedFontSize = false;
		const reportFocused = (focused: boolean) => {
			const handlesFontSize = focused && Boolean(callbacksRef.current.onChangeFontSize);
			if (focused === reportedFocused && handlesFontSize === reportedFontSize) return;
			reportedFocused = focused;
			reportedFontSize = handlesFontSize;
			aoBridge.terminal.setFocused(focused, handlesFontSize);
		};
		const handleFocusIn = () => reportFocused(true);
		const handleFocusOut = (event: FocusEvent) => {
			const next = event.relatedTarget;
			if (next instanceof Node && host.contains(next)) return;
			reportFocused(false);
		};
		host.addEventListener("focusin", handleFocusIn);
		host.addEventListener("focusout", handleFocusOut);
		const disposeFontSizeShortcut = aoBridge.terminal.onFontSizeShortcut((delta) => {
			if (!terminalHasFocus(host)) return;
			callbacksRef.current.onChangeFontSize?.(delta);
		});
		const activateLink = (event: MouseEvent, uri: string) => {
			if (uri.startsWith("ao:")) {
				callbacksRef.current.onSessionLinkOpen?.(uri);
				return;
			}
			if (isWebLink(uri)) {
				if (event.altKey || event.metaKey) {
					void openLinkInSystemBrowser(uri);
					return;
				}
				callbacksRef.current.onLinkOpen?.(uri);
				return;
			}
			window.open(uri, "_blank", "noopener");
		};
		let pointerOverLink = false;
		const trackHover = (event: MouseEvent, uri: string) => {
			pointerOverLink = true;
			const webUri = isWebLink(uri) ? uri : null;
			hoveredLinkRef.current = webUri;
			if (webUri) linkPreviewControlsRef.current.show(webUri, event.clientX, event.clientY);
			else linkPreviewControlsRef.current.hide(true);
		};
		const clearHover = () => {
			pointerOverLink = false;
			hoveredLinkRef.current = null;
			linkPreviewControlsRef.current.hide();
		};

		let term: Terminal;
		try {
			const { dark, light } = buildTerminalThemes(translucent);
			term = new Terminal({
				allowTransparency: macPlatform,
				allowProposedApi: true,
				cursorBlink: true,
				fontFamily:
					getComputedStyle(shell).getPropertyValue("--font-mono").trim() ||
					'ui-monospace, Menlo, Monaco, "Courier New", monospace',
				fontSize: props.fontSize ?? TERMINAL_FONT_SIZE_DEFAULT,
				lineHeight: 1.35,
				linkHandler: { activate: activateLink, hover: trackHover, leave: clearHover },
				drawBoldTextInBrightColors: true,
				minimumContrastRatio: 1,
				scrollback: 5000,
				scrollbar: { showScrollbar: isMacPlatform(), width: MAC_TERMINAL_SCROLLBAR_WIDTH },
				vtExtensions: { colorSchemeQuery: false },
				theme: props.theme === "dark" ? dark : light,
			});
		} catch (error) {
			callbacksRef.current.onError?.(error);
			return undefined;
		}

		termRef.current = term;

		const fit = new FitAddon();
		term.loadAddon(fit);
		const unicode = new Unicode11Addon();
		term.loadAddon(unicode);
		term.unicode.activeVersion = "11";
		term.loadAddon(new WebLinksAddon(activateLink, { hover: trackHover, leave: clearHover }));
		term.registerLinkProvider(
			sessionLinkProvider(term, activateLink, {
				hover: () => {
					pointerOverLink = true;
				},
				leave: () => {
					pointerOverLink = false;
				},
			}),
		);
		const searchAddon = new SearchAddon();
		searchAddonRef.current = searchAddon;
		term.loadAddon(searchAddon);

		term.open(host);
		gridMeasuredRef.current = false;
		let visibleContentReported = false;
		const reportVisibleContent = () => {
			if (visibleContentReported || !callbacksRef.current.onVisibleContent) return;
			const buffer = term.buffer.active;
			for (let row = buffer.viewportY; row < buffer.viewportY + term.rows; row++) {
				if (!buffer.getLine(row)?.translateToString(true).trim()) continue;
				visibleContentReported = true;
				callbacksRef.current.onVisibleContent();
				break;
			}
		};
		const visibleContentRender = term.onRender(reportVisibleContent);
		reportVisibleContent();
		if (import.meta.env.DEV) {
			(host as DevXtermHost).__aoXtermForTest = term;
		}
		const webglLease = leaseWebglRenderer(term, () => callbacksRef.current.isVisible !== false, !translucent);
		webglLeaseRef.current = webglLease;
		webglLease.activate();
		term.options.macOptionClickForcesSelection = true;
		const clickMode = () => useUiStore.getState().terminalClickMode;
		if (clickMode() === "select") forceSelectionMode(term);
		routeTerminalClicks(term, () => ({ mode: clickMode(), mac: macPlatform, overLink: pointerOverLink }));
		const clickModeSubscription = useUiStore.subscribe((next, previous) => {
			if (next.terminalClickMode !== previous.terminalClickMode) applyTerminalClickMode(term, next.terminalClickMode);
		});
		confineDragSelectionToTerminalWidth(term);

		const scrollbarTrack = scrollbarTrackRef.current;
		const scrollbarThumb = scrollbarThumbRef.current;
		let scrollbarFrame: number | null = null;
		let scrollbarHideTimer: number | null = null;
		let scrollbarDrag: { pointerId: number; startLine: number; startY: number } | null = null;
		let scrollbarLastActive = 0;
		const hideScrollbarWhenIdle = () => {
			scrollbarHideTimer = null;
			if (!scrollbarTrack || scrollbarDrag) return;
			const idleFor = Date.now() - scrollbarLastActive;
			if (idleFor < MAC_TERMINAL_SCROLLBAR_IDLE_MS) {
				scrollbarHideTimer = window.setTimeout(hideScrollbarWhenIdle, MAC_TERMINAL_SCROLLBAR_IDLE_MS - idleFor);
				return;
			}
			scrollbarTrack.dataset.active = "false";
		};
		const revealScrollbar = () => {
			if (!scrollbarTrack || scrollbarTrack.dataset.scrollable !== "true") return;
			scrollbarLastActive = Date.now();
			if (scrollbarTrack.dataset.active !== "true") scrollbarTrack.dataset.active = "true";
			if (scrollbarDrag || scrollbarHideTimer !== null) return;
			scrollbarHideTimer = window.setTimeout(hideScrollbarWhenIdle, MAC_TERMINAL_SCROLLBAR_IDLE_MS);
		};
		let revealAfterUpdate = false;
		const updateScrollbar = () => {
			scrollbarFrame = null;
			const reveal = revealAfterUpdate;
			revealAfterUpdate = false;
			if (!scrollbarTrack || !scrollbarThumb) return;
			const buffer = term.buffer.active;
			const maxScrollLine = buffer.type === "normal" ? buffer.baseY : 0;
			const trackHeight = scrollbarTrack.clientHeight;
			if (maxScrollLine <= 0 || trackHeight <= 0) {
				scrollbarTrack.dataset.scrollable = "false";
				scrollbarTrack.dataset.active = "false";
				return;
			}
			const totalLines = maxScrollLine + term.rows;
			const thumbHeight = Math.max(24, (trackHeight * term.rows) / totalLines);
			const travel = Math.max(0, trackHeight - thumbHeight);
			const thumbTop = travel * (buffer.viewportY / maxScrollLine);
			scrollbarThumb.style.height = `${thumbHeight}px`;
			scrollbarThumb.style.transform = `translateY(${thumbTop}px)`;
			scrollbarTrack.dataset.scrollable = "true";
			if (reveal) revealScrollbar();
		};
		const scheduleScrollbarUpdate = () => {
			if (!scrollbarTrack || scrollbarFrame !== null) return;
			scrollbarFrame = requestAnimationFrame(updateScrollbar);
		};
		const scrollPositionChange = scrollbarTrack
			? term.onScroll(() => {
				revealAfterUpdate = true;
				scheduleScrollbarUpdate();
			})
			: null;
		const scrollbarResize = scrollbarTrack ? term.onResize(scheduleScrollbarUpdate) : null;
		const scrollToPointer = (clientY: number) => {
			if (!scrollbarTrack || !scrollbarThumb) return;
			const maxScrollLine = term.buffer.active.type === "normal" ? term.buffer.active.baseY : 0;
			const trackRect = scrollbarTrack.getBoundingClientRect();
			const travel = trackRect.height - scrollbarThumb.getBoundingClientRect().height;
			if (maxScrollLine <= 0 || travel <= 0) return;
			const thumbTop = Math.min(travel, Math.max(0, clientY - trackRect.top - scrollbarThumb.offsetHeight / 2));
			term.scrollToLine(Math.round((thumbTop / travel) * maxScrollLine));
		};
		const scrollbarPointerDown = (event: PointerEvent) => {
			if (!scrollbarTrack || !scrollbarThumb || scrollbarTrack.dataset.scrollable !== "true") return;
			event.preventDefault();
			scrollbarTrack.setPointerCapture(event.pointerId);
			if (event.target !== scrollbarThumb) scrollToPointer(event.clientY);
			scrollbarDrag = {
				pointerId: event.pointerId,
				startLine: term.buffer.active.viewportY,
				startY: event.clientY,
			};
			revealScrollbar();
		};
		const scrollbarPointerMove = (event: PointerEvent) => {
			if (!scrollbarDrag || scrollbarDrag.pointerId !== event.pointerId || !scrollbarTrack || !scrollbarThumb) return;
			const maxScrollLine = term.buffer.active.type === "normal" ? term.buffer.active.baseY : 0;
			const travel = scrollbarTrack.clientHeight - scrollbarThumb.offsetHeight;
			if (maxScrollLine <= 0 || travel <= 0) return;
			const lineDelta = ((event.clientY - scrollbarDrag.startY) / travel) * maxScrollLine;
			term.scrollToLine(Math.round(scrollbarDrag.startLine + lineDelta));
		};
		const scrollbarPointerUp = (event: PointerEvent) => {
			if (!scrollbarDrag || scrollbarDrag.pointerId !== event.pointerId) return;
			scrollbarDrag = null;
			if (scrollbarTrack?.hasPointerCapture(event.pointerId)) scrollbarTrack.releasePointerCapture(event.pointerId);
			revealScrollbar();
		};
		scrollbarTrack?.addEventListener("pointerdown", scrollbarPointerDown);
		scrollbarTrack?.addEventListener("pointermove", scrollbarPointerMove);
		scrollbarTrack?.addEventListener("pointerup", scrollbarPointerUp);
		scrollbarTrack?.addEventListener("pointercancel", scrollbarPointerUp);
		scheduleScrollbarUpdate();

		let columnSelectionActive = false;
		let pendingColumnSelection: boolean | null = null;
		let appGestureEndedAt = Number.NEGATIVE_INFINITY;
		const copySelection = (options?: { clipboardData?: DataTransfer | null }) => {
			const selection = joinVisuallyContinuousLines(term, term.getSelection(), columnSelectionActive);
			if (!selection) return false;
			markLocalTerminalCopy();
			options?.clipboardData?.setData("text/plain", selection);
			void aoBridge.clipboard
				.writeText(selection)
				.then(() => {
					showCopiedToastRef.current();
				})
				.catch((error) => {
					console.warn("Unable to copy terminal selection", error);
				});
			return true;
		};
		const userInputListeners = new Set<(data: string, source: TerminalUserInputSource) => boolean | void>();
		const emitUserInput = (data: string, source: TerminalUserInputSource) => {
			if (data.length === 0) return false;
			let accepted = false;
			userInputListeners.forEach((listener) => {
				if (listener(data, source) === true) accepted = true;
			});
			return accepted;
		};
		let colorSchemeUpdatesEnabled = false;
		let currentColorScheme = props.theme;
		let currentThemeStyle = themeStyle;
		const reportColorScheme = (theme: Theme, nextThemeStyle: ThemeStyle, force = false) => {
			const changed = theme !== currentColorScheme || nextThemeStyle !== currentThemeStyle;
			currentColorScheme = theme;
			currentThemeStyle = nextThemeStyle;
			if (!force && (!colorSchemeUpdatesEnabled || !changed)) return;
			emitUserInput(`\x1b[?997;${theme === "dark" ? 1 : 2}n`, "protocol");
		};
		const hasCsiMode = (params: (number | number[])[], mode: number) =>
			params.some((param) => param === mode);
		colorSchemeReporterRef.current = reportColorScheme;
		const setColorSchemeUpdates = term.parser.registerCsiHandler(
			{ prefix: "?", final: "h" },
			(params) => {
				if (!hasCsiMode(params, COLOR_SCHEME_UPDATE_MODE)) return false;
				colorSchemeUpdatesEnabled = true;
				currentColorScheme = callbacksRef.current.theme;
				currentThemeStyle = useUiStore.getState().themeStyle;
				return params.length === 1;
			},
		);
		const resetColorSchemeUpdates = term.parser.registerCsiHandler(
			{ prefix: "?", final: "l" },
			(params) => {
				if (!hasCsiMode(params, COLOR_SCHEME_UPDATE_MODE)) return false;
				colorSchemeUpdatesEnabled = false;
				return params.length === 1;
			},
		);
		const queryColorSchemeCapability = term.parser.registerCsiHandler(
			{ prefix: "?", intermediates: "$", final: "p" },
			(params) => {
				if (!hasCsiMode(params, COLOR_SCHEME_UPDATE_MODE)) return false;
				emitUserInput(`\x1b[?${COLOR_SCHEME_UPDATE_MODE};${colorSchemeUpdatesEnabled ? 1 : 2}$y`, "protocol");
				return params.length === 1;
			},
		);
		const queryColorScheme = term.parser.registerCsiHandler(
			{ prefix: "?", final: "n" },
			(params) => {
				if (!hasCsiMode(params, COLOR_SCHEME_QUERY)) return false;
				reportColorScheme(
					callbacksRef.current.theme,
					useUiStore.getState().themeStyle,
					true,
				);
				return params.length === 1;
			},
		);
		const terminalColorsForScheme = (scheme: Theme): OscTerminalColors => {
			const palette = buildTerminalThemes()[scheme];
			return {
				foreground: palette.foreground ?? "",
				background: palette.background ?? "",
				cursor: palette.cursor ?? palette.foreground ?? "",
			};
		};
		const notifyCursorScheme = (scheme: Theme, force = false, retry = false) => {
			if (!force && announcedCursorSchemeRef.current === scheme) return;
			announcedCursorSchemeRef.current = scheme;
			const bytes =
				buildOscColorReports(terminalColorsForScheme(scheme)) +
				buildCursorColorSchemeNotification(scheme);
			const send = () => emitUserInput(bytes, "protocol");
			send();
			if (!retry) return;
			for (const timer of schemeRetryTimers) window.clearTimeout(timer);
			schemeRetryTimers = [50, 200].map((delayMs) => window.setTimeout(send, delayMs));
		};
		let schemeRetryTimers: number[] = [];
		notifyCursorSchemeRef.current = notifyCursorScheme;
		const pasteText = (text: string) => {
			const prepared = preparePastedText(text);
			const bracketed = term.modes.bracketedPasteMode && term.options.ignoreBracketedPasteMode !== true;
			emitUserInput(bracketPastedText(prepared, bracketed), "paste");
		};
		let suppressNextNativePaste = false;
		let suppressPasteTimer: number | null = null;
		const clearSuppressNativePaste = () => {
			suppressNextNativePaste = false;
			if (suppressPasteTimer !== null) {
				window.clearTimeout(suppressPasteTimer);
				suppressPasteTimer = null;
			}
		};
		const suppressNativePasteOnce = () => {
			suppressNextNativePaste = true;
			if (suppressPasteTimer !== null) window.clearTimeout(suppressPasteTimer);
			suppressPasteTimer = window.setTimeout(clearSuppressNativePaste, SUPPRESS_NATIVE_PASTE_MS);
		};
		const pasteFromClipboard = () => {
			void aoBridge.clipboard
				.readText()
				.then(pasteText)
				.catch((error) => {
					console.warn("Unable to paste terminal clipboard text", error);
				});
		};
		const focusTerminal = () => {
			try {
				term.focus();
			} catch {
			}
		};
		contextMenuActionsRef.current = {
			copy: () => {
				copySelection();
				focusTerminal();
			},
			paste: () => {
				pasteFromClipboard();
				focusTerminal();
			},
			selectAll: () => {
				columnSelectionActive = false;
				pendingColumnSelection = null;
				term.selectAll();
				focusTerminal();
			},
		};
		const openContextMenu = (event: MouseEvent) => {
			if (isTerminalSearchEvent(event)) return;
			event.preventDefault();
			event.stopPropagation();
			setContextMenu({
				canCopy: term.hasSelection(),
				open: true,
				x: event.clientX,
				y: event.clientY,
				link: hoveredLinkRef.current,
			});
		};
		shell.addEventListener("contextmenu", openContextMenu);
		term.attachCustomKeyEventHandler((event) => {
			if (event.type === "keyup" || event.type === "keypress") return true;
			if (isTerminalSearchShortcut(event)) {
				consumeTerminalShortcut(event);
				setSearchOpen(true);
				return false;
			}
			if (event.key === "Enter" && event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey) {
				consumeTerminalShortcut(event);
				emitUserInput("\x1b\r", "keyboard");
				return false;
			}
			const fontSizeDelta = terminalFontSizeDelta(event);
			if (fontSizeDelta !== 0 && callbacksRef.current.onChangeFontSize) {
				consumeTerminalShortcut(event);
				callbacksRef.current.onChangeFontSize(fontSizeDelta);
				return false;
			}
			if (isTerminalCopyShortcut(event)) {
				if (copySelection()) {
					consumeTerminalShortcut(event);
					return false;
				}
				if ((event.ctrlKey && event.shiftKey) || (event.key === "Insert" && event.ctrlKey)) {
					consumeTerminalShortcut(event);
					return false;
				}
				return true;
			}
			if (isTerminalPasteShortcut(event)) {
				consumeTerminalShortcut(event);
				suppressNativePasteOnce();
				pasteFromClipboard();
				return false;
			}
			const normalized = normalizedTerminalShortcut(event);
			if (!normalized) return true;
			consumeTerminalShortcut(event);
			emitUserInput(normalized, "shortcut");
			return false;
		});
		const copyInput = (event: ClipboardEvent) => {
			if (isTerminalSearchEvent(event)) return;
			if (!copySelection({ clipboardData: event.clipboardData })) return;
			event.preventDefault();
		};
		const copyShortcut = (event: KeyboardEvent) => {
			if (isTerminalSearchEvent(event) || !isTerminalCopyShortcut(event) || !terminalHasFocus(shell) || !copySelection()) return;
			event.preventDefault();
			event.stopPropagation();
		};
		shell.addEventListener("copy", copyInput);
		window.addEventListener("keydown", copyShortcut, true);

		let pointerSelectionArmed = false;
		let appGesture: { x: number; y: number; dragged: boolean } | null = null;
		const disarmPointerSelection = () => {
			pointerSelectionArmed = false;
			pendingColumnSelection = null;
			appGesture = null;
		};
		const pointerDown = (event: PointerEvent) => {
			if (event.button !== 0) return;
			pointerSelectionArmed = true;
			pendingColumnSelection = event.altKey && !isMacPlatform();
			const toApp =
				term.modes.mouseTrackingMode !== "none" &&
				!shouldForceTerminalSelection(event, { mode: clickMode(), mac: macPlatform, overLink: pointerOverLink });
			appGesture = toApp ? { x: event.clientX, y: event.clientY, dragged: false } : null;
			if (!toApp && term.modes.mouseTrackingMode !== "none" && forcesTerminalSelection(event, macPlatform)) {
				hideSelectHintRef.current();
			}
		};
		const pointerMove = (event: PointerEvent) => {
			if (!appGesture || appGesture.dragged || !(event.buttons & 1)) return;
			if (Math.hypot(event.clientX - appGesture.x, event.clientY - appGesture.y) < DRAG_HINT_THRESHOLD_PX) return;
			appGesture.dragged = true;
			showSelectHintRef.current();
		};
		const pointerUp = (event: PointerEvent) => {
			if (event.button !== 0) return;
			if (!pointerSelectionArmed) return;
			pointerSelectionArmed = false;
			columnSelectionActive = pendingColumnSelection ?? false;
			pendingColumnSelection = null;
			if (appGesture) {
				appGesture = null;
				appGestureEndedAt = performance.now();
				return;
			}
			if (!useUiStore.getState().terminalCopyOnSelect) return;
			copySelection();
		};
		host.addEventListener("pointerdown", pointerDown);
		document.addEventListener("pointermove", pointerMove);
		document.addEventListener("pointerup", pointerUp);
		document.addEventListener("pointercancel", disarmPointerSelection);
		window.addEventListener("blur", disarmPointerSelection);

		const proposeGrid = () => (host.clientWidth > 0 && host.clientHeight > 0 ? fit.proposeDimensions() : undefined);
		let pendingReplayWrites = 0;
		let usesSynchronizedOutput = false;
		let resizeGeneration = 0;
		let coveredResizeGeneration = 0;
		let parsedResizeGeneration = -1;
		let waitingForResizeOutput = false;
		const fitTerminal = () => {
			if (callbacksRef.current.isVisible === false) return;
			try {
				const grid = proposeGrid();
				if (grid) resizeGrid(grid.cols, grid.rows);
			} catch {
			}
		};
		const FIT_QUIET_MS = 120;
		const FIT_CAP_MS = 500;
		let fitQuietTimer: ReturnType<typeof setTimeout> | null = null;
		let fitCapTimer: ReturnType<typeof setTimeout> | null = null;
		let fitAllowsHidden = false;
		let disposed = false;
		const fitSettledListeners = new Set<() => void>();
		const flushScheduledFit = () => {
			if (disposed) return;
			if (fitQuietTimer !== null) {
				clearTimeout(fitQuietTimer);
				fitQuietTimer = null;
			}
			if (fitCapTimer !== null) {
				clearTimeout(fitCapTimer);
				fitCapTimer = null;
			}
			if (fitAllowsHidden || callbacksRef.current.isVisible !== false) {
				try {
					const grid = proposeGrid();
					if (grid) resizeGrid(grid.cols, grid.rows, fitAllowsHidden);
				} catch {
				}
			}
			fitAllowsHidden = false;
			for (const listener of [...fitSettledListeners]) listener();
			fitSettledListeners.clear();
		};
		const scheduleStableFit = (allowHidden = false, onSettled?: () => void) => {
			if (disposed) return;
			if (!allowHidden && callbacksRef.current.isVisible === false) return;
			fitAllowsHidden ||= allowHidden;
			if (onSettled) fitSettledListeners.add(onSettled);
			if (fitQuietTimer !== null) clearTimeout(fitQuietTimer);
			fitQuietTimer = setTimeout(flushScheduledFit, FIT_QUIET_MS);
			if (fitCapTimer === null) fitCapTimer = setTimeout(flushScheduledFit, FIT_CAP_MS);
		};
		const scheduleVisibleFit = () => scheduleStableFit(fitAllowsHidden);
		fitRef.current = scheduleVisibleFit;
		let liveFitFrame: number | null = null;
		const scheduleLiveFit = () => {
			if (disposed || callbacksRef.current.isVisible === false || liveFitFrame !== null) return;
			liveFitFrame = requestAnimationFrame(() => {
				liveFitFrame = null;
				fitTerminal();
			});
		};

		const raf = requestAnimationFrame(fitTerminal);
		const settleTimers = [50, 250, 600, 1200].map((ms) => window.setTimeout(scheduleVisibleFit, ms));
		if (document.fonts?.ready) {
			void document.fonts.ready.then(() => scheduleStableFit());
		}
		const terminalStyle = term.element ? getComputedStyle(term.element) : undefined;
		const paddingX = (parseFloat(terminalStyle?.paddingLeft ?? "0") || 0)
			+ (parseFloat(terminalStyle?.paddingRight ?? "0") || 0);
		const paddingY = (parseFloat(terminalStyle?.paddingTop ?? "0") || 0)
			+ (parseFloat(terminalStyle?.paddingBottom ?? "0") || 0);
		const resizeCover = document.createElement("canvas");
		resizeCover.setAttribute("aria-hidden", "true");
		Object.assign(resizeCover.style, {
			position: "absolute", pointerEvents: "none", zIndex: "1",
			backgroundColor: "var(--color-bg-terminal-opaque)",
		});
		let hasResizeFrame = false;
		const cancelResizeCover = () => {
			if (!waitingForResizeOutput) return;
			waitingForResizeOutput = false;
			resizeCover.remove();
			term.refresh(0, term.rows - 1);
		};
		host.addEventListener("wheel", cancelResizeCover, { passive: true });
		host.addEventListener("keydown", cancelResizeCover);
		host.addEventListener("pointerdown", cancelResizeCover);
		const paintedResizeFrame = term.onRender(() => {
			if (disposed || term.modes.synchronizedOutputMode) return;
			if (waitingForResizeOutput && parsedResizeGeneration < coveredResizeGeneration) return;
			waitingForResizeOutput = false;
			resizeCover.remove();
			hasResizeFrame = false;
			if (callbacksRef.current.isVisible === false || !usesSynchronizedOutput) {
				resizeCover.width = 0;
				resizeCover.height = 0;
				return;
			}
			const screen = host.querySelector<HTMLElement>(".xterm-screen");
			const source = screen?.querySelector<HTMLCanvasElement>(":scope > canvas:not(.xterm-link-layer)");
			if (!screen || !source || source.width === 0 || source.height === 0) return;
			const context = resizeCover.getContext("2d");
			if (!context) return;
			if (resizeCover.width !== source.width) resizeCover.width = source.width;
			if (resizeCover.height !== source.height) resizeCover.height = source.height;
			context.clearRect(0, 0, resizeCover.width, resizeCover.height);
			context.drawImage(source, 0, 0);
			resizeCover.style.width = source.style.width;
			resizeCover.style.height = source.style.height;
			resizeCover.style.left = `${screen.offsetLeft}px`;
			resizeCover.style.top = `${screen.offsetTop}px`;
			hasResizeFrame = true;
		});
		const resizeGrid = (cols: number, rows: number, allowHidden = false) => {
			if (disposed || (!allowHidden && callbacksRef.current.isVisible === false)) return;
			const firstMeasurement = !gridMeasuredRef.current;
			gridMeasuredRef.current = true;
			if (cols !== term.cols || rows !== term.rows) {
				const buffer = term.buffer.active;
				const wasAtBottom = buffer.type === "normal" && buffer.viewportY === buffer.baseY;
				resizeGeneration++;
				if (hasResizeFrame && usesSynchronizedOutput && !waitingForResizeOutput) {
					coveredResizeGeneration = resizeGeneration;
					waitingForResizeOutput = true;
					host.appendChild(resizeCover);
				}
				term.resize(cols, rows);
				if (wasAtBottom) term.scrollToBottom();
			}
			if (firstMeasurement && callbacksRef.current.isVisible !== false) {
				callbacksRef.current.onVisibleSize?.(term.cols, term.rows);
			}
			if (callbacksRef.current.isVisible !== false) callbacksRef.current.onGridChange?.(term.cols, term.rows);
		};
		const synchronizedFrames = term.parser.registerCsiHandler({ prefix: "?", final: "h" }, (params) => {
			if (params.includes(2026)) usesSynchronizedOutput = true;
			return false;
		});
		const didParseResizeOutput = (generation: number) => {
			if (!waitingForResizeOutput || generation < coveredResizeGeneration) return;
			parsedResizeGeneration = Math.max(parsedResizeGeneration, generation);
			term.refresh(0, term.rows - 1);
		};
		const synchronizedCapability = term.parser.registerCsiHandler(
			{ prefix: "?", intermediates: "$", final: "p" },
			(params) => {
				if (params[0] !== 2026) return false;
				if (pendingReplayWrites === 0) {
					emitUserInput(`\x1b[?2026;${term.modes.synchronizedOutputMode ? 1 : 2}$y`, "protocol");
				}
				return true;
			},
		);
		let wasDragging = document.body.classList.contains("is-resizing-x");
		const dragStateObserver = new MutationObserver(() => {
			const dragging = document.body.classList.contains("is-resizing-x");
			if (wasDragging && !dragging && callbacksRef.current.isVisible !== false) {
				const grid = proposeGrid();
				if (grid) resizeGrid(grid.cols, grid.rows);
				term.refresh(0, term.rows - 1);
			}
			wasDragging = dragging;
		});
		dragStateObserver.observe(document.body, { attributes: true, attributeFilter: ["class"] });
		let observedBox: { width: number; height: number } | null = null;
		const gridFromObservedBox = () => {
			const cell = term.dimensions?.css.cell;
			if (!observedBox || observedBox.width <= 0 || observedBox.height <= 0) return undefined;
			if (!cell || !Number.isFinite(cell.width) || !Number.isFinite(cell.height)
				|| cell.width <= 0 || cell.height <= 0) {
				return undefined;
			}
			const gutter = term.options.scrollback === 0 || term.options.scrollbar?.showScrollbar === false
				? 0 : (term.options.scrollbar?.width ?? MAC_TERMINAL_SCROLLBAR_WIDTH);
			return {
				cols: Math.max(2, Math.floor((Math.floor(observedBox.width) - paddingX - gutter) / cell.width)),
				rows: Math.max(1, Math.floor((Math.floor(observedBox.height) - paddingY) / cell.height)),
			};
		};
		const inspectorSliding = () =>
			host.closest(".session-split")?.querySelector('[data-slot="inspector-gap"][data-motion="animated"]') != null;
		const settleAfterSlide = (event: TransitionEvent) => {
			if (event.propertyName !== "width" || fitQuietTimer === null) return;
			if (!(event.target instanceof Element) || event.target.getAttribute("data-slot") !== "inspector-gap") return;
			if (!event.target.closest(".session-split")?.contains(host)) return;
			flushScheduledFit();
		};
		document.addEventListener("transitionend", settleAfterSlide);
		const observer = new ResizeObserver((entries) => {
			if (disposed) return;
			const observed = entries.find((entry) => entry.target === host)?.contentRect;
			if (observed) observedBox = { width: observed.width, height: observed.height };
			if (callbacksRef.current.isVisible === false) {
				waitingForResizeOutput = false;
				hasResizeFrame = false;
				resizeCover.remove();
				scheduleVisibleFit();
				return;
			}
			if (!observed) {
				scheduleLiveFit();
				return;
			}
			if (observed.width <= 0 || observed.height <= 0) return;
			if (inspectorSliding()) {
				scheduleStableFit();
				return;
			}
			const grid = gridFromObservedBox();
			if (!grid) {
				scheduleLiveFit();
				return;
			}
			resizeGrid(grid.cols, grid.rows);
		});
		observer.observe(host);

		const STABLE_FRAMES_TARGET = 3;
		const MAX_REFITS = 20;
		let stableFrames = 0;
		let refits = 0;
		let pending: { cols: number; rows: number } | null = null;
		const stabilizer = term.onRender(() => {
			const proposed = gridFromObservedBox() ?? proposeGrid();
			if (!proposed || !proposed.cols || !proposed.rows) return;
			if (proposed.cols !== term.cols || proposed.rows !== term.rows) {
				stableFrames = 0;
				if (pending && pending.cols === proposed.cols && pending.rows === proposed.rows) {
					pending = null;
					if (refits++ >= MAX_REFITS) {
						stabilizer.dispose();
						return;
					}
					resizeGrid(proposed.cols, proposed.rows);
					return;
				}
				pending = { cols: proposed.cols, rows: proposed.rows };
				return;
			}
			pending = null;
			if (++stableFrames >= STABLE_FRAMES_TARGET) stabilizer.dispose();
		});

		window.addEventListener("resize", scheduleLiveFit);

		const oscColorForwarder = createOscColorReportForwarder((report) => {
			emitUserInput(report, "protocol");
		});
		const cursorPositionForwarder = createCursorPositionReportForwarder((report) => {
			emitUserInput(report, "protocol");
		});
		let mouseEventSeenAt = Number.NEGATIVE_INFINITY;
		const noteMouseEvent = () => {
			mouseEventSeenAt = performance.now();
		};
		for (const type of ["mousedown", "mousemove", "mouseup"] as const) {
			window.addEventListener(type, noteMouseEvent, true);
		}
		const forwardMouseReport = (data: string) => {
			if (clickMode() !== "app") return;
			if (performance.now() - mouseEventSeenAt > MOUSE_REPORT_EVENT_WINDOW_MS) return;
			if (!isForwardableMouseReport(data)) return;
			emitUserInput(data, isMouseHoverReport(data) ? "hover" : "mouse");
		};
		const protocolInput = term.onData((data) => {
			if (pendingReplayWrites === 0) oscColorForwarder.push(data);
			cursorPositionForwarder.push(data);
			forwardMouseReport(data);
		});
		const binaryMouseInput = term.onBinary(forwardMouseReport);
		const osc52Clipboard = term.parser.registerOscHandler(52, (data) => {
			const text = parseOsc52Write(data);
			if (text === null || pendingReplayWrites > 0) return true;
			if (clickMode() !== "app" || !useUiStore.getState().terminalCopyOnSelect) return true;
			if (performance.now() - appGestureEndedAt > OSC52_GESTURE_WINDOW_MS) return true;
			if (lastLocalTerminalCopyAt >= appGestureEndedAt) return true;
			appGestureEndedAt = Number.NEGATIVE_INFINITY;
			void aoBridge.clipboard
				.writeText(text)
				.then(() => showCopiedToastRef.current())
				.catch((error) => console.warn("Unable to copy terminal OSC 52 text", error));
			return true;
		});
		const keyInput = term.onKey(({ key }) => emitUserInput(key, "keyboard"));

		let wheelAccumPx = 0;
		term.attachCustomWheelEventHandler((event) => {
			if (event.ctrlKey || event.metaKey) return false;
			let lines: number;
			if (event.deltaMode === 1) {
				lines = Math.trunc(event.deltaY) || Math.sign(event.deltaY);
			} else if (event.deltaMode === 2) {
				lines = (Math.trunc(event.deltaY) || Math.sign(event.deltaY)) * term.rows;
			} else {
				const rowHeight = (term.options.fontSize ?? TERMINAL_FONT_SIZE_DEFAULT) * (term.options.lineHeight ?? 1);
				wheelAccumPx += event.deltaY;
				lines = Math.trunc(wheelAccumPx / rowHeight);
				wheelAccumPx -= lines * rowHeight;
			}
			if (lines === 0) return false;
			if (callbacksRef.current.paneScrollsByKeyboard) {
				emitUserInput(pageKeyReport(lines), "wheel");
				return false;
			}
			if (term.modes.mouseTrackingMode === "none" && term.buffer.active.type === "normal") {
				term.scrollLines(lines);
				return false;
			}
			if (term.modes.mouseTrackingMode !== "none") {
				const button = lines < 0 ? SGR_WHEEL_UP : SGR_WHEEL_DOWN;
				const screen = term.screenElement?.getBoundingClientRect();
				if (!screen || screen.width <= 0 || screen.height <= 0
					|| !Number.isFinite(event.clientX) || !Number.isFinite(event.clientY)) return false;
				const column = Math.max(1, Math.min(term.cols,
					Math.floor((event.clientX - screen.left) * term.cols / screen.width) + 1));
				const row = Math.max(1, Math.min(term.rows,
					Math.floor((event.clientY - screen.top) * term.rows / screen.height) + 1));
				emitUserInput(sgrWheelReport(button, Math.abs(lines), column, row), "wheel");
				return false;
			}
			emitUserInput(pageKeyReport(lines), "wheel");
			return false;
		});
		const pasteInput = (event: ClipboardEvent) => {
			if (isTerminalSearchEvent(event)) return;
			event.preventDefault();
			event.stopPropagation();
			if (suppressNextNativePaste) {
				clearSuppressNativePaste();
				return;
			}
			const text = event.clipboardData?.getData("text/plain") ?? "";
			pasteText(text);
		};
		const compositionInput = (event: CompositionEvent) => {
			if (isTerminalSearchEvent(event)) return;
			emitUserInput(event.data, "composition");
		};
		shell.addEventListener("paste", pasteInput, true);
		shell.addEventListener("compositionend", compositionInput, true);

		const isFileDrag = (event: DragEvent) => Array.from(event.dataTransfer?.types ?? []).includes("Files");
		const dragOverInput = (event: DragEvent) => {
			if (!isFileDrag(event) || saveDroppedFileRef.current === null) return;
			event.preventDefault();
			if (event.dataTransfer) event.dataTransfer.dropEffect = "copy";
		};
		const isDirectoryDrag = (event: DragEvent) =>
			event.dataTransfer?.items?.[0]?.webkitGetAsEntry?.()?.isDirectory ?? false;
		const dropInput = (event: DragEvent) => {
			if (isDirectoryDrag(event) || saveDroppedFileRef.current === null) return;
			const files = Array.from(event.dataTransfer?.files ?? []);
			if (files.length === 0) return;
			event.preventDefault();
			void (async () => {
				const paths: string[] = [];
				const failures: DropFailure[] = [];
				const sandbox = saveDroppedFileRef.current;
				const save = sandbox ?? ((dropped: { name: string; bytes: Uint8Array }) => aoBridge.terminal.saveDroppedFile(dropped));
				const maxBytes = sandbox ? droppedFileMaxBytesRef.current : undefined;
				const drop = dropNoticeRef.current.begin(files.map((file) => file.name), sandbox ? 0 : LOCAL_DROP_NOTICE_DELAY_MS);
				try {
					for (const file of files) {
						if (maxBytes !== undefined && file.size > maxBytes) {
							failures.push({ name: file.name, maxMegabytes: Math.floor(maxBytes / (1024 * 1024)) });
							continue;
						}
						try {
							const bytes = new Uint8Array(await file.arrayBuffer());
							const saved = await save({ name: file.name, bytes });
							if (saved) paths.push(saved);
						} catch (error) {
							console.warn("Unable to attach dropped file", error);
							const reason = error instanceof Error ? error.message.trim() : "";
							failures.push({ name: file.name, ...(reason ? { reason } : {}) });
						}
					}
				} finally {
					dropNoticeRef.current.end(drop, failures);
				}
				if (paths.length === 0) return;
				pasteText(droppedPathsText(paths, Boolean(sandbox)));
			})();
		};
		shell.addEventListener("dragover", dragOverInput);
		shell.addEventListener("drop", dropInput);

		const showLatestOutput = () => {
			term.scrollToBottom();
			const viewport = host.querySelector<HTMLElement>(".xterm-viewport");
			if (!viewport) return;
			viewport.scrollTop = Math.max(0, viewport.scrollHeight - viewport.clientHeight);
			scheduleScrollbarUpdate();
		};

		let cancelActivationPreparation: (() => void) | null = null;
		const prepareForActivation = (): Promise<void> => {
			cancelActivationPreparation?.();
			webglLease.activate();
			return new Promise((resolve) => {
				let firstFrame: number | null = null;
				let paintFrame: number | null = null;
				let finished = false;
				const finish = () => {
					if (finished) return;
					finished = true;
					if (firstFrame !== null) cancelAnimationFrame(firstFrame);
					if (paintFrame !== null) cancelAnimationFrame(paintFrame);
					if (cancelActivationPreparation === finish) cancelActivationPreparation = null;
					resolve();
				};
				cancelActivationPreparation = finish;

				const finishAcrossPaintFrames = () => {
					if (finished) return;
					showLatestOutput();
					firstFrame = requestAnimationFrame(() => {
						firstFrame = null;
						showLatestOutput();
						paintFrame = requestAnimationFrame(() => {
							paintFrame = null;
							finish();
						});
					});
				};
				scheduleStableFit(true, finishAcrossPaintFrames);
			});
		};

		const handle: AttachableTerminal = {
			get cols() {
				return term.cols;
			},
			get rows() {
				return term.rows;
			},
			get hasMeasuredGrid() {
				return gridMeasuredRef.current;
			},
			write: (data, done, source = "live") => {
				let hasEsc = false;
				for (let i = 0; i < data.length; i++) {
					if (data[i] === 0x1b) {
						hasEsc = true;
						break;
					}
				}
				if (source === "replay") {
					cursorPositionForwarder.dispose();
				}
				if (hasEsc || cursorPositionForwarder.hasPartialRequest()) {
					const chunk = new TextDecoder().decode(data);
					if (source === "live") cursorPositionForwarder.observeOutput(chunk);
					if (hasEsc) {
						const reply = callbacksRef.current.supportsCursorColorScheme
							? cursorColorSchemeReplyForOutput(chunk, callbacksRef.current.theme)
							: null;
						if (reply) {
							announcedCursorSchemeRef.current = null;
							notifyCursorScheme(callbacksRef.current.theme, true, true);
						}
					}
				}
				const outputResizeGeneration = resizeGeneration;
				if (source === "replay") pendingReplayWrites++;
				term.write(data, () => {
					if (source === "replay") pendingReplayWrites--;
					else didParseResizeOutput(outputResizeGeneration);
					scheduleScrollbarUpdate();
					done?.();
				});
			},
			writeln: (line) => {
				const outputResizeGeneration = resizeGeneration;
				term.writeln(line, () => {
					didParseResizeOutput(outputResizeGeneration);
					scheduleScrollbarUpdate();
				});
			},
			showLatestOutput,
			prepareForActivation,
			requestActivationFocus: () => {
				window.setTimeout(() => {
					const host = hostRef.current;
					if (
						!host ||
						callbacksRef.current.isVisible === false ||
						callbacksRef.current.focusRequested === false ||
						!canAutoFocusTerminal(host)
					) return;
					focusTerminal();
				}, 0);
			},
			notifyCursorColorScheme: () => {
				if (callbacksRef.current.supportsCursorColorScheme) {
					notifyCursorScheme(callbacksRef.current.theme, false, true);
				}
			},
			sendUserInput: (data, source = "shortcut") => emitUserInput(data, source),
			onUserInput: (listener) => {
				userInputListeners.add(listener);
				return { dispose: () => userInputListeners.delete(listener) };
			},
			onResize: (listener) => term.onResize(listener),
		};
		callbacksRef.current.onReady?.(handle);

		return () => {
			disposed = true;
			webglLease.release();
			webglLeaseRef.current = null;
			if (reportedFocused) aoBridge.terminal.setFocused(false);
			disposeFontSizeShortcut();
			host.removeEventListener("focusin", handleFocusIn);
			host.removeEventListener("focusout", handleFocusOut);
			delete (host as DevXtermHost).__aoXtermForTest;
			termRef.current = null;
			if (searchAddonRef.current === searchAddon) searchAddonRef.current = null;
			fitRef.current = null;
			cancelAnimationFrame(raf);
			if (liveFitFrame !== null) cancelAnimationFrame(liveFitFrame);
			for (const timer of settleTimers) window.clearTimeout(timer);
			if (fitQuietTimer !== null) clearTimeout(fitQuietTimer);
			if (fitCapTimer !== null) clearTimeout(fitCapTimer);
			fitSettledListeners.clear();
			observer.disconnect();
			document.removeEventListener("transitionend", settleAfterSlide);
			dragStateObserver.disconnect();
			synchronizedFrames.dispose();
			synchronizedCapability.dispose();
			paintedResizeFrame.dispose();
			host.removeEventListener("wheel", cancelResizeCover);
			host.removeEventListener("keydown", cancelResizeCover);
			host.removeEventListener("pointerdown", cancelResizeCover);
			resizeCover.remove();
			stabilizer.dispose();
			scrollPositionChange?.dispose();
			scrollbarResize?.dispose();
			if (scrollbarFrame !== null) cancelAnimationFrame(scrollbarFrame);
			if (scrollbarHideTimer !== null) window.clearTimeout(scrollbarHideTimer);
			scrollbarTrack?.removeEventListener("pointerdown", scrollbarPointerDown);
			scrollbarTrack?.removeEventListener("pointermove", scrollbarPointerMove);
			scrollbarTrack?.removeEventListener("pointerup", scrollbarPointerUp);
			scrollbarTrack?.removeEventListener("pointercancel", scrollbarPointerUp);
			window.removeEventListener("resize", scheduleLiveFit);
			shell.removeEventListener("copy", copyInput);
			window.removeEventListener("keydown", copyShortcut, true);
			host.removeEventListener("pointerdown", pointerDown);
			document.removeEventListener("pointermove", pointerMove);
			document.removeEventListener("pointerup", pointerUp);
			document.removeEventListener("pointercancel", disarmPointerSelection);
			window.removeEventListener("blur", disarmPointerSelection);
			shell.removeEventListener("contextmenu", openContextMenu);
			shell.removeEventListener("paste", pasteInput, true);
			shell.removeEventListener("compositionend", compositionInput, true);
			shell.removeEventListener("dragover", dragOverInput);
			shell.removeEventListener("drop", dropInput);
			contextMenuActionsRef.current = null;
			visibleContentRender.dispose();
			cancelActivationPreparation?.();
			clearSuppressNativePaste();
			if (colorSchemeReporterRef.current === reportColorScheme) colorSchemeReporterRef.current = null;
			setColorSchemeUpdates.dispose();
			resetColorSchemeUpdates.dispose();
			queryColorSchemeCapability.dispose();
			queryColorScheme.dispose();
			for (const timer of schemeRetryTimers) window.clearTimeout(timer);
			schemeRetryTimers = [];
			oscColorForwarder.dispose();
			cursorPositionForwarder.dispose();
			protocolInput.dispose();
			binaryMouseInput.dispose();
			osc52Clipboard.dispose();
			for (const type of ["mousedown", "mousemove", "mouseup"] as const) {
				window.removeEventListener(type, noteMouseEvent, true);
			}
			clickModeSubscription();
			keyInput.dispose();
			notifyCursorSchemeRef.current = () => {};
			announcedCursorSchemeRef.current = null;
			userInputListeners.clear();
			window.setTimeout(() => {
				try {
					term.dispose();
				} catch {
				}
			}, 0);
		};
	}, []);

	useEffect(() => {
		if (!props.focusRequested || props.isVisible === false) return undefined;
		let initialFocusTimer: number | null = null;
		let retryFrame: number | null = null;
		let retriesRemaining = AUTOFOCUS_RETRY_FRAMES;
		let cancelled = false;
		const focusIfAllowed = () => {
			if (cancelled) return;
			const host = hostRef.current;
			if (!host || !canAutoFocusTerminal(host)) {
				if (retriesRemaining === 0) return;
				retriesRemaining -= 1;
				retryFrame = requestAnimationFrame(() => {
					retryFrame = null;
					focusIfAllowed();
				});
				return;
			}
			focusTerminal();
		};

		const initialHost = hostRef.current;
		if (initialHost && canAutoFocusTerminal(initialHost)) {
			initialFocusTimer = window.setTimeout(() => {
				initialFocusTimer = null;
				focusIfAllowed();
			}, 0);
		} else {
			focusIfAllowed();
		}

		return () => {
			cancelled = true;
			if (initialFocusTimer !== null) window.clearTimeout(initialFocusTimer);
			if (retryFrame !== null) cancelAnimationFrame(retryFrame);
		};
	}, [focusTerminal, props.focusRequested, props.isVisible]);

	useLayoutEffect(() => {
		if (props.isVisible === false) {
			setSearchOpen(false);
			searchAddonRef.current?.clearDecorations();
			setContextMenuOpen(false);
			setCopiedToast(false);
			if (copiedToastTimerRef.current !== undefined) {
				window.clearTimeout(copiedToastTimerRef.current);
				copiedToastTimerRef.current = undefined;
			}
			cancelPendingFocusRestore();
		}
	}, [props.isVisible, setContextMenuOpen, cancelPendingFocusRestore]);

	useEffect(() => cancelPendingFocusRestore, [cancelPendingFocusRestore]);

	const wasVisibleRef = useRef(props.isVisible !== false);
	useEffect(() => {
		const visible = props.isVisible !== false;
		const becameVisible = visible && !wasVisibleRef.current;
		wasVisibleRef.current = visible;
		if (!becameVisible) return;
		webglLeaseRef.current?.activate();
		const term = termRef.current;
		if (term && gridMeasuredRef.current) callbacksRef.current.onVisibleSize?.(term.cols, term.rows);
	}, [props.isVisible]);

	const fullscreenElement = document.fullscreenElement;
	const contextMenuPortalContainer =
		props.isFullscreen &&
		fullscreenElement instanceof HTMLElement &&
		hostRef.current &&
		fullscreenElement.contains(hostRef.current)
			? fullscreenElement
			: undefined;

	return (
		<>
			<div
				ref={shellRef}
				aria-label={props.ariaLabel}
				className={props.className}
				style={{
					height: "100%",
					overflow: "hidden",
					position: "relative",
					width: "100%",
				}}
			>
				<div
					ref={hostRef}
					className={macPlatform ? "terminal-xterm-host terminal-xterm-host--mac" : "terminal-xterm-host"}
					style={{
						backgroundColor: "var(--color-bg-terminal-opaque)",
						position: "relative",
						contain: "paint",
						minWidth: 0,
						height: "100%",
						overflow: "hidden",
						width: "100%",
					}}
				/>
				{macPlatform ? (
					<div
						aria-hidden="true"
						className="terminal-scrollbar"
						data-active="false"
						data-scrollable="false"
						ref={scrollbarTrackRef}
					>
						<div className="terminal-scrollbar__thumb" ref={scrollbarThumbRef} />
					</div>
				) : null}
				<TerminalSearch
					onClose={() => setSearchOpen(false)}
					onReturnFocus={focusTerminal}
					open={searchOpen && props.isVisible !== false}
					searchAddon={searchAddonRef.current}
				/>
				{dropNotice && props.isVisible !== false ? (
					dropNotice.kind === "placing" ? (
						<div
							aria-live="polite"
							className="pointer-events-none absolute left-1/2 top-3 z-10 flex max-w-[calc(100%-1.5rem)] -translate-x-1/2 items-center gap-2 rounded-md border border-(--color-border-overlay) bg-[var(--color-bg-import-modal)] px-3 py-1.5 text-xs text-[var(--color-text-import-title)] shadow-[var(--shadow-import-modal)]"
							role="status"
						>
							<LoaderCircle aria-hidden="true" className="size-3.5 shrink-0 motion-safe:animate-spin" />
							<span className="truncate">
								{dropNotice.names.length === 1
									? t("terminal.drop.attachingOne", { name: dropNotice.names[0] })
									: t("terminal.drop.attachingMany", { number: dropNotice.names.length })}
							</span>
						</div>
					) : (
						<div
							className="absolute left-1/2 top-3 z-10 flex w-max max-w-[min(36rem,calc(100%-1.5rem))] -translate-x-1/2 items-start gap-2 rounded-md border border-(--color-border-overlay) bg-[var(--color-bg-import-modal)] py-1.5 pl-3 pr-1.5 text-xs text-[var(--color-text-import-title)] shadow-[var(--shadow-import-modal)]"
							role="alert"
						>
							<TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-error" />
							<ul className="min-w-0 space-y-0.5">
								{dropNotice.failures.map((failure, index) => (
									<li key={`${failure.name}-${index}`} className="break-words">
										{t("terminal.drop.failed", {
											name: failure.name,
											reason: failure.maxMegabytes !== undefined
												? t("terminal.drop.tooLarge", { size: failure.maxMegabytes })
												: failure.reason ?? t("terminal.drop.unknownReason"),
										})}
									</li>
								))}
							</ul>
							<CloseButton
								aria-label={t("terminal.drop.dismiss")}
								onClick={() => {
									if (dropNoticeTimerRef.current !== undefined) window.clearTimeout(dropNoticeTimerRef.current);
									dropNoticeTimerRef.current = undefined;
									setDropNotice(null);
								}}
								size="icon-xs"
							/>
						</div>
					)
				) : null}
				{selectHint && props.isVisible !== false ? (
					<div
						aria-live="polite"
						className="absolute left-1/2 top-3 z-10 flex w-max max-w-[min(36rem,calc(100%-1.5rem))] -translate-x-1/2 items-center gap-2 rounded-md border border-(--color-border-overlay) bg-[var(--color-bg-import-modal)] py-1.5 pl-3 pr-1.5 text-xs text-[var(--color-text-import-title)] shadow-[var(--shadow-import-modal)]"
						data-testid="terminal-select-hint"
						role="status"
					>
						<span className="min-w-0">{macPlatform ? t("terminal.selectHint.mac") : t("terminal.selectHint.other")}</span>
						<CloseButton
							aria-label={t("terminal.selectHint.dismiss")}
							onClick={() => hideSelectHintRef.current()}
							size="icon-xs"
						/>
					</div>
				) : null}
				{copiedToast && props.isVisible !== false ? (
					<div
						aria-live="polite"
						className="pointer-events-none absolute bottom-3 left-1/2 z-10 -translate-x-1/2 rounded-md border border-(--color-border-overlay) bg-[var(--color-bg-import-modal)] px-3 py-1.5 text-xs text-[var(--color-text-import-title)] shadow-[var(--shadow-import-modal)]"
						role="status"
					>
						{t("terminal.copiedToClipboard")}
					</div>
				) : null}
			</div>
			<DropdownMenu modal={false} open={contextMenu.open} onOpenChange={setContextMenuOpen}>
				<DropdownMenuTrigger asChild>
					<Pressable
						aria-hidden="true"
						tabIndex={-1}
						style={{
							border: 0,
							height: 0,
							left: contextMenu.x,
							opacity: 0,
							padding: 0,
							pointerEvents: "none",
							position: "fixed",
							top: contextMenu.y,
							width: 0,
						}}
					/>
				</DropdownMenuTrigger>
				<DropdownMenuContent
					align="start"
					className="min-w-36"
					onCloseAutoFocus={(event) => event.preventDefault()}
					portalContainer={contextMenuPortalContainer}
					side="right"
					sideOffset={2}
				>
					{contextMenu.link ? (
						<>
							<DropdownMenuItem disabled={!props.onLinkOpen} onSelect={() => {
								const { link } = contextMenu;
								setContextMenuOpen(false);
								if (link) props.onLinkOpen?.(link);
							}}>
								{t("link.openInAOBrowser")}
							</DropdownMenuItem>
							<DropdownMenuItem
								onSelect={() => {
									const { link } = contextMenu;
									setContextMenuOpen(false);
									if (link) void aoBridge.app.openExternal(link);
								}}
							>
								{t("link.openInExternalBrowser")}
							</DropdownMenuItem>
							<DropdownMenuSeparator />
							<DropdownMenuItem onSelect={() => {
								const { link } = contextMenu;
								setContextMenuOpen(false);
								if (link) {
									markLocalTerminalCopy();
									void aoBridge.clipboard.writeText(link);
								}
							}}>
								{t("link.copy")}
							</DropdownMenuItem>
							<DropdownMenuSeparator />
						</>
					) : null}
					<DropdownMenuItem disabled={!contextMenu.canCopy} onSelect={() => runContextMenuAction("copy")}>
						{t("titlebar.copy")}
					</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => runContextMenuAction("paste")}>{t("titlebar.paste")}</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => runContextMenuAction("selectAll")}>{t("titlebar.selectAll")}</DropdownMenuItem>
					<DropdownMenuSeparator />
					<DropdownMenuItem
						onSelect={() => {
							setContextMenuOpen(false);
							setSearchOpen(true);
						}}
					>
						{t("terminal.search")}
					</DropdownMenuItem>
					{props.onToggleFullscreen ? (
						<DropdownMenuItem
							onSelect={() => {
								setContextMenuOpen(false);
								void toggleFullscreenAndRestoreFocus();
							}}
						>
							{props.isFullscreen ? t("terminal.exitFullscreen") : t("terminal.fullscreen")}
						</DropdownMenuItem>
					) : null}
				</DropdownMenuContent>
			</DropdownMenu>
			<HoverCard
				open={linkPreview !== null}
				onOpenChange={(open) => {
					if (!open) linkPreviewControlsRef.current.hide(true);
				}}
			>
				<HoverCardTrigger asChild>
					<Pressable
						aria-hidden="true"
						tabIndex={-1}
						style={{
							border: 0,
							height: 0,
							left: linkPreview?.x ?? 0,
							opacity: 0,
							padding: 0,
							pointerEvents: "none",
							position: "fixed",
							top: linkPreview?.y ?? 0,
							width: 0,
						}}
					/>
				</HoverCardTrigger>
				{linkPreview ? (
					<LinkPreviewHoverCard
						url={linkPreview.url}
						query={linkPreviewQuery}
						onBrowserOpen={props.onLinkOpen}
						portalContainer={contextMenuPortalContainer}
						onPointerEnter={() => linkPreviewControlsRef.current.cancelHide()}
						onPointerLeave={() => linkPreviewControlsRef.current.hide()}
					/>
				) : null}
			</HoverCard>
		</>
	);
}
