import type { HTMLAttributes, InputHTMLAttributes, ReactNode, Ref } from "react";
import { ChevronDown, ChevronUp, Search, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "./ui/button";

/** Shared presentation for contextual find; each viewer owns its search engine. */
export function FindBar({ label, rootRef, inputRef, inputProps, status, disabled, onPrevious, onNext, onClose, children, ...props }: HTMLAttributes<HTMLDivElement> & {
	label: string;
	rootRef?: Ref<HTMLDivElement>;
	inputRef: Ref<HTMLInputElement>;
	inputProps: InputHTMLAttributes<HTMLInputElement>;
	status: ReactNode;
	disabled: boolean;
	onPrevious: () => void;
	onNext: () => void;
	onClose: () => void;
}) {
	const { t } = useTranslation();
	return <div {...props} ref={rootRef} role="search" aria-label={label} className="z-overlay absolute top-3 right-3 flex w-[320px] max-w-[calc(100%-24px)] shrink-0 items-center gap-1 rounded-lg border border-(--color-border-overlay) bg-overlay px-3 py-2 text-sm leading-5 shadow-(--shadow-popover) focus-within:ring-1 focus-within:ring-ring/40">
		<Search aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
		<input {...inputProps} ref={inputRef} type="search" aria-label={label} placeholder={label} className="min-w-0 flex-1 bg-transparent px-1 outline-none [&::-webkit-search-cancel-button]:hidden" />
		{status}
		<div className="flex shrink-0 items-center gap-1 border-l border-border pl-2">
			<Button type="button" variant="quiet" size="icon-sm" aria-label={t("browser.find.previous")} disabled={disabled} onClick={onPrevious} className="focus-visible:ring-1 focus-visible:ring-ring"><ChevronUp className="size-3.5" /></Button>
			<Button type="button" variant="quiet" size="icon-sm" aria-label={t("browser.find.next")} disabled={disabled} onClick={onNext} className="focus-visible:ring-1 focus-visible:ring-ring"><ChevronDown className="size-3.5" /></Button>
			<Button type="button" variant="quiet" size="icon-sm" aria-label={t("browser.find.close")} onClick={onClose} className="focus-visible:ring-1 focus-visible:ring-ring"><X className="size-3.5" /></Button>
		</div>
		{children}
	</div>;
}
