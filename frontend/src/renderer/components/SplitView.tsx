import {
	Fragment,
	useCallback,
	useEffect,
	useLayoutEffect,
	useRef,
	useState,
	type KeyboardEvent as ReactKeyboardEvent,
	type PointerEvent as ReactPointerEvent,
	type ReactNode,
	type RefObject,
} from "react";
import { agentLabel } from "@aoagents/product-ui";
import { createPortal } from "react-dom";
import { Ungroup, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { beginPaneTabDrag } from "../lib/pane-drag";
import { MIN_PANE_PX, ratioVar, type Box, type CssDivider } from "../lib/pane-split";
import { clampTerminalFontSize, initialTerminalFontSize, terminalFontSizeStorageKey } from "../lib/terminal-font-size";
import { cn } from "../lib/utils";
import type { Theme } from "../stores/ui-store";
import type { TerminalTarget } from "../types/terminal";
import type { WorkspaceSession } from "../types/workspace";
import { useShellAgentStore, useShellAgentStream } from "../stores/shell-agent-store";
import { AgentAvatar } from "./AgentAvatar";
import { ShellAgentStatus } from "./ShellTerminalTab";
import { TerminalPane } from "./TerminalPane";
import { Pressable } from "./ui/pressable";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "./ui/context-menu";
import { terminalTabActionClassName } from "./TerminalTabFrame";

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

/**
 * The outline that flashes on the pane focus moved to. It is drawn over the
 * whole window (the center column clips at its own edge, just inside the
 * panel's frame), so on the frame's sides it can sit exactly on the panel's
 * border, with the frame's rounding at the frame's corners; edges beside a
 * divider follow the pane. Measured once per appearance: it is remounted on
 * every focus change and lasts only a moment.
 */
export function SplitFocusOutline({ screenRef, box }: { screenRef: RefObject<HTMLElement | null>; box: Box }) {
	const ref = useRef<HTMLDivElement>(null);
	const { x, y, w, h } = box;
	useLayoutEffect(() => {
		const outline = ref.current;
		const screen = screenRef.current;
		if (!outline || !screen) return;
		const screenBox = screen.getBoundingClientRect();
		let left = screenBox.left + x * screenBox.width;
		let top = screenBox.top + y * screenBox.height;
		let right = left + w * screenBox.width;
		let bottom = top + h * screenBox.height;
		const edge = 0.001;
		const at = { top: y < edge, bottom: y + h > 1 - edge, left: x < edge, right: x + w > 1 - edge };
		const surface = screen.closest<HTMLElement>(".center-panel-surface");
		let frameStyle: CSSStyleDeclaration | null = null;
		if (surface) {
			// The panel's rounded frame is the surface's own border, or, under
			// the floating top bar, an ::after layer over a borderless surface.
			const own = getComputedStyle(surface);
			const after = getComputedStyle(surface, "::after");
			const framed = (Number.parseFloat(own.borderBottomRightRadius) || 0) === 0
				&& (Number.parseFloat(after.borderBottomRightRadius) || 0) > 0;
			frameStyle = framed ? after : own;
			const frame = surface.getBoundingClientRect();
			const frameTop = frame.top + (framed ? Number.parseFloat(after.top) || 0 : 0);
			if (at.left) left = frame.left;
			if (at.right) right = frame.right;
			if (at.top) top = frameTop;
			if (at.bottom) bottom = frame.bottom;
		}
		outline.style.left = `${left}px`;
		outline.style.top = `${top}px`;
		outline.style.width = `${Math.max(0, right - left)}px`;
		outline.style.height = `${Math.max(0, bottom - top)}px`;
		const radius = (corner: string, vertical: "top" | "bottom", horizontal: "left" | "right") =>
			frameStyle && at[vertical] && at[horizontal] ? frameStyle.getPropertyValue(`border-${corner}-radius`) || "0px" : "0px";
		outline.style.borderTopLeftRadius = radius("top-left", "top", "left");
		outline.style.borderTopRightRadius = radius("top-right", "top", "right");
		outline.style.borderBottomLeftRadius = radius("bottom-left", "bottom", "left");
		outline.style.borderBottomRightRadius = radius("bottom-right", "bottom", "right");
	}, [screenRef, x, y, w, h]);
	return createPortal(<div aria-hidden="true" className="pane-split-focus" ref={ref} />, document.body);
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
	useEffect(() => () => {
		const drag = dragRef.current;
		if (!drag) return;
		dragRef.current = null;
		if (drag.frame !== null) cancelAnimationFrame(drag.frame);
		document.body.classList.remove("is-resizing-x");
		document.body.style.cursor = "";
	}, []);
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
	return (
		<div
			aria-label={t("splitPane.resize")}
			aria-orientation={row ? "vertical" : "horizontal"}
			aria-valuemax={100}
			aria-valuemin={0}
			aria-valuenow={Math.round(divider.ratio * 100)}
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
	/** Closes the member's pane (a member that cannot close leaves the group). */
	onClose: () => void;
	/** A shell whose running agent should show, as on its strip tab. */
	agentHandleId?: string;
};

/**
 * One strip tab for tabs shown together. The focused member shows its name;
 * the others show only their icon, named by a tooltip. Each member is its own
 * target: clicking it shows the group with that pane focused, and dragging it
 * moves it within the split or out of the group. Hovering the tab swaps the
 * focused member's icon for a close button, as on other tabs, and its context
 * menu ungroups it, followed by `menuItems`: the focused member's own tab menu.
 */
export function SplitGroupTab({
	active,
	focusedKey,
	members,
	onUngroup,
	menuItems,
}: {
	active: boolean;
	focusedKey?: string;
	members: SplitGroupMember[];
	onUngroup: () => void;
	menuItems?: ReactNode;
}) {
	const { t } = useTranslation();
	// A shell in a split has no strip tab of its own, so the group tab keeps
	// the agent watch open and shows the agent as that tab would.
	useShellAgentStream(members.some((member) => member.agentHandleId));
	const agents = useShellAgentStore((state) => state.agents);
	const shown = members.map((member) => {
		const agent = member.agentHandleId ? agents[member.agentHandleId] : undefined;
		if (!agent) return { ...member, agent };
		// A default-named shell ("Terminal 3") reads as its agent.
		const label = /^Terminal \d+$/.test(member.label) ? agentLabel(agent.harness) : member.label;
		return { ...member, agent, label, icon: <AgentAvatar className="size-icon-sm!" decorative provider={agent.harness} /> };
	});
	const frame = (
		<span
			aria-label={t("splitPane.group", { names: shown.map((member) => member.label).join(", ") })}
			className={cn(
				"group relative inline-flex h-full shrink-0 self-stretch items-stretch border-r border-border",
				active ? "bg-overlay text-foreground" : "text-passive hover:bg-raised",
			)}
			// The floating top bar draws the selected tab's pill from data-active.
			data-active={active || undefined}
			data-split-group-tab
			data-terminal-tab-frame
			role="group"
		>
			{shown.map((member, index) => {
				const focused = member.key === focusedKey;
				const segment = (
					<Pressable
						aria-label={member.label}
						aria-selected={active && focused}
						className={cn(
							"inline-flex h-full min-w-0 cursor-pointer items-center gap-2 leading-none",
							focused ? "max-w-shell-tab-max px-2 text-control" : "px-1.5",
							active && focused ? "text-foreground" : active ? "text-muted-foreground hover:text-foreground" : "hover:text-foreground",
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
						tabIndex={active && focused ? 0 : -1}
						type="button"
					>
						<span
							className={cn(
								"pointer-events-none grid size-icon-base shrink-0 place-items-center",
								focused && "group-hover:opacity-0 group-has-[:focus-visible]:opacity-0",
							)}
						>
							{member.icon}
						</span>
						{focused ? <span className="truncate">{member.label}</span> : null}
						{focused && member.agent ? <ShellAgentStatus agent={member.agent} done={false} /> : null}
					</Pressable>
				);
				return (
					<Fragment key={member.key}>
						{index > 0 ? (
							<span aria-hidden="true" className="my-2.5 w-px shrink-0 self-stretch bg-border" />
						) : null}
						{focused ? (
							<span className="relative inline-flex min-w-0">
								{segment}
								{/* Takes the icon's slot on hover, like the close button on other tabs. */}
								<span
									className="pointer-events-none absolute left-2 top-1/2 grid size-icon-base -translate-y-1/2 place-items-center opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-has-[:focus-visible]:pointer-events-auto group-has-[:focus-visible]:opacity-100"
									data-terminal-tab-action
								>
									<Pressable
										aria-label={t("splitPane.closePane", { name: member.label })}
										className={terminalTabActionClassName}
										onClick={(event) => {
											event.stopPropagation();
											member.onClose();
										}}
										onPointerDown={(event) => event.stopPropagation()}
										type="button"
									>
										<X aria-hidden="true" className="size-icon-sm" />
									</Pressable>
								</span>
							</span>
						) : (
							<Tooltip>
								<TooltipTrigger asChild>{segment}</TooltipTrigger>
								<TooltipContent side="bottom">{member.label}</TooltipContent>
							</Tooltip>
						)}
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
	return (
		<ContextMenu>
			<ContextMenuTrigger asChild>{frame}</ContextMenuTrigger>
			<ContextMenuContent className={menuItems ? "min-w-56" : "min-w-44"}>
				<ContextMenuItem onSelect={onUngroup}>
					<Ungroup aria-hidden="true" />
					{t("splitPane.ungroup")}
				</ContextMenuItem>
				{menuItems ? (
					<>
						<ContextMenuSeparator />
						{menuItems}
					</>
				) : null}
			</ContextMenuContent>
		</ContextMenu>
	);
}
