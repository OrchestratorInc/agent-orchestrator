import type { ComponentProps, ComponentPropsWithoutRef, CSSProperties, ReactNode, Ref } from "react";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { sidebarOccupiesLayout, useUiStore } from "../stores/ui-store";

/**
 * The one page topbar. Every center-panel page (board, automations, archive,
 * settings, artifacts, session chrome) renders its header through this module.
 * Height, insets, gaps, border, drag region and window-control clearance are defined
 * once. Pages supply only content: a lead (where am I) and actions.
 *
 * - `PageTopbar` is the page header row (lead + actions).
 * - `SessionTabsTopbar` is the same frame around a session's tab strip.
 */

const isMac = isMacPlatform();
const dragStyle = isMac ? ({ WebkitAppRegion: "drag" } as CSSProperties) : undefined;
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as CSSProperties) : undefined;

// Linux's fixed navigation cluster extends beyond the collapsed sidebar.
// macOS clearance comes from the center-panel-titlebar CSS; Windows has its
// own titlebar above the panel.
function usePageTopbarPaddingLeft() {
	const isSidebarOpen = useUiStore(sidebarOccupiesLayout);
	return !isSidebarOpen && isLinuxPlatform() ? 114 : 18;
}

function PageTopbarFrame({ className, ...props }: ComponentProps<"header">) {
	return <header className={cn("flex h-toolbar w-full shrink-0 items-center", className)} {...props} />;
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
			className={cn("workspace-topbar-container center-panel-titlebar relative z-chrome gap-3 border-b border-border-strong pr-3", className)}
			style={isMac ? dragStyle : { paddingLeft }}
			{...props}
		>
			<div className="flex min-w-0 flex-1 items-center gap-2.5">
				{lead}
			</div>
			{actions ? <PageTopbarActions>{actions}</PageTopbarActions> : null}
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
}: {
	className?: string;
	tabs: ReactNode;
	actions?: ReactNode;
	clearanceRef?: Ref<HTMLDivElement>;
	regionWidth?: number | string;
	/** Window-control clearance classes for the tabs region. */
	clearanceClassName?: string;
	hideActions?: boolean;
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
				{hideActions ? null : (
					<div
						className="ml-auto flex shrink-0 items-center gap-1 pl-2 pr-3"
						data-testid="session-action-region"
					>
						{actions}
					</div>
				)}
			</div>
		</PageTopbarFrame>
	);
}
