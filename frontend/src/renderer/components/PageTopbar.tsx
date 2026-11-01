import { createContext, useContext, useLayoutEffect, useRef, useState, type ComponentProps, type ComponentPropsWithoutRef, type CSSProperties, type ReactNode, type Ref } from "react";
import { createPortal } from "react-dom";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { NotificationCenter } from "./NotificationCenter";
import { sidebarOccupiesLayout, useUiStore } from "../stores/ui-store";

/**
 * The one page topbar. Every center-panel page (board, automations, archive,
 * settings, artifacts, session chrome) renders its header through this module.
 * The panel owns trailing workspace controls; page and session headers reserve
 * their measured width. Pages supply a lead (where am I) and actions.
 *
 * - `PageTopbar` is the page header row (lead + actions).
 * - `SessionTabsTopbar` is the same frame around a session's tab strip.
 */

const usesNativeDragRegion = isMacPlatform() || isLinuxPlatform();
export const dragStyle = usesNativeDragRegion ? ({ WebkitAppRegion: "drag" } as CSSProperties) : undefined;
const noDragStyle = usesNativeDragRegion ? ({ WebkitAppRegion: "no-drag" } as CSSProperties) : undefined;

const TopbarTrailingActionsContext = createContext<HTMLDivElement | null>(null);

/** The panel is the single owner of notification placement and trailing width. */
export function PageTopbarSurface({ children, notifications = false }: { children: ReactNode; notifications?: boolean }) {
	// Global settings replaces the visible page while its routed session stays mounted.
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	const showControls = notifications && !settingsOpen;
	const surfaceRef = useRef<HTMLDivElement>(null);
	const [actionsHost, setActionsHost] = useState<HTMLDivElement | null>(null);
	useLayoutEffect(() => {
		const surface = surfaceRef.current;
		const controls = actionsHost?.parentElement;
		if (!surface || !controls) return;
		const measure = () => surface.style.setProperty("--topbar-trailing-width", `${controls.getBoundingClientRect().width}px`);
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(controls);
		return () => {
			observer.disconnect();
			surface.style.removeProperty("--topbar-trailing-width");
		};
	}, [actionsHost]);

	return (
		<TopbarTrailingActionsContext.Provider value={showControls ? actionsHost : null}>
			<div className={cn("center-panel-surface relative", showControls && "has-topbar-controls")} ref={surfaceRef}>
				{children}
				{showControls ? (
					<PageTopbarActions className="panel-topbar-controls" data-testid="panel-topbar-controls">
						<div className="contents" ref={setActionsHost} />
						<NotificationCenter style={noDragStyle} />
					</PageTopbarActions>
				) : null}
			</div>
		</TopbarTrailingActionsContext.Provider>
	);
}

/** Session controls join the panel's trailing group, immediately before the bell. */
export function PageTopbarTrailingAction({ children }: { children: ReactNode }) {
	const host = useContext(TopbarTrailingActionsContext);
	return host ? createPortal(children, host) : null;
}

// Linux's fixed navigation cluster extends beyond the collapsed sidebar.
// macOS clearance comes from the center-panel-titlebar CSS; Windows has its
// own titlebar above the panel.
function usePageTopbarPaddingLeft() {
	const isSidebarOpen = useUiStore(sidebarOccupiesLayout);
	return !isSidebarOpen && isLinuxPlatform()
		? "calc(var(--size-titlebar-content-offset-linux) - var(--size-center-panel-inline-inset))"
		: "var(--space-4_5)";
}

function PageTopbarFrame({ className, ...props }: ComponentProps<"header">) {
	return <header className={cn("flex h-toolbar w-full shrink-0 items-center", className)} style={dragStyle} {...props} />;
}

export const topbarTitleClass = "text-brand font-semibold tracking-tight leading-none text-foreground whitespace-nowrap";

export function PageTopbar({
	lead,
	actions,
	className,
	...props
}: Omit<ComponentProps<"header">, "children" | "style"> & {
	/** Orientation: the page's title or crumb. */
	lead?: ReactNode;
	/** High-value page actions, right-aligned. */
	actions?: ReactNode;
}) {
	const paddingLeft = usePageTopbarPaddingLeft();
	return (
		<PageTopbarFrame
			className={cn("workspace-topbar-container center-panel-titlebar relative z-chrome gap-3 border-b border-border-strong pr-(--gap-topbar-control)", className)}
			style={{ ...dragStyle, paddingLeft: isMacPlatform() ? undefined : paddingLeft }}
			{...props}
		>
			<div className="flex min-w-0 flex-1 items-center gap-2.5">
				{lead}
			</div>
			<PageTopbarActions>
				{actions}
			</PageTopbarActions>
		</PageTopbarFrame>
	);
}

/** Title with an optional leading icon, the standard lead for a page. */
export function PageTopbarTitle({
	as: Component = "span",
	icon,
	children,
	...props
}: Omit<ComponentPropsWithoutRef<"span">, "className"> & { as?: "span" | "h1"; icon?: ReactNode }) {
	return (
		<Component className={cn(topbarTitleClass, "inline-flex min-w-0 items-center gap-1.5")} {...props}>
			{icon}
			<span className="max-w-content-max truncate">{children}</span>
		</Component>
	);
}

/** Right-aligned action cluster. Controls inside never start a window drag. */
export function PageTopbarActions({ className, children, ...props }: ComponentProps<"div">) {
	return (
		<div
			className={cn("workspace-topbar-actions shrink-0", className)}
			data-testid="workspace-topbar-actions"
			style={noDragStyle}
			{...props}
		>
			{children}
		</div>
	);
}

/**
 * The session tab strip's frame. Terminal and chat sessions render different
 * tabs but share this chrome: surface, window-control clearance, and the
 * trailing action region.
 */
export function SessionTabsTopbar({
	className,
	tabs,
	actions,
	clearanceRef,
	regionWidth = "100%",
	clearanceClassName,
	hideActions = false,
	fullscreen = false,
}: {
	className?: string;
	tabs: ReactNode;
	actions?: ReactNode;
	clearanceRef?: Ref<HTMLDivElement>;
	regionWidth?: number | string;
	/** Window-control clearance classes for the tabs region. */
	clearanceClassName?: string;
	hideActions?: boolean;
	/** Native fullscreen hides the panel-owned controls outside this subtree. */
	fullscreen?: boolean;
}) {
	return (
		<PageTopbarFrame className={cn("items-stretch bg-sidebar", className)}>
			<div className="session-topbar-surface flex min-w-0 flex-1" data-testid="session-workspace-topbar">
				<div
					className={cn("flex min-w-0 shrink items-stretch", clearanceClassName)}
					data-testid="session-terminal-region"
					ref={clearanceRef}
					style={{ width: regionWidth }}
				>
					{tabs}
				</div>
				{!hideActions || fullscreen ? (
					<div
						className="session-topbar-actions ml-auto flex shrink-0 items-center"
						data-testid="session-action-region"
						data-fullscreen={fullscreen || undefined}
					>
						{hideActions ? null : actions}
						{fullscreen ? <NotificationCenter style={noDragStyle} /> : null}
					</div>
				) : null}
			</div>
		</PageTopbarFrame>
	);
}
