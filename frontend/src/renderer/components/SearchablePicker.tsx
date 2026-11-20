import { Check, ChevronDown, Loader2, Lock, Plus, Search } from "@/components/app-icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../lib/utils";
import { LiveStatus } from "./LiveStatus";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Pressable } from "./ui/pressable";
import { NATIVE_BOUNCE_Y } from "../lib/native-overscroll";


/** Lists with more options than this get a search box. */
const SEARCH_THRESHOLD = 10;
export interface SearchablePickerOption {
	value: string;
	label: string;
	description?: string;
	private?: boolean;
}

export function SearchablePicker({
	ariaLabel,
	placeholder,
	searchPlaceholder,
	value,
	onChange,
	options,
	disabled = false,
	fixedScroll = false,
	className,
	action,
	defaultOpen = false,
	onDismiss,
	loading = false,
	loadingText,
}: {
	ariaLabel: string;
	placeholder: string;
	searchPlaceholder: string;
	value: string;
	onChange: (value: string) => void;
	options: SearchablePickerOption[];
	disabled?: boolean;
	fixedScroll?: boolean;
	className?: string;
	/** An action pinned below the options (e.g. "Connect more repositories"). */
	action?: { label: string; onSelect: () => void; disabled?: boolean };
	/** Opens the list on mount (e.g. a row the user just added). */
	defaultOpen?: boolean;
	/** Called when the list closes without a choice (Escape, outside click, trigger). */
	onDismiss?: () => void;
	/** The options are still loading: an empty list says `loadingText`, not "No matches". */
	loading?: boolean;
	loadingText?: string;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(defaultOpen && !disabled);
	const [search, setSearch] = useState("");
	const selected = options.find((option) => option.value === value);
	// A short list is easier to scan than to search; offer search only once the
	// list is long enough to need it.
	const searchable = options.length > SEARCH_THRESHOLD;
	const filtered = options.filter((option) =>
		`${option.label} ${option.description ?? ""}`.toLowerCase().includes(search.trim().toLowerCase()),
	);


	return (
		<Popover
			open={open}
			onOpenChange={(next) => {
				setOpen(next);
				if (!next) {
					setSearch("");
					onDismiss?.();
				}
			}}
		>
			<PopoverTrigger asChild>
				<Pressable
					role="combobox"
					aria-label={ariaLabel}
					aria-expanded={open}
					aria-controls={open ? `${ariaLabel.replace(/\W+/g, "-").toLowerCase()}-options` : undefined}
					disabled={disabled}
					className={cn(
						"flex h-control-form w-full items-center gap-2 rounded-md border border-border bg-[var(--color-bg-import-card)] px-3 text-[13px] text-foreground transition-colors hover:border-foreground/30 disabled:cursor-not-allowed disabled:opacity-50",
						className,
					)}
				>
					{selected?.private ? <Lock className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" /> : null}
					<span className={cn("min-w-0 flex-1 truncate", !selected && "text-muted-foreground")}>{selected?.label ?? placeholder}</span>
					<ChevronDown className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
				</Pressable>
			</PopoverTrigger>
			{/* Mounted with the picker, so the list saying it is loading is heard. */}
			<LiveStatus text={open && filtered.length === 0 && loading ? loadingText ?? t("common.loading") : ""} />
			<PopoverContent align="start" className="w-[var(--radix-popover-trigger-width)] min-w-56 overflow-hidden p-0">
				{searchable ? (
					<div className="flex h-10 items-center gap-2 border-b border-border px-3">
						<Search className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
						<input
							autoFocus
							aria-label={searchPlaceholder}
							placeholder={searchPlaceholder}
							value={search}
							onChange={(event) => setSearch(event.target.value)}
							className="h-full min-w-0 flex-1 bg-transparent text-[13px] text-foreground outline-none placeholder:text-muted-foreground"
						/>
					</div>
				) : null}
				<div
					id={`${ariaLabel.replace(/\W+/g, "-").toLowerCase()}-options`}
					role="listbox"
					aria-label={ariaLabel}
					// Stretches natively at its ends. Inside a modal dialog the scroll lock
					// would cancel this portaled list's wheel; lib/native-overscroll.ts lets
					// it through to the native scroll.
					className={cn(
						"p-1",
						NATIVE_BOUNCE_Y,
						fixedScroll ? "repository-picker-scrollbar max-h-72 overflow-y-auto" : "settings-thin-scrollbar max-h-72 overflow-y-auto",
					)}
				>
					{filtered.length === 0 && loading ? (
						<p className="flex items-center justify-center gap-2 px-3 py-5 text-xs text-muted-foreground">
							<Loader2 className="size-3.5 shrink-0 animate-spin" aria-hidden="true" />
							{loadingText ?? t("common.loading")}
						</p>
					) : filtered.length === 0 ? <p className="px-3 py-5 text-center text-xs text-muted-foreground">{t("common.noMatches", { defaultValue: "No matches" })}</p> : null}
					{filtered.map((option) => (
						<Pressable
							key={option.value}
							role="option"
							aria-selected={option.value === value}
							aria-label={option.label}
							onClick={() => { onChange(option.value); setOpen(false); }}
							className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-[13px] text-foreground hover:bg-accent focus-visible:bg-accent"
						>
							{option.private ? <Lock className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" /> : <span className="w-3.5 shrink-0" />}
							<span className="min-w-0 flex-1">
								<span className="block truncate">{option.label}</span>
								{option.description ? <span className="block truncate text-[11px] text-muted-foreground">{option.description}</span> : null}
							</span>
							{option.value === value ? <Check className="size-3.5 shrink-0 text-primary" aria-hidden="true" /> : null}
						</Pressable>
					))}
				</div>
				{action ? (
					<div className="border-t border-border p-1">
						<Pressable
							disabled={action.disabled}
							onClick={() => { setOpen(false); action.onSelect(); }}
							className="flex w-full items-center gap-2 rounded-md px-2 py-2 text-[13px] text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:bg-accent disabled:opacity-60"
						>
							<Plus className="size-3.5 shrink-0" aria-hidden="true" />
							<span className="min-w-0 flex-1 truncate">{action.label}</span>
						</Pressable>
					</div>
				) : null}
			</PopoverContent>
		</Popover>
	);
}
