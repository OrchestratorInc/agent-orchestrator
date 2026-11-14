import { ChevronRight } from "@/components/app-icons";
import { ContextMenu as ContextMenuPrimitive } from "radix-ui";
import { cn } from "../../lib/utils";
import { composeMenuCloseAutoFocus, useMenuReturnTarget } from "./menu-focus";
import {
	actionMenuContentClass,
	actionMenuItemClass,
	actionMenuLabelClass,
	actionMenuSeparatorClass,
} from "./menu-styles";

export const ContextMenu = ContextMenuPrimitive.Root;
export const ContextMenuTrigger = ContextMenuPrimitive.Trigger;
export const ContextMenuGroup = ContextMenuPrimitive.Group;
export const ContextMenuPortal = ContextMenuPrimitive.Portal;

export function ContextMenuContent({
	className,
	onCloseAutoFocus,
	onContextMenu,
	...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Content>) {
	const rememberReturnTarget = useMenuReturnTarget<HTMLDivElement>();
	return (
		<ContextMenuPrimitive.Portal>
			<ContextMenuPrimitive.Content
				ref={rememberReturnTarget}
				onCloseAutoFocus={composeMenuCloseAutoFocus(onCloseAutoFocus)}
				onContextMenu={(event) => {
					onContextMenu?.(event);
					event.stopPropagation();
				}}
				className={cn(
					actionMenuContentClass,
					"origin-(--radix-context-menu-content-transform-origin)",
					"data-[state=open]:animate-popover-in data-[state=closed]:animate-popover-out",
					className,
				)}
				{...props}
			/>
		</ContextMenuPrimitive.Portal>
	);
}

export function ContextMenuItem({
	className,
	inset,
	...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Item> & { inset?: boolean }) {
	return (
		<ContextMenuPrimitive.Item
			className={cn(
				actionMenuItemClass,
				inset && "pl-8",
				className,
			)}
			{...props}
		/>
	);
}

export function ContextMenuLabel({
	className,
	inset,
	...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Label> & { inset?: boolean }) {
	return (
		<ContextMenuPrimitive.Label
			className={cn(
				actionMenuLabelClass,
				inset && "pl-8",
				className,
			)}
			{...props}
		/>
	);
}

export function ContextMenuSeparator({
	className,
	...props
}: React.ComponentProps<typeof ContextMenuPrimitive.Separator>) {
	return <ContextMenuPrimitive.Separator className={cn(actionMenuSeparatorClass, className)} {...props} />;
}

export function ContextMenuShortcut({ className, ...props }: React.ComponentProps<"span">) {
	return <span className={cn("ml-auto pl-4 text-micro tracking-wide-md text-passive", className)} {...props} />;
}

export const ContextMenuSub = ContextMenuPrimitive.Sub;

export function ContextMenuSubTrigger({
	className,
	children,
	...props
}: React.ComponentProps<typeof ContextMenuPrimitive.SubTrigger>) {
	return (
		<ContextMenuPrimitive.SubTrigger
			className={cn(actionMenuItemClass, "data-[state=open]:bg-interactive-hover", className)}
			{...props}
		>
			{children}
			<ChevronRight aria-hidden="true" className="ml-auto size-3.5 shrink-0 text-passive" />
		</ContextMenuPrimitive.SubTrigger>
	);
}

export function ContextMenuSubContent({
	className,
	sideOffset = 9,
	alignOffset = -5,
	...props
}: React.ComponentProps<typeof ContextMenuPrimitive.SubContent>) {
	return (
		<ContextMenuPrimitive.Portal>
			<ContextMenuPrimitive.SubContent
				alignOffset={alignOffset}
				sideOffset={sideOffset}
				className={cn(
					actionMenuContentClass,
					"origin-(--radix-context-menu-content-transform-origin)",
					"data-[state=open]:animate-popover-in data-[state=closed]:animate-popover-out",
					className,
				)}
				{...props}
			/>
		</ContextMenuPrimitive.Portal>
	);
}
