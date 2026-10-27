import { useContext } from "react";
import { Check, ChevronRight } from "lucide-react";
import { DropdownMenu as DropdownMenuPrimitive } from "radix-ui";
import { cn } from "../../lib/utils";
import { composeMenuCloseAutoFocus, ShieldOutsideClick, skipFocusHopToSibling, swallowClickAfter, useMenuReturnTarget } from "./menu-focus";
import {
	actionMenuContentClass,
	actionMenuItemClass,
	actionMenuLabelClass,
	actionMenuSeparatorClass,
} from "./menu-styles";

/**
 * Not modal by default. A modal menu locks scrolling (a stylesheet added and removed)
 * and turns off pointer events on <body>; either restyles every element on the page
 * when the menu opens and again when it closes. Unless `modal` is passed, an outside
 * click still only closes the menu, as a modal one's would.
 */
export function DropdownMenu({ modal, ...props }: React.ComponentProps<typeof DropdownMenuPrimitive.Root>) {
	return (
		<ShieldOutsideClick.Provider value={modal === undefined}>
			<DropdownMenuPrimitive.Root modal={modal ?? false} {...props} />
		</ShieldOutsideClick.Provider>
	);
}
export const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger;
export const DropdownMenuGroup = DropdownMenuPrimitive.Group;
export const DropdownMenuPortal = DropdownMenuPrimitive.Portal;

export function DropdownMenuContent({
	className,
	onCloseAutoFocus,
	portalContainer,
	sideOffset = 6,
	onPointerDownOutside,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Content> & {
	portalContainer?: React.ComponentProps<typeof DropdownMenuPrimitive.Portal>["container"];
}) {
	const rememberReturnTarget = useMenuReturnTarget<HTMLDivElement>();
	const shield = useContext(ShieldOutsideClick);
	return (
		<DropdownMenuPrimitive.Portal container={portalContainer}>
			<DropdownMenuPrimitive.Content
				ref={rememberReturnTarget}
				onCloseAutoFocus={composeMenuCloseAutoFocus(onCloseAutoFocus)}
				onPointerDownOutside={(event) => {
					onPointerDownOutside?.(event);
					if (shield) swallowClickAfter(event);
				}}
				sideOffset={sideOffset}
				className={cn(
					actionMenuContentClass,
					"origin-(--radix-dropdown-menu-content-transform-origin)",
					"data-[state=open]:animate-popover-in data-[state=closed]:animate-popover-out",
					className,
				)}
				{...props}
			/>
		</DropdownMenuPrimitive.Portal>
	);
}

export function DropdownMenuItem({
	className,
	inset,
	onPointerLeave,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Item> & { inset?: boolean }) {
	return (
		<DropdownMenuPrimitive.Item
			className={cn(
				actionMenuItemClass,
				inset && "pl-8",
				className,
			)}
			onPointerLeave={(event) => {
				onPointerLeave?.(event);
				skipFocusHopToSibling(event);
			}}
			{...props}
		/>
	);
}

export const DropdownMenuRadioGroup = DropdownMenuPrimitive.RadioGroup;

/**
 * A single-choice row. The trailing check sits in a reserved slot so labels
 * never shift when the selection moves; picking a row closes the menu.
 */
export function DropdownMenuRadioItem({
	className,
	children,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.RadioItem>) {
	return (
		<DropdownMenuPrimitive.RadioItem className={cn(actionMenuItemClass, className)} {...props}>
			{children}
			<span className="ml-auto flex size-4 shrink-0 items-center justify-center">
				{/* ! because the row's [&_svg] colour outranks a plain utility. */}
				<DropdownMenuPrimitive.ItemIndicator className="flex">
					<Check aria-hidden="true" className="text-logo-accent!" />
				</DropdownMenuPrimitive.ItemIndicator>
			</span>
		</DropdownMenuPrimitive.RadioItem>
	);
}

/**
 * A multi-choice row with a trailing box drawn like the review pane's viewed
 * checkbox, in the same reserved column as a radio row's check. Radix closes
 * the menu on select; callers that toggle several rows in one visit call
 * `event.preventDefault()` in `onSelect`.
 */
export function DropdownMenuCheckboxItem({
	className,
	children,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.CheckboxItem>) {
	return (
		<DropdownMenuPrimitive.CheckboxItem className={cn(actionMenuItemClass, "group/checkbox-item", className)} {...props}>
			{children}
			{/* The row forces 15px icons; the check fits the 16px box at 12px. */}
			<span
				aria-hidden="true"
				className="ml-auto flex size-4 shrink-0 items-center justify-center rounded-sm border border-muted-foreground/70 group-data-[state=checked]/checkbox-item:border-foreground group-data-[state=checked]/checkbox-item:bg-foreground [&_svg]:size-3! [&_svg]:text-background!"
			>
				<DropdownMenuPrimitive.ItemIndicator className="flex">
					<Check />
				</DropdownMenuPrimitive.ItemIndicator>
			</span>
		</DropdownMenuPrimitive.CheckboxItem>
	);
}

export function DropdownMenuLabel({
	className,
	inset,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Label> & { inset?: boolean }) {
	return (
		<DropdownMenuPrimitive.Label
			className={cn(
				actionMenuLabelClass,
				inset && "pl-8",
				className,
			)}
			{...props}
		/>
	);
}

export function DropdownMenuSeparator({
	className,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Separator>) {
	return <DropdownMenuPrimitive.Separator className={cn(actionMenuSeparatorClass, className)} {...props} />;
}

export function DropdownMenuShortcut({ className, ...props }: React.ComponentProps<"span">) {
	return <span className={cn("ml-auto text-micro tracking-wide-md text-passive", className)} {...props} />;
}

export const DropdownMenuSub = DropdownMenuPrimitive.Sub;

/** A row that opens a side submenu; same row styling as DropdownMenuItem. */
export function DropdownMenuSubTrigger({
	className,
	children,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.SubTrigger>) {
	return (
		<DropdownMenuPrimitive.SubTrigger
			className={cn(actionMenuItemClass, "data-[state=open]:bg-interactive-hover", className)}
			{...props}
		>
			{children}
			<ChevronRight aria-hidden="true" className="ml-auto size-3.5 shrink-0 text-passive" />
		</DropdownMenuPrimitive.SubTrigger>
	);
}

/** The side panel a DropdownMenuSubTrigger opens; same surface as DropdownMenuContent. */
export function DropdownMenuSubContent({
	className,
	// Offsets are measured from the sub-trigger row, which sits inside the parent
	// menu's 1px border + 4px padding: 9 leaves a visible 4px gap instead of the
	// panels touching, and -5 lines the submenu's first row up with the trigger.
	sideOffset = 9,
	alignOffset = -5,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.SubContent>) {
	return (
		<DropdownMenuPrimitive.Portal>
			<DropdownMenuPrimitive.SubContent
				alignOffset={alignOffset}
				sideOffset={sideOffset}
				className={cn(
					actionMenuContentClass,
					"origin-(--radix-dropdown-menu-content-transform-origin)",
					"data-[state=open]:animate-popover-in data-[state=closed]:animate-popover-out",
					className,
				)}
				{...props}
			/>
		</DropdownMenuPrimitive.Portal>
	);
}
