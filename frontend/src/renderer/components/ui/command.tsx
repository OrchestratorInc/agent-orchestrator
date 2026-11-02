"use client";

import * as React from "react";
import { FocusScope } from "@radix-ui/react-focus-scope";
import { Command as CommandPrimitive } from "cmdk";
import * as Dialog from "@radix-ui/react-dialog";
import { Search } from "lucide-react";

import { cn } from "@/lib/utils";

function Command({ className, ...props }: React.ComponentProps<typeof CommandPrimitive>) {
	return (
		<CommandPrimitive
			data-slot="command"
			className={cn(
				"flex w-full flex-col overflow-hidden rounded-[var(--radius-command-palette)] text-foreground outline-none",
				className,
			)}
			{...props}
		/>
	);
}

function CommandDialog({
	title = "Command palette",
	description = "Search projects, sessions, PRs, and commands",
	children,
	className,
	commandProps,
	contentProps,
	modal,
	...props
}: React.ComponentProps<typeof Dialog.Root> & {
	title?: string;
	description?: string;
	className?: string;
	commandProps?: React.ComponentProps<typeof CommandPrimitive>;
	contentProps?: React.ComponentProps<typeof Dialog.Content>;
}) {
	const { className: commandClassName, ...restCommandProps } = commandProps ?? {};
	const nonModal = modal === false;
	const content = (
		<Dialog.Content
			data-slot="command-dialog-content"
			aria-label={title}
			aria-modal={nonModal || undefined}
			{...contentProps}
			className={cn(
				"fixed left-1/2 top-command-palette z-overlay w-command-palette -translate-x-1/2 overflow-hidden rounded-[var(--radius-command-palette)] border border-[var(--color-border-command-palette)] overlay-glass text-foreground shadow-[var(--shadow-command-palette)] outline-none data-[state=open]:animate-modal-in data-[state=closed]:animate-modal-out motion-reduce:animate-none",
				className,
			)}
		>
			<Dialog.Title className="sr-only">{title}</Dialog.Title>
			<Dialog.Description className="sr-only">{description}</Dialog.Description>
			<Command
				className={cn(
					"**:[[cmdk-group-heading]]:px-[var(--size-command-pad-x)] **:[[cmdk-group-heading]]:pt-2 **:[[cmdk-group-heading]]:pb-1 **:[[cmdk-group-heading]]:text-caption **:[[cmdk-group-heading]]:font-medium **:[[cmdk-group-heading]]:text-muted-foreground **:[[cmdk-group]]:px-0",
					commandClassName,
				)}
				{...restCommandProps}
			>
				{children}
			</Command>
		</Dialog.Content>
	);
	return (
		<Dialog.Root data-slot="command-dialog" modal={modal} {...props}>
			<Dialog.Portal>
				{nonModal ? (
					<>
						<div
							data-slot="command-dialog-overlay"
							className="dialog-overlay data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out motion-reduce:animate-none"
							onWheel={(event) => event.preventDefault()}
						/>
						<FocusScope loop trapped>{content}</FocusScope>
					</>
				) : (
					<>
						<Dialog.Overlay
							data-slot="command-dialog-overlay"
							className="dialog-overlay data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out motion-reduce:animate-none"
						/>
						{content}
					</>
				)}
			</Dialog.Portal>
		</Dialog.Root>
	);
}

function CommandInput({
	className,
	leading,
	trailing,
	...props
}: React.ComponentProps<typeof CommandPrimitive.Input> & {
	/** Replaces the search glyph, such as with a back button in a nested view. */
	leading?: React.ReactNode;
	/** Sits at the end of the field: a search spinner, the current scope. */
	trailing?: React.ReactNode;
}) {
	return (
		<CommandBar data-slot="command-input-wrapper" cmdk-input-wrapper="">
			{leading ?? <Search className="size-icon-base shrink-0 text-muted-foreground" aria-hidden="true" />}
			<CommandPrimitive.Input
				data-slot="command-input"
				className={cn(
					"flex h-control-sm min-w-0 flex-1 bg-transparent text-[length:var(--font-size-command-input)] leading-body text-foreground caret-foreground outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed disabled:opacity-50",
					className,
				)}
				{...props}
			/>
			{trailing}
		</CommandBar>
	);
}

/**
 * The palette's top bar. The search field and any view that replaces it (such
 * as New task) share it, so the bar keeps its height and the text its place.
 */
function CommandBar({ className, ...props }: React.ComponentProps<"div"> & { "cmdk-input-wrapper"?: string }) {
	return <div className={cn("flex items-center gap-2.5 px-[var(--size-command-pad-x)] pt-3 pb-1.5", className)} {...props} />;
}

function CommandList({ className, ...props }: React.ComponentProps<typeof CommandPrimitive.List>) {
	return (
		<CommandPrimitive.List
			data-slot="command-list"
			className={cn(
				"max-h-command-palette-list scrollbar-none scroll-py-1.5 overflow-y-auto overflow-x-hidden overscroll-contain pb-1.5 outline-none",
				className,
			)}
			{...props}
		/>
	);
}

function CommandEmpty({ ...props }: React.ComponentProps<typeof CommandPrimitive.Empty>) {
	return (
		<CommandPrimitive.Empty
			data-slot="command-empty"
			className="px-[var(--size-command-pad-x)] py-8 text-center text-control text-muted-foreground"
			{...props}
		/>
	);
}

function CommandGroup({ className, ...props }: React.ComponentProps<typeof CommandPrimitive.Group>) {
	return <CommandPrimitive.Group data-slot="command-group" className={cn("overflow-hidden", className)} {...props} />;
}

function CommandItem({ className, ...props }: React.ComponentProps<typeof CommandPrimitive.Item>) {
	return (
		<CommandPrimitive.Item
			data-slot="command-item"
			className={cn(
				"group/command-item relative mx-[var(--size-command-item-inset)] flex min-h-control-md cursor-default select-none items-center gap-2.5 rounded-md py-1 pr-2.5 pl-2.5 text-sm leading-body-md text-foreground outline-none",
				"data-[selected=true]:bg-[var(--color-bg-command-item-active)]",
				"data-[disabled=true]:pointer-events-none data-[disabled=true]:opacity-50",
				"[&_svg]:size-icon-base [&_svg]:shrink-0 [&_svg]:text-muted-foreground data-[selected=true]:[&_svg]:text-foreground",
				className,
			)}
			{...props}
		/>
	);
}

/**
 * Keys for a command. Rows show bare glyphs (⇧⌘O) so they read as a hint, not
 * a control; the footer shows each key as a small cap.
 */
function CommandShortcut({ keys, variant = "glyphs", className }: { keys: readonly string[]; variant?: "glyphs" | "caps"; className?: string }) {
	if (variant === "glyphs") {
		return (
			<span
				data-slot="command-shortcut"
				className={cn("shrink-0 font-sans text-control tracking-wide text-muted-foreground group-data-[selected=true]/command-item:text-foreground/75", className)}
				aria-hidden="true"
			>
				{keys.every((key) => key.length === 1) ? keys.join("") : keys.join("+")}
			</span>
		);
	}
	return (
		<span data-slot="command-shortcut" className={cn("inline-flex shrink-0 items-center gap-1", className)} aria-hidden="true">
			{keys.map((key, index) => (
				<kbd
					key={`${key}-${index}`}
					className="inline-flex h-control-xs min-w-control-xs items-center justify-center rounded-sm bg-interactive-active px-1.5 font-sans text-caption leading-none text-foreground"
				>
					{key}
				</kbd>
			))}
		</span>
	);
}

function CommandFooter({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="command-footer"
			className={cn(
				"flex h-control-lg items-center gap-3.5 bg-interactive-hover px-[var(--size-command-pad-x)] text-caption text-muted-foreground",
				className,
			)}
			{...props}
		/>
	);
}

export {
	Command,
	CommandDialog,
	CommandBar,
	CommandInput,
	CommandList,
	CommandEmpty,
	CommandGroup,
	CommandItem,
	CommandShortcut,
	CommandFooter,
};
