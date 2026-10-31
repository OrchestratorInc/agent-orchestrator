import { Fragment, type ReactNode, type Ref, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { useSuppressStrayFocusRing } from "../../hooks/useSuppressStrayFocusRing";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";
import {
	SETTINGS_MENU_ITEM,
	SETTINGS_MENU_SURFACE,
	SettingsMenuTrigger,
} from "./SettingsMenuTrigger";

export type SettingsOption<T extends string> = {
	value: T;
	label: string;
	icon?: ReactNode;
	disabled?: boolean;
	/** Heading for a run of consecutive options; shown above the first of each run. */
	group?: string;
};

export function SettingsOptionMenu<T extends string>({
	value,
	options,
	onChange,
	onHighlight,
	disabled,
	placeholder,
	renderMenuItem,
	renderTrigger,
	triggerClassName,
	showCaret,
	menuClassName,
	menuItemClassName,
	menuAlign = "end",
	searchable = false,
	searchPlaceholder,
	action,
	emptyLabel,
	triggerRef,
	onCloseAutoFocus: onMenuCloseAutoFocus,
	"aria-label": ariaLabel,
}: {
	value: T;
	options: SettingsOption<T>[];
	onChange: (value: T) => void;
	/** Called with the pointer- or keyboard-highlighted option, and `null` when the menu closes. */
	onHighlight?: (value: T | null) => void;
	disabled?: boolean;
	placeholder?: string;
	renderMenuItem?: (option: SettingsOption<T>, selected: boolean) => ReactNode;
	renderTrigger?: (selected: SettingsOption<T> | undefined, placeholder?: string) => ReactNode;
	triggerClassName?: string;
	showCaret?: boolean;
	menuClassName?: string;
	menuItemClassName?: string;
	menuAlign?: "start" | "center" | "end";
	searchable?: boolean;
	searchPlaceholder?: string;
	/** An extra row under the options, such as "Manage…". */
	action?: { label: string; onSelect: () => void; icon?: ReactNode };
	emptyLabel?: string;
	triggerRef?: Ref<HTMLButtonElement>;
	onCloseAutoFocus?: (event: Event) => void;
	"aria-label": string;
}) {
	const { t } = useTranslation();
	const [search, setSearch] = useState("");
	const [menuOpen, setMenuOpen] = useState(false);
	const selected = options.find((option) => option.value === value);
	const normalizedSearch = search.trim().toLocaleLowerCase();
	const visibleOptions = normalizedSearch
		? options.filter((option) =>
				`${option.label} ${option.value}`.toLocaleLowerCase().includes(normalizedSearch),
			)
		: options;
	const scrollRef = useRef<HTMLDivElement>(null);
	const [canScrollDown, setCanScrollDown] = useState(false);
	const updateScrollCue = useCallback(() => {
		const element = scrollRef.current;
		setCanScrollDown(Boolean(element && element.scrollHeight - element.scrollTop > element.clientHeight + 1));
	}, []);
	useLayoutEffect(() => {
		if (!menuOpen) {
			setCanScrollDown(false);
			return;
		}
		updateScrollCue();
		const element = scrollRef.current;
		if (!element || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(updateScrollCue);
		observer.observe(element);
		return () => observer.disconnect();
	}, [menuOpen, updateScrollCue, visibleOptions.length]);
	const onCloseAutoFocus = useSuppressStrayFocusRing(menuOpen);
	// Radix skips onOpenChange when an open menu unmounts (e.g. a shortcut closes
	// the settings view), so end any highlight here or a preview would stick.
	const highlightCleanupRef = useRef<(() => void) | null>(null);
	highlightCleanupRef.current = menuOpen && onHighlight ? () => onHighlight(null) : null;
	useEffect(() => () => highlightCleanupRef.current?.(), []);

	return (
		<DropdownMenu
			onOpenChange={(open) => {
				setMenuOpen(open);
				if (!open) {
					setSearch("");
					onHighlight?.(null);
				}
			}}
		>
			<DropdownMenuTrigger asChild disabled={disabled}>
				<SettingsMenuTrigger ref={triggerRef} className={triggerClassName} showCaret={showCaret} aria-label={ariaLabel}>
					{renderTrigger ? (
						renderTrigger(selected, placeholder)
					) : (
						<>
							{selected?.icon}
							<span className="min-w-0 truncate">{selected?.label ?? placeholder}</span>
						</>
					)}
				</SettingsMenuTrigger>
			</DropdownMenuTrigger>
			{/* This menu scrolls on an inner element, so the panel itself must not
			    inherit the surface utility's overflow-y. */}
			<DropdownMenuContent
				align={menuAlign}
				alignOffset={0}
				onCloseAutoFocus={(event) => {
					onMenuCloseAutoFocus?.(event);
					if (!event.defaultPrevented) onCloseAutoFocus(event);
				}}
				className={cn(SETTINGS_MENU_SURFACE, "overflow-hidden!", menuClassName)}
			>
				{searchable && (
					<div className="shrink-0 p-1" onKeyDown={(event) => event.stopPropagation()}>
						<input
							type="search"
							aria-label={t("settings.options.searchAria", { label: ariaLabel.toLocaleLowerCase() })}
							value={search}
							onChange={(event) => setSearch(event.target.value)}
							placeholder={searchPlaceholder ?? t("settings.options.searchPlaceholder")}
							className="menu-search-input"
						/>
					</div>
				)}
				<div
					data-slot="settings-option-menu-scroll-region"
					className="relative grid min-h-0 flex-1 grid-rows-[minmax(0,1fr)] overflow-hidden"
				>
					<div
						ref={scrollRef}
						className="model-menu-scroll min-h-0 overflow-y-auto overscroll-contain"
						onScroll={updateScrollCue}
					>
						{visibleOptions.map((option, index) => (
							<Fragment key={option.value}>
							{option.group && option.group !== visibleOptions[index - 1]?.group ? <DropdownMenuLabel>{option.group}</DropdownMenuLabel> : null}
							<DropdownMenuItem
								disabled={option.disabled}
								onSelect={() => onChange(option.value)}
								onFocus={onHighlight ? () => onHighlight(option.value) : undefined}
								// Hovering off the options (or into the search box) ends the
								// preview; moving to another option hands it straight over.
								onBlur={
									onHighlight
										? (event) => {
												const next = event.relatedTarget;
												if (next instanceof Element && next.closest('[role="menuitem"]')) return;
												onHighlight(null);
											}
										: undefined
								}
								className={cn(
									SETTINGS_MENU_ITEM,
									option.value === value && "bg-interactive-active text-foreground",
									menuItemClassName,
								)}
							>
								{renderMenuItem ? (
									renderMenuItem(option, option.value === value)
								) : (
									<>
										{option.icon}
										{option.label}
									</>
								)}
							</DropdownMenuItem>
							</Fragment>
						))}
						{visibleOptions.length === 0 && (
							<p className="px-2 py-1.5 text-xs text-settings-muted">{emptyLabel ?? t("settings.options.noMatches")}</p>
						)}
					</div>
					<div
						className={cn("model-menu-overflow-cue", canScrollDown ? "opacity-100" : "opacity-0")}
						aria-hidden="true"
					/>
				</div>
				{action && (
					<div data-slot="settings-option-menu-action" className="shrink-0">
						<DropdownMenuSeparator />
						<DropdownMenuItem onSelect={action.onSelect} className={cn(SETTINGS_MENU_ITEM, action.icon && "gap-2! [&_svg]:text-current!")}>
							{action.icon}
							{action.label}
						</DropdownMenuItem>
					</div>
				)}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
