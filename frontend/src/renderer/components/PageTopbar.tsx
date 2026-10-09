import { useEffect, type ComponentProps, type CSSProperties, type ReactNode, type Ref } from "react";
import { animate, motion, useMotionValue, useReducedMotion } from "motion/react";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { SHELL_PANEL_SPRING } from "../lib/motion-spring";
import { sidebarOccupiesLayout, useUiStore } from "../stores/ui-store";
import { Skeleton } from "./ui/skeleton";

/**
 * The one page topbar. Every center-panel page (board, automations, archive,
 * settings, session chrome) renders its header through this module so height,
 * insets, gaps, border, drag region and window-control clearance are defined
 * once. Pages supply only content: a lead (where am I) and actions.
 *
 * - `PageTopbar` is the page header row (lead + actions).
 * - `SessionTabsTopbar` is the same frame around a session's tab strip.
 */

const isMac = isMacPlatform();
const dragStyle = isMac ? ({ WebkitAppRegion: "drag" } as CSSProperties) : undefined;
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as CSSProperties) : undefined;

// Pixel equivalents of the CSS custom properties used for titlebar clearance on
// Linux, which animates its inset in JS when the sidebar toggles. macOS clears
// its traffic lights through `.center-panel-shell--titlebar-clearance*` CSS and
// Windows owns a separate window titlebar, so neither needs the JS inset.
// --size-titlebar-cluster-left-linux-panel (26) + --size-titlebar-cluster-width (92)
// + --size-titlebar-content-gap (12) - --size-center-panel-inline-inset (16).
const PADDING_DEFAULT = 18; // 1.125rem
const PADDING_CLEARANCE_LINUX = 114;

/** Left padding of a page topbar, animated as the sidebar toggles so the lead clears the window controls. */
export function usePageTopbarPaddingLeft() {
	const isSidebarOpen = useUiStore(sidebarOccupiesLayout);
	const prefersReducedMotion = useReducedMotion();
	const target = !isSidebarOpen && isLinuxPlatform() ? PADDING_CLEARANCE_LINUX : PADDING_DEFAULT;
	const paddingLeft = useMotionValue(target);
	useEffect(() => {
		const controls = animate(paddingLeft, target, prefersReducedMotion ? { duration: 0 } : SHELL_PANEL_SPRING);
		return controls.stop;
	}, [target, paddingLeft, prefersReducedMotion]);
	return paddingLeft;
}

/** Height and flex frame shared by the page header row and the session tab strip. */
const topbarFrameClass = "flex h-toolbar w-full shrink-0 items-center";

export const topbarTitleClass = "text-brand font-semibold tracking-tight leading-none text-foreground whitespace-nowrap";

export function PageTopbar({
	lead,
	actions,
	loading = false,
	className,
	...props
}: Omit<ComponentProps<"header">, "children" | "style"> & {
	/** Orientation: the page's title or crumb. */
	lead?: ReactNode;
	/** High-value page actions, right-aligned. */
	actions?: ReactNode;
	/** Renders the lead as a skeleton so the bar keeps its shape while data resolves. */
	loading?: boolean;
}) {
	const paddingLeft = usePageTopbarPaddingLeft();
	return (
		<motion.header
			aria-busy={loading || undefined}
			className={cn(topbarFrameClass, "workspace-topbar-container center-panel-titlebar relative z-chrome gap-3 border-b border-border-strong pr-3", className)}
			style={isMac ? dragStyle : { paddingLeft }}
			{...(props as object)}
		>
			<div className="flex min-w-0 flex-1 items-center gap-2.5">
				{loading ? <PageTopbarLeadSkeleton /> : lead}
			</div>
			{actions ? <PageTopbarActions>{actions}</PageTopbarActions> : null}
		</motion.header>
	);
}

/** Title with an optional leading icon, the standard lead for a page. */
export function PageTopbarTitle({
	as: Component = "span",
	icon,
	children,
	...props
}: Omit<ComponentProps<"span">, "className"> & { as?: "span" | "h1"; icon?: ReactNode }) {
	return (
		<Component className={cn(topbarTitleClass, "inline-flex min-w-0 items-center gap-1.5")} {...(props as object)}>
			{icon}
			<span className="max-w-content-max truncate">{children}</span>
		</Component>
	);
}

export function PageTopbarLeadSkeleton() {
	return <Skeleton aria-hidden="true" className="h-4 w-24" data-testid="page-topbar-skeleton" />;
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
	as: Component = "div",
	className,
	tabs,
	actions,
	clearanceRef,
	regionWidth = "100%",
	clearanceClassName,
	hideActions = false,
}: {
	as?: "div" | "header";
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
		<Component className={cn(topbarFrameClass, "items-stretch bg-sidebar", className)}>
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
		</Component>
	);
}
