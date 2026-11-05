import { Reorder } from "motion/react";
import {
	useCallback,
	useRef,
	useState,
	type KeyboardEvent as ReactKeyboardEvent,
	type PointerEvent as ReactPointerEvent,
	type ReactNode,
	type RefObject,
} from "react";
import { useTranslation } from "react-i18next";
import { useTabListTabStop } from "../hooks/useTabListTabStop";
import { handleTerminalTabListKeyDown } from "../lib/terminal-tabs";
import { clampTerminalFontSize, initialTerminalFontSize, terminalFontSizeStorageKey } from "../lib/terminal-font-size";
import {
	clampSplitSize,
	dividerOffset,
	isVerticalSplit,
	paneBox,
	SPLIT_SIZE_VAR,
	type SplitLayout,
	type SplitSide,
} from "../lib/pane-split";
import type { Theme } from "../stores/ui-store";
import type { TerminalTarget } from "../types/terminal";
import type { WorkspaceSession } from "../types/workspace";
import { DraggableWorkspaceTab } from "./CenterPane";
import { TerminalPane } from "./TerminalPane";

export type SplitSideTab = {
	key: string;
	/** The tab chrome, already wired to select and close in the side pane. */
	tab: ReactNode;
	/**
	 * What the pane shows while the tab is active. Files leave it empty: their
	 * views stay where SessionView mounts them and are placed over this pane.
	 */
	body?: ReactNode;
};

/** Height of the side pane's tab strip; file layers are placed below it. */
export const SPLIT_SIDE_STRIP_HEIGHT = "var(--size-toolbar)";

/**
 * The split's side pane: a tab strip of its own above the active tab's view.
 * Its tabs reorder in place and drag out like the main strip's.
 */
export function SplitSidePane({
	layout,
	tabs,
	onReorder,
}: {
	layout: SplitLayout;
	tabs: SplitSideTab[];
	onReorder: (keys: string[]) => void;
}) {
	const { t } = useTranslation();
	const tabListRef = useTabListTabStop<HTMLDivElement>();
	const active = tabs.find((tab) => tab.key === layout.active);
	return (
		<section
			aria-label={t("splitPane.label")}
			className="pane-split-enter flex min-h-0 min-w-0 flex-col overflow-hidden bg-background"
			data-split-side={layout.side}
			data-testid="split-side-pane"
			style={paneBox("side", layout.side)}
		>
			<div
				className="flex shrink-0 items-stretch overflow-hidden border-b border-border bg-sidebar"
				style={{ height: SPLIT_SIDE_STRIP_HEIGHT }}
			>
				<div
					aria-label={t("terminal.tabsAria")}
					className="scrollbar-none flex h-full min-w-0 flex-1 items-stretch overflow-x-auto"
					data-tab-scroll-region
					onKeyDown={handleTerminalTabListKeyDown}
					ref={tabListRef}
					role="tablist"
				>
					<Reorder.Group
						as="div"
						axis="x"
						className="flex w-max items-stretch self-stretch"
						onReorder={onReorder}
						values={tabs.map((tab) => tab.key)}
					>
						{tabs.map((tab) => (
							<DraggableWorkspaceTab key={tab.key} value={tab.key}>
								{tab.tab}
							</DraggableWorkspaceTab>
						))}
					</Reorder.Group>
				</div>
			</div>
			<div className="relative min-h-0 flex-1" role="tabpanel">
				{active?.body ?? null}
			</div>
		</section>
	);
}

/**
 * A terminal shown in the side pane. Shell terminals come from the shared
 * cache, so moving one here re-parents the live xterm instead of remounting.
 */
export function SplitSideTerminal({
	session,
	target,
	theme,
	daemonReady,
	createMux,
}: {
	session?: WorkspaceSession;
	target: TerminalTarget;
	theme: Theme;
	daemonReady: boolean;
	createMux?: Parameters<typeof TerminalPane>[0]["createMux"];
}) {
	const [fontSize, setFontSize] = useState(initialTerminalFontSize);
	const changeFontSize = useCallback((delta: number) => {
		setFontSize((current) => {
			const next = clampTerminalFontSize(current + delta);
			window.localStorage?.setItem(terminalFontSizeStorageKey, String(next));
			return next;
		});
	}, []);
	return (
		<div className="h-full min-h-0" data-testid="split-side-terminal">
			<TerminalPane
				createMux={createMux}
				daemonReady={daemonReady}
				focusRequested
				fontSize={fontSize}
				onChangeFontSize={changeFontSize}
				session={session}
				terminalTarget={target}
				theme={theme}
			/>
		</div>
	);
}

const KEYBOARD_STEP = 0.05;

/**
 * The boundary between the panes. Dragging rewrites only the size variable on
 * the split area, so neither pane re-renders until the drag ends; terminals
 * refit through their own resize observers and settle once it ends.
 */
export function SplitDivider({
	areaRef,
	side,
	size,
	onResize,
}: {
	areaRef: RefObject<HTMLElement | null>;
	side: SplitSide;
	size: number;
	onResize: (size: number) => void;
}) {
	const { t } = useTranslation();
	const vertical = isVerticalSplit(side);
	const dragRef = useRef<{ frame: number | null; size: number } | null>(null);

	const sizeAt = useCallback(
		(x: number, y: number) => {
			const rect = areaRef.current?.getBoundingClientRect();
			if (!rect || rect.width <= 0 || rect.height <= 0) return size;
			const fraction =
				side === "right" ? (rect.right - x) / rect.width
				: side === "left" ? (x - rect.left) / rect.width
				: side === "bottom" ? (rect.bottom - y) / rect.height
				: (y - rect.top) / rect.height;
			return clampSplitSize(fraction);
		},
		[areaRef, side, size],
	);

	const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
		if (event.button !== 0) return;
		event.preventDefault();
		event.currentTarget.setPointerCapture(event.pointerId);
		dragRef.current = { frame: null, size };
		// Shared with the shell resize grips: terminals do their final fit when
		// the class goes away.
		document.body.classList.add("is-resizing-x");
		if (vertical) document.body.style.cursor = "row-resize";
	};
	const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
		const drag = dragRef.current;
		if (!drag) return;
		drag.size = sizeAt(event.clientX, event.clientY);
		if (drag.frame !== null) return;
		drag.frame = requestAnimationFrame(() => {
			drag.frame = null;
			areaRef.current?.style.setProperty(SPLIT_SIZE_VAR, String(drag.size));
		});
	};
	const endDrag = () => {
		const drag = dragRef.current;
		if (!drag) return;
		dragRef.current = null;
		if (drag.frame !== null) cancelAnimationFrame(drag.frame);
		areaRef.current?.style.setProperty(SPLIT_SIZE_VAR, String(drag.size));
		document.body.classList.remove("is-resizing-x");
		document.body.style.cursor = "";
		onResize(drag.size);
	};
	const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
		const grow = side === "right" || side === "bottom" ? -1 : 1;
		const keys = vertical ? { ArrowUp: grow, ArrowDown: -grow } : { ArrowLeft: grow, ArrowRight: -grow };
		const direction = keys[event.key as keyof typeof keys];
		if (direction === undefined) return;
		event.preventDefault();
		onResize(clampSplitSize(size + direction * KEYBOARD_STEP));
	};

	return (
		<div
			aria-label={t("splitPane.resize")}
			aria-orientation={vertical ? "horizontal" : "vertical"}
			aria-valuemax={80}
			aria-valuemin={20}
			aria-valuenow={Math.round(size * 100)}
			className="pane-split-divider"
			data-orientation={vertical ? "horizontal" : "vertical"}
			data-testid="split-divider"
			onDoubleClick={() => onResize(0.5)}
			onKeyDown={onKeyDown}
			onLostPointerCapture={endDrag}
			onPointerCancel={endDrag}
			onPointerDown={onPointerDown}
			onPointerMove={onPointerMove}
			onPointerUp={endDrag}
			role="separator"
			style={vertical ? { top: dividerOffset(side) } : { left: dividerOffset(side) }}
			tabIndex={0}
		/>
	);
}
