import { Check } from "@/components/app-icons";
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { CatalogProvider } from "../../../hooks/useAgentAuth";
import { cn } from "../../../lib/utils";
import { Input } from "../../ui/input";
import { Skeleton } from "../../ui/skeleton";

/**
 * A harness's provider catalog as a search box over a short scrollable list,
 * inside the form rather than in a popover, so choosing a provider never
 * covers the panel around it. Arrow keys move through the list from the
 * search box and Enter picks; a click picks too.
 */
export function CatalogProviderPicker({ providers, value, onChange, disabled, labelledBy }: {
	providers: CatalogProvider[];
	value: string;
	onChange: (id: string) => void;
	disabled?: boolean;
	labelledBy: string;
}) {
	const { t } = useTranslation();
	const listId = useId();
	const listRef = useRef<HTMLDivElement>(null);
	const [query, setQuery] = useState("");
	const [active, setActive] = useState(0);
	const needle = query.trim().toLocaleLowerCase();
	const visible = useMemo(
		() => needle ? providers.filter((provider) => `${provider.name} ${provider.id}`.toLocaleLowerCase().includes(needle)) : providers,
		[providers, needle],
	);
	const activeIndex = Math.min(active, visible.length - 1);
	const optionId = (id: string) => `${listId}-${id}`;
	useEffect(() => { setActive(0); }, [needle]);
	// Keep the keyboard-highlighted option in view by scrolling the list only,
	// never the settings page around it.
	useEffect(() => {
		const list = listRef.current;
		const option = list?.querySelector<HTMLElement>(`[data-index="${activeIndex}"]`);
		if (!list || !option) return;
		if (option.offsetTop < list.scrollTop) list.scrollTop = option.offsetTop;
		else if (option.offsetTop + option.offsetHeight > list.scrollTop + list.clientHeight) list.scrollTop = option.offsetTop + option.offsetHeight - list.clientHeight;
	}, [activeIndex]);
	const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
		if (event.key === "ArrowDown" || event.key === "ArrowUp") {
			event.preventDefault();
			setActive((current) => Math.max(0, Math.min(visible.length - 1, Math.min(current, visible.length - 1) + (event.key === "ArrowDown" ? 1 : -1))));
		} else if (event.key === "Enter") {
			// Enter picks the highlighted provider instead of submitting the form.
			event.preventDefault();
			const provider = visible[activeIndex];
			if (provider) onChange(provider.id);
		}
	};

	return <div className="space-y-1.5">
		<Input
			role="combobox"
			aria-expanded="true"
			aria-controls={listId}
			aria-autocomplete="list"
			aria-labelledby={labelledBy}
			aria-activedescendant={visible[activeIndex] ? optionId(visible[activeIndex].id) : undefined}
			className="text-[13px] focus-visible:ring-2 focus-visible:ring-ring/30"
			value={query}
			disabled={disabled}
			autoComplete="off"
			spellCheck={false}
			placeholder={t("settings.harness.loginUi.catalogSearch")}
			onChange={(event) => setQuery(event.target.value)}
			onKeyDown={onKeyDown}
		/>
		<div ref={listRef} id={listId} role="listbox" aria-labelledby={labelledBy} className="relative max-h-48 overflow-y-auto overscroll-contain rounded-lg border border-border py-1">
			{visible.map((provider, index) => {
				const selected = provider.id === value;
				return <div
					key={provider.id}
					id={optionId(provider.id)}
					data-index={index}
					role="option"
					aria-selected={selected}
					aria-disabled={disabled || undefined}
					// Keep focus in the search box so typing and arrows keep working.
					onMouseDown={(event) => event.preventDefault()}
					onMouseMove={() => setActive(index)}
					onClick={() => { if (!disabled) onChange(provider.id); }}
					className={cn(
						"flex h-8 cursor-pointer items-center justify-between gap-3 px-3 text-control leading-5 text-settings-label",
						index === activeIndex && "bg-(--color-interactive-hover)",
						selected && "bg-(--color-interactive-active) font-medium",
						disabled && "cursor-default opacity-60",
					)}
				>
					<span className="truncate">{provider.name}</span>
					<span className="flex shrink-0 items-center gap-2 text-xs tabular-nums text-settings-muted">
						{provider.models > 0 ? t("settings.harness.loginUi.catalogModels", { count: provider.models }) : null}
						{selected ? <Check className="size-3.5 text-settings-label" aria-hidden="true" /> : null}
					</span>
				</div>;
			})}
			{visible.length === 0 ? <p className="px-3 py-1.5 text-xs text-settings-muted">{t("settings.harness.loginUi.catalogEmpty")}</p> : null}
		</div>
	</div>;
}

/**
 * Stands in for the picker while the catalog loads: the same search box and
 * list footprint, so nothing moves when the providers arrive. The loading
 * message is for screen readers only; the placeholder rows show it visually.
 */
export function CatalogProviderPickerSkeleton({ label }: { label: string }) {
	return <div className="space-y-1.5" aria-busy="true">
		<span role="status" className="sr-only">{label}</span>
		<Skeleton className="h-control-form w-full bg-input/50" />
		<div className="space-y-1 rounded-lg border border-border p-2" aria-hidden="true">
			{["w-2/5", "w-1/3", "w-1/2", "w-1/4", "w-2/5"].map((width, index) => <div key={index} className="flex h-6 items-center justify-between gap-3 px-1">
				<Skeleton className={`h-3 ${width}`} />
				<Skeleton className="h-3 w-14" />
			</div>)}
		</div>
	</div>;
}
