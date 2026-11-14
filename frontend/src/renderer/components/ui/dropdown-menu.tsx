import { useContext } from "react";
import { Check, ChevronRight } from "@/components/app-icons";
import { DropdownMenu as DropdownMenuPrimitive } from "radix-ui";
import { cn } from "../../lib/utils";
import { composeMenuCloseAutoFocus, ShieldOutsideClick, skipFocusHopToSibling, swallowClickAfter, useMenuReturnTarget } from "./menu-focus";
import {
	actionMenuContentClass,
	actionMenuDestructiveItemClass,
	actionMenuItemClass,
	actionMenuLabelClass,
	actionMenuSeparatorClass,
} from "./menu-styles";

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
	onContextMenu,
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
				onContextMenu={(event) => {
					onContextMenu?.(event);
					event.stopPropagation();
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
	variant = "default",
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Item> & { inset?: boolean; variant?: "default" | "destructive" }) {
	return (
		<DropdownMenuPrimitive.Item
			className={cn(
				actionMenuItemClass,
				variant === "destructive" && actionMenuDestructiveItemClass,
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

export function DropdownMenuRadioItem({
	className,
	children,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.RadioItem>) {
	return (
		<DropdownMenuPrimitive.RadioItem className={cn(actionMenuItemClass, className)} {...props}>
			{children}
			<span className="ml-auto flex size-4 shrink-0 items-center justify-center">
				<DropdownMenuPrimitive.ItemIndicator className="flex">
					<Check aria-hidden="true" className="text-logo-accent!" />
				</DropdownMenuPrimitive.ItemIndicator>
			</span>
		</DropdownMenuPrimitive.RadioItem>
	);
}

export function DropdownMenuCheckboxItem({
	className,
	children,
	...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.CheckboxItem>) {
	return (
		<DropdownMenuPrimitive.CheckboxItem className={cn(actionMenuItemClass, "group/checkbox-item", className)} {...props}>
			{children}
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

export function DropdownMenuSwitchItem({
	checked,
	className,
	children,
	onSelect,
	...props
}: Omit<React.ComponentProps<typeof DropdownMenuPrimitive.CheckboxItem>, "checked"> & { checked: boolean }) {
	return (
		<DropdownMenuPrimitive.CheckboxItem
			checked={checked}
			className={cn(actionMenuItemClass, className)}
			onSelect={(event) => {
				event.preventDefault();
				onSelect?.(event);
			}}
			{...props}
		>
			{children}
			<span
				aria-hidden="true"
				data-slot="switch"
				data-state={checked ? "checked" : "unchecked"}
				className="switch-inset relative ml-auto inline-flex h-4 w-8 shrink-0 items-center rounded-full border border-transparent transition-[background-color,border-color] duration-fast ease-out motion-reduce:transition-none"
			>
				<span
					data-slot="switch-thumb"
					className={cn(
						"block size-3.5 rounded-full transition-transform duration-fast ease-out motion-reduce:transition-none",
						checked ? "translate-x-4" : "translate-x-0",
					)}
				/>
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

export function DropdownMenuSubContent({
	className,
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
