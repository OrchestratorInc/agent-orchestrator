import { forwardRef, useCallback, useContext, useRef, type ButtonHTMLAttributes } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { DropdownMenu as DropdownMenuPrimitive } from "radix-ui";
import { cn } from "../../lib/utils";
import { ShieldOutsideClick, skipFocusHopToSibling, swallowClickAfter } from "./menu-focus";

const SURFACE =
	"settings-menu-surface min-w-[14rem] rounded-(--radius-settings-panel) border border-(--color-border-overlay) bg-settings-menu shadow-(--shadow-popover)";

const ROW =
	"relative isolate flex min-w-0 cursor-default items-center gap-2.5 rounded-none px-2 py-1.5 text-control text-muted-foreground outline-none whitespace-nowrap transition-none! before:pointer-events-none before:absolute before:inset-0 before:-z-10 before:rounded-md focus:before:bg-interactive-hover focus:text-foreground data-highlighted:before:bg-interactive-hover data-highlighted:text-foreground data-[search-active=true]:before:bg-interactive-hover data-[search-active=true]:text-foreground data-[active=true]:before:bg-interactive-hover data-[active=true]:text-foreground";

const LABEL =
	"px-2 py-1.5 text-micro tracking-wide text-passive";

/**
 * The quiet filled trigger both this menu and the Settings pickers wear —
 * everything except the `group/*` token, which each owner names for itself so
 * its own chevron can key off it. Shared because the two are meant to be the
 * same control: a visual tweak here should never need making twice.
 */
export const MENU_TRIGGER_CHROME =
	"settings-option-trigger max-w-full min-w-0 bg-[var(--color-bg-settings-trigger)] text-[var(--color-text-settings-trigger)] hover:bg-[var(--color-bg-settings-trigger-hover)] hover:text-[var(--color-text-settings-trigger)] data-[state=open]:bg-[var(--color-bg-settings-trigger-hover)] focus:outline-none focus-visible:outline-none focus-visible:ring-0 data-[state=open]:outline-none data-[state=open]:ring-0 disabled:cursor-not-allowed disabled:opacity-50";

const TRIGGER = cn("group/option-menu-trigger", MENU_TRIGGER_CHROME);

// ---------------------------------------------------------------------------
// Root — re-export for convenience
// ---------------------------------------------------------------------------

/**
 * Not modal by default. A modal menu locks scrolling (a stylesheet added and removed)
 * and turns off pointer events on <body>; either restyles every element on the page
 * when the menu opens and again when it closes. Unless `modal` is passed, an outside
 * click still only closes the menu, as a modal one's would.
 */
export function OptionMenu({ modal, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Root>) {
	return (
		<ShieldOutsideClick.Provider value={modal === undefined}>
			<DropdownMenuPrimitive.Root modal={modal ?? false} {...props} />
		</ShieldOutsideClick.Provider>
	);
}

// ---------------------------------------------------------------------------
// Trigger
// ---------------------------------------------------------------------------

export const OptionMenuTrigger = forwardRef<
	HTMLButtonElement,
	ButtonHTMLAttributes<HTMLButtonElement> & { showCaret?: boolean }
>(function OptionMenuTrigger({ className, children, showCaret = true, ...props }, ref) {
	return (
		<DropdownMenuPrimitive.Trigger asChild>
			<button ref={ref} type="button" className={cn(TRIGGER, className)} {...props}>
				{children}
				{showCaret ? (
					<ChevronDown
						className="size-icon-sm shrink-0 group-data-[state=open]/option-menu-trigger:rotate-180"
						aria-hidden="true"
					/>
				) : null}
			</button>
		</DropdownMenuPrimitive.Trigger>
	);
});

// ---------------------------------------------------------------------------
// Content (the panel)
// ---------------------------------------------------------------------------

export function OptionMenuContent({
	className,
	sideOffset = 6,
	steady,
	children,
	onPointerDownOutside,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Content> & {
	/**
	 * Keep the rows still while open. A menu that opens upward is pinned by its bottom
	 * edge, so a row that disappears would pull every row above it down under the pointer.
	 */
	steady?: boolean;
}) {
	const steadyRef = useSteadyHeight(steady);
	const shield = useContext(ShieldOutsideClick);
	return (
		<DropdownMenuPrimitive.Portal>
			<DropdownMenuPrimitive.Content
				ref={steadyRef}
				onPointerDownOutside={(event) => {
					onPointerDownOutside?.(event);
					if (shield) swallowClickAfter(event);
				}}
				data-browser-native-overlay="true"
				sideOffset={sideOffset}
				collisionPadding={16}
				className={cn(
					"z-overlay",
					SURFACE,
					"origin-(--radix-dropdown-menu-content-transform-origin)",
					"data-[state=open]:animate-popover-in data-[state=closed]:animate-popover-out",
					className,
				)}
				{...props}
			>
				{children}
			</DropdownMenuPrimitive.Content>
		</DropdownMenuPrimitive.Portal>
	);
}

/**
 * Shrinks the panel from the bottom while the pointer is on it. The height it loses
 * becomes a bottom margin, which the positioned wrapper still counts, so the top edge
 * and every row stay put; the space sits outside the panel, between it and the
 * trigger. Once the pointer leaves for anywhere but a submenu, the panel settles
 * back onto its trigger.
 */
function useSteadyHeight(enabled: boolean | undefined) {
	const cleanup = useRef<(() => void) | null>(null);
	return useCallback((node: HTMLDivElement | null) => {
		cleanup.current?.();
		cleanup.current = null;
		if (!node || !enabled) return;
		let tallest = 0;
		const observer = new ResizeObserver(() => {
			const height = node.offsetHeight;
			tallest = Math.max(tallest, height);
			node.style.marginBottom = tallest > height ? `${tallest - height}px` : "";
		});
		const settle = (event: PointerEvent) => {
			if (event.relatedTarget instanceof Element && event.relatedTarget.closest('[role="menu"]')) return;
			tallest = node.offsetHeight;
			node.style.marginBottom = "";
		};
		observer.observe(node);
		node.addEventListener("pointerleave", settle);
		cleanup.current = () => {
			observer.disconnect();
			node.removeEventListener("pointerleave", settle);
		};
	}, [enabled]);
}

// ---------------------------------------------------------------------------
// Label
// ---------------------------------------------------------------------------

export function OptionMenuLabel({
	className,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Label>) {
	return (
		<DropdownMenuPrimitive.Label
			className={cn(LABEL, className)}
			{...props}
		/>
	);
}

// ---------------------------------------------------------------------------
// Item
// ---------------------------------------------------------------------------

export function OptionMenuItem({
	className,
	active,
	searchActive,
	radio,
	onPointerLeave,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Item> & {
	active?: boolean;
	/** Visual active descendant used while a menu-owned search field retains focus. */
	searchActive?: boolean;
	radio?: boolean;
}) {
	return (
		<DropdownMenuPrimitive.Item
			{...(radio === undefined
				? {}
			: { role: "menuitemradio" as const, "aria-checked": active })}
			data-active={active || undefined}
			data-search-active={searchActive || undefined}
			className={cn(ROW, className)}
			onPointerLeave={(event) => {
				onPointerLeave?.(event);
				skipFocusHopToSibling(event);
			}}
			{...props}
		/>
	);
}

// ---------------------------------------------------------------------------
// Submenu — trigger row + subcontent panel
// ---------------------------------------------------------------------------

export const OptionMenuSub = DropdownMenuPrimitive.Sub;

export function OptionMenuSubTrigger({
	className,
	label,
	value,
	children,
	onClick,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.SubTrigger> & {
	label?: string;
	value?: string;
}) {
	return (
		<DropdownMenuPrimitive.SubTrigger
			className={cn(ROW, "justify-between gap-3 text-xs text-foreground data-[state=open]:before:bg-interactive-hover data-[state=open]:text-foreground", className)}
			// Stop the click reaching the composer's click-to-focus handler without
			// calling preventDefault, which Radix's composeEventHandlers reads as a
			// signal to skip its own open/close handling.
			onClick={(e) => {
				e.stopPropagation();
				onClick?.(e);
			}}
			{...props}
		>
			{children ?? (
				<>
					<span>{label}</span>
					<span className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
						<span className="min-w-0 truncate">{value}</span>
						<ChevronRight aria-hidden="true" className="size-3 shrink-0" />
					</span>
				</>
			)}
		</DropdownMenuPrimitive.SubTrigger>
	);
}

export function OptionMenuSubContent({
	className,
	sideOffset = 6,
	alignOffset = -4,
	scrollable,
	children,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.SubContent> & {
	scrollable?: boolean;
}) {
	return (
		<DropdownMenuPrimitive.Portal>
			<DropdownMenuPrimitive.SubContent
				data-browser-native-overlay="true"
				sideOffset={sideOffset}
				alignOffset={alignOffset}
				collisionPadding={16}
				className={cn(
					"z-overlay",
					SURFACE,
					"h-fit!",
					"origin-(--radix-dropdown-menu-content-transform-origin)",
					"data-[state=open]:animate-popover-in",
					scrollable && "max-h-select-menu-max! overflow-hidden!",
					className,
				)}
				{...props}
			>
				{children}
			</DropdownMenuPrimitive.SubContent>
		</DropdownMenuPrimitive.Portal>
	);
}
