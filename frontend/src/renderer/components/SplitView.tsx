import {
	Fragment,
	useCallback,
	useRef,
	useState,
	type KeyboardEvent as ReactKeyboardEvent,
	type PointerEvent as ReactPointerEvent,
	type ReactNode,
	type RefObject,
} from "react";
import { useTranslation } from "react-i18next";
import { beginPaneTabDrag } from "../lib/pane-drag";
import { MIN_PANE_PX, ratioVar, type CssDivider } from "../lib/pane-split";
import { clampTerminalFontSize, initialTerminalFontSize, terminalFontSizeStorageKey } from "../lib/terminal-font-size";
import { cn } from "../lib/utils";
import type { Theme } from "../stores/ui-store";
import type { TerminalTarget } from "../types/terminal";
import type { WorkspaceSession } from "../types/workspace";
import { TerminalPane } from "./TerminalPane";
import { Pressable } from "./ui/pressable";

/**
 * A terminal shown in a split pane. Shell terminals come from the shared
 * cache, so moving one into or around a split re-parents the live xterm
 * instead of remounting it.
 */
export function SplitPaneTerminal({
	session,
	target,
	theme,
	daemonReady,
	createMux,
	focusRequested,
}: {
	session?: WorkspaceSession;
	target: TerminalTarget;
	theme: Theme;
	daemonReady: boolean;
	createMux?: Parameters<typeof TerminalPane>[0]["createMux"];
	focusRequested: boolean;
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
		<TerminalPane
			createMux={createMux}
			daemonReady={daemonReady}
			focusRequested={focusRequested}
			fontSize={fontSize}
			onChangeFontSize={changeFontSize}
			session={session}
			terminalTarget={target}
			theme={theme}
		/>
	);
}

const KEYBOARD_STEP = 0.05;

/**
 * The boundary between two panes of a split. Dragging rewrites only that
 * split's ratio variable on the screen element, so no pane re-renders until
 * the drag ends; terminals refit through their own resize observers.
 */
export function SplitDivider({
	screenRef,
	divider,
	onResize,
}: {
	screenRef: RefObject<HTMLElement | null>;
	divider: CssDivider;
	onResize: (path: string, ratio: number) => void;
}) {
	const { t } = useTranslation();
	const row = divider.dir === "row";
	const dragRef = useRef<{ frame: number | null; ratio: number } | null>(null);
	const ratioNow = () => {
		const value = Number.parseFloat(screenRef.current?.style.getPropertyValue(ratioVar(divider.path)) ?? "");
		return Number.isFinite(value) ? value : 0.5;
	};
	// The split's own box never depends on its own ratio, so it can be measured
	// once per move from the screen and the layout's fractions.
	const clampRatio = (ratio: number) => {
		const rect = screenRef.current?.getBoundingClientRect();
		const span = rect ? (row ? divider.box.w * rect.width : divider.box.h * rect.height) : 0;
		const min = span > 0 ? Math.min(0.45, MIN_PANE_PX / span) : 0.1;
		return Math.min(1 - min, Math.max(min, ratio));
	};
	const ratioAt = (x: number, y: number) => {
		const rect = screenRef.current?.getBoundingClientRect();
		if (!rect || rect.width <= 0 || rect.height <= 0) return ratioNow();
		const ratio = row
			? (x - rect.left - divider.box.x * rect.width) / (divider.box.w * rect.width)
			: (y - rect.top - divider.box.y * rect.height) / (divider.box.h * rect.height);
		return clampRatio(ratio);
	};
	const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
		if (event.button !== 0) return;
		event.preventDefault();
		event.currentTarget.setPointerCapture(event.pointerId);
		dragRef.current = { frame: null, ratio: ratioNow() };
		// Shared with the shell resize grips: terminals do their final fit when
		// the class goes away.
		document.body.classList.add("is-resizing-x");
		if (!row) document.body.style.cursor = "row-resize";
	};
	const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
		const drag = dragRef.current;
		if (!drag) return;
		drag.ratio = ratioAt(event.clientX, event.clientY);
		if (drag.frame !== null) return;
		drag.frame = requestAnimationFrame(() => {
			drag.frame = null;
			screenRef.current?.style.setProperty(ratioVar(divider.path), String(drag.ratio));
		});
	};
	const endDrag = () => {
		const drag = dragRef.current;
		if (!drag) return;
		dragRef.current = null;
		if (drag.frame !== null) cancelAnimationFrame(drag.frame);
		screenRef.current?.style.setProperty(ratioVar(divider.path), String(drag.ratio));
		document.body.classList.remove("is-resizing-x");
		document.body.style.cursor = "";
		onResize(divider.path, drag.ratio);
	};
	const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
		const keys = row ? { ArrowLeft: -1, ArrowRight: 1 } : { ArrowUp: -1, ArrowDown: 1 };
		const direction = keys[event.key as keyof typeof keys];
		if (direction === undefined) return;
		event.preventDefault();
		onResize(divider.path, clampRatio(ratioNow() + direction * KEYBOARD_STEP));
	};
	const ratio = ratioNow();
	return (
		<div
			aria-label={t("splitPane.resize")}
			aria-orientation={row ? "vertical" : "horizontal"}
			aria-valuemax={100}
			aria-valuemin={0}
			aria-valuenow={Math.round(ratio * 100)}
			className="pane-split-divider"
			data-orientation={row ? "vertical" : "horizontal"}
			data-testid="split-divider"
			onDoubleClick={() => onResize(divider.path, 0.5)}
			onKeyDown={onKeyDown}
			onLostPointerCapture={endDrag}
			onPointerCancel={endDrag}
			onPointerDown={onPointerDown}
			onPointerMove={onPointerMove}
			onPointerUp={endDrag}
			role="separator"
			style={row
				? { left: divider.style.left, top: divider.style.top, height: divider.style.height }
				: { left: divider.style.left, top: divider.style.top, width: divider.style.width }}
			tabIndex={0}
		/>
	);
}

export type SplitGroupMember = {
	key: string;
	label: string;
	icon: ReactNode;
	onSelect: () => void;
};

/**
 * One strip tab for tabs shown together. Each member's name is its own
 * target: clicking it shows the group with that pane focused, and dragging it
 * moves it within the split or out of the group.
 */
export function SplitGroupTab({
	active,
	focusedKey,
	members,
}: {
	active: boolean;
	focusedKey?: string;
	members: SplitGroupMember[];
}) {
	const { t } = useTranslation();
	return (
		<span
			aria-label={t("splitPane.group", { names: members.map((member) => member.label).join(", ") })}
			className={cn(
				"group relative inline-flex h-full shrink-0 self-stretch items-stretch border-r border-border",
				active ? "bg-overlay text-foreground" : "text-passive hover:bg-raised",
			)}
			data-split-group-tab
			data-terminal-tab-frame
			role="group"
		>
			{members.map((member, index) => {
				const focused = active && member.key === focusedKey;
				return (
					<Fragment key={member.key}>
						{index > 0 ? (
							<span aria-hidden="true" className="my-2.5 w-px shrink-0 self-stretch bg-border" />
						) : null}
						<Pressable
							aria-selected={focused}
							className={cn(
								"inline-flex h-full min-w-0 max-w-shell-tab-max cursor-pointer items-center gap-2 px-2 text-control leading-none",
								focused ? "text-foreground" : active ? "text-muted-foreground hover:text-foreground" : "hover:text-foreground",
							)}
							data-split-member={member.key}
							onClick={(event) => {
								if (event.detail > 1) return;
								member.onSelect();
							}}
							onPointerDown={(event) => {
								// A member drags on its own; the group tab itself stays in place.
								event.stopPropagation();
								beginPaneTabDrag(event, member.key, event.currentTarget);
							}}
							role="tab"
							tabIndex={focused ? 0 : -1}
							title={member.label}
							type="button"
						>
							<span className="pointer-events-none grid size-icon-base shrink-0 place-items-center">{member.icon}</span>
							<span className="truncate">{member.label}</span>
						</Pressable>
					</Fragment>
				);
			})}
			{active ? (
				<span
					aria-hidden="true"
					className="pointer-events-none absolute inset-x-0 bottom-0 h-0.5 bg-foreground/80"
					data-testid="active-terminal-tab-indicator"
				/>
			) : null}
		</span>
	);
}
