import { createContext, forwardRef, useCallback, useContext, useRef, useState, type ButtonHTMLAttributes } from "react";
import { ChevronDown, ChevronRight } from "@/components/app-icons";
import { DropdownMenu as DropdownMenuPrimitive } from "radix-ui";
import { cn } from "../../lib/utils";
import { GHOST_PILL_CLASS } from "./button";
import { ShieldOutsideClick, skipFocusHopToSibling, swallowClickAfter } from "./menu-focus";

const SURFACE =
	"settings-menu-surface min-w-[14rem] rounded-(--radius-settings-panel) border border-(--color-border-overlay) bg-settings-menu shadow-(--shadow-popover)";

const ROW =
	"relative isolate flex min-w-0 cursor-default items-center gap-2.5 rounded-none px-2 py-1.5 text-control text-muted-foreground outline-none whitespace-nowrap transition-none! before:pointer-events-none before:absolute before:inset-0 before:-z-10 before:rounded-md focus:before:bg-interactive-hover focus:text-foreground data-highlighted:before:bg-interactive-hover data-highlighted:text-foreground data-[search-active=true]:before:bg-interactive-hover data-[search-active=true]:text-foreground data-[active=true]:before:bg-interactive-hover data-[active=true]:text-foreground";

const LABEL =
	"px-2 py-1.5 text-micro tracking-wide text-passive";

export const MENU_TRIGGER_CHROME =
	"settings-option-trigger button-raised max-w-full min-w-0 border [--button-color:var(--secondary)] text-secondary-foreground motion-reduce:transition-none disabled:cursor-not-allowed disabled:opacity-50";

export const MENU_GHOST_TRIGGER_CHROME = cn(
	"settings-option-trigger max-w-full min-w-0 border-0 text-foreground motion-reduce:transition-none disabled:cursor-not-allowed disabled:opacity-50",
	GHOST_PILL_CLASS,
);
const CloseSubmenu = createContext<(() => void) | undefined>(undefined);

export function OptionMenu({ modal, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Root>) {
	return (
		<ShieldOutsideClick.Provider value={modal === undefined}>
			<DropdownMenuPrimitive.Root modal={modal ?? false} {...props} />
		</ShieldOutsideClick.Provider>
	);
}

export const OptionMenuTrigger = forwardRef<
	HTMLButtonElement,
	ButtonHTMLAttributes<HTMLButtonElement> & { showCaret?: boolean; variant?: "secondary" | "ghost" }
>(function OptionMenuTrigger({ className, children, showCaret = true, variant = "secondary", ...props }, ref) {
	return (
		<DropdownMenuPrimitive.Trigger asChild>
			<button ref={ref} type="button" className={cn("group/option-menu-trigger", variant === "ghost" ? MENU_GHOST_TRIGGER_CHROME : MENU_TRIGGER_CHROME, className)} {...props}>
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

export function OptionMenuContent({
	className,
	sideOffset = 6,
	steady,
	children,
	onPointerDownOutside,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Content> & {
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

export function OptionMenuItem({
	className,
	active,
	searchActive,
	radio,
	onPointerLeave,
	onSelect,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Item> & {
	active?: boolean;
	searchActive?: boolean;
	radio?: boolean;
}) {
	const closeSubmenu = useContext(CloseSubmenu);
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
			onSelect={(event) => {
				onSelect?.(event);
				if (!closeSubmenu || event.defaultPrevented) return;
				const triggerId = event.currentTarget instanceof HTMLElement
					? event.currentTarget.closest('[role="menu"]')?.getAttribute("aria-labelledby")
					: undefined;
				event.preventDefault();
				closeSubmenu();
				if (triggerId) document.getElementById(triggerId)?.focus();
			}}
			{...props}
		/>
	);
}

const AllowSubmenuReopen = createContext<(() => void) | undefined>(undefined);

export function OptionMenuSub({
	closeOnSelect = false,
	open,
	defaultOpen = false,
	onOpenChange,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Sub> & { closeOnSelect?: boolean }) {
	const [localOpen, setLocalOpen] = useState(defaultOpen);
	const closedBySelection = useRef(false);
	const changeOpen = (next: boolean) => {
		if (next && closedBySelection.current) return;
		setLocalOpen(next);
		onOpenChange?.(next);
	};
	const closeAfterSelection = () => {
		closedBySelection.current = true;
		changeOpen(false);
	};
	const allowReopen = useCallback(() => {
		closedBySelection.current = false;
	}, []);
	return (
		<CloseSubmenu.Provider value={closeOnSelect ? closeAfterSelection : undefined}>
			<AllowSubmenuReopen.Provider value={allowReopen}>
				<DropdownMenuPrimitive.Sub open={open ?? localOpen} onOpenChange={changeOpen} {...props} />
			</AllowSubmenuReopen.Provider>
		</CloseSubmenu.Provider>
	);
}

export function OptionMenuSubTrigger({
	className,
	label,
	value,
	children,
	onClick,
	onKeyDown,
	onPointerMove,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.SubTrigger> & {
	label?: string;
	value?: string;
}) {
	const allowReopen = useContext(AllowSubmenuReopen);
	return (
		<DropdownMenuPrimitive.SubTrigger
			className={cn(ROW, "justify-between gap-3 text-xs text-foreground data-[state=open]:before:bg-interactive-hover data-[state=open]:text-foreground", className)}
			onClick={(e) => {
				e.stopPropagation();
				allowReopen?.();
				onClick?.(e);
			}}
			onKeyDown={(e) => {
				allowReopen?.();
				onKeyDown?.(e);
			}}
			onPointerMove={(e) => {
				if (e.movementX || e.movementY) allowReopen?.();
				onPointerMove?.(e);
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
