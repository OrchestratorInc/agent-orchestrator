import { useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../ui/tooltip";

/**
 * The Save bar of a project settings page that saves on submit. It is pinned to
 * the bottom of the settings dialog's scrolling body (whose padding it spans) so
 * Save stays in view however long the page is.
 */
export function SettingsSaveFooter({ dirty, disabled, error }: { dirty: boolean; disabled: boolean; error?: string }) {
	const { t } = useTranslation();
	return <div className="sticky -bottom-(--size-modal-padding) z-[1] -mx-(--size-modal-padding) -mb-(--size-modal-padding) border-t border-border bg-card px-(--size-modal-padding) py-4" data-settings-save-footer="">
		{/* The error shares the Save row, clamped to the button's height, so the bar
		    (and its divider) never moves when validation comes and goes. */}
		<div className="flex items-center justify-end gap-3">
			{error ? <SaveFooterError error={error} /> : null}
			<Button disabled={!dirty || disabled} type="submit">{t("settings.project.saveChanges")}</Button>
		</div>
	</div>;
}

/**
 * The footer's error, clamped to two lines. A screen reader hears all of it
 * (the clamp only hides it visually); when the clamp cuts it short, it takes
 * keyboard focus and shows the whole message in a tooltip on focus or hover,
 * over the page, so the bar keeps its height.
 */
function SaveFooterError({ error }: { error: string }) {
	const ref = useRef<HTMLParagraphElement>(null);
	const [clipped, setClipped] = useState(false);
	const [open, setOpen] = useState(false);
	useLayoutEffect(() => {
		const node = ref.current;
		if (!node) return;
		const measure = () => setClipped(node.scrollHeight > node.clientHeight + 1);
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(node);
		return () => observer.disconnect();
	}, [error]);
	// The tooltip is always mounted, only held shut while the text fits, so the
	// alert is never remounted (and announced again) when the clamp changes. It
	// stays controlled: Radix does not support switching.
	return <TooltipProvider>
		<Tooltip open={clipped && open} onOpenChange={setOpen}>
			<TooltipTrigger asChild>
				<p
					ref={ref}
					role="alert"
					tabIndex={clipped ? 0 : undefined}
					className="line-clamp-2 min-w-0 flex-1 rounded-sm text-xs leading-4 text-error outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
				>
					{error}
				</p>
			</TooltipTrigger>
			<TooltipContent side="top" className="max-w-96 break-words">{error}</TooltipContent>
		</Tooltip>
	</TooltipProvider>;
}
