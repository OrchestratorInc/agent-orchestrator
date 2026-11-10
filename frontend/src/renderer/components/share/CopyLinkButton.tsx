import { Check, Link2 } from "@/components/app-icons";
import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import type { LinkAccess } from "./linkAccess";

/** How long the button says "Link copied". */
export const COPIED_MS = 2_000;

/** Whether Copy link can act now: there is a link, nothing is changing the links, and this device's URLs are known. */
export function canCopy(access: LinkAccess): boolean {
	const { mode, busy } = access.copy;
	return !busy && (mode === "ready" || mode === "new" || mode === "replace");
}

/**
 * Copies the link general access describes. A link this device made is
 * copied as it is; one made elsewhere can't be shown again, so the button
 * makes a new link with the same access ("Copy new link"), asking first
 * when that link can write, as turning writing on does. A Trusted link, which
 * no new link can match, is replaced, after asking. With link access off there is nothing to copy, and the button says
 * how to get one. It stays focusable while it can't copy, so focus never
 * drops out of the dialog and the reason can be read.
 */
export function CopyLinkButton({ access, people, onCopied, onAsk, buttonRef, size, fallbackHost }: {
	access: LinkAccess;
	/** Whether people can be added in this dialog, for the hint when link access is off. */
	people: boolean;
	/** Announces the copy, with who the link lets in. */
	onCopied: () => void;
	/**
	 * Asks before making a link that can write: one replacing a Trusted link
	 * made elsewhere, or a copy of a Can write one. Resolves to the new URL, or undefined.
	 */
	onAsk: () => Promise<string | undefined>;
	buttonRef?: React.Ref<HTMLButtonElement>;
	size?: "sm";
	/** Where the link to select by hand shows when the clipboard refuses it; under the button by default. */
	fallbackHost?: HTMLElement | null;
}) {
	const { t } = useTranslation();
	const hintId = useId();
	const [state, setState] = useState<"idle" | "copied">("idle");
	// The URL to select by hand when the clipboard refused it.
	const [fallback, setFallback] = useState<string>();
	const timer = useRef<number | undefined>(undefined);
	useEffect(() => () => window.clearTimeout(timer.current), []);
	const { mode } = access.copy;
	const available = canCopy(access);

	const copy = async () => {
		if (!available) return;
		setFallback(undefined);
		let url: string | undefined;
		try {
			url = mode === "ready"
				? access.url
				: mode === "new" && access.broadest?.role !== "editor" ? await access.copyNew.mutateAsync() : await onAsk();
		} catch {
			// The error shows with the dialog's others.
			return;
		}
		if (!url) return;
		try {
			await aoBridge.clipboard.writeText(url);
		} catch {
			setFallback(url);
			return;
		}
		setState("copied");
		onCopied();
		window.clearTimeout(timer.current);
		timer.current = window.setTimeout(() => setState("idle"), COPIED_MS);
	};

	const label = state === "copied" ? t("share.linkCopied") : mode === "new" || mode === "replace" ? t("share.copyNewLink") : t("share.copyLink");
	const hint = mode === "off"
		? people ? t("share.copyNeedsLinkAccess") : t("share.copyNeedsLinkAccessNoPeople")
		: mode === "new" ? t("share.copyNewLinkHint") : mode === "replace" ? t("share.copyReplaceHint") : undefined;
	const copyFallback = (
		<div className="flex w-80 max-w-full flex-col gap-1" data-share-copy-fallback="">
			<Input readOnly value={fallback} aria-label={t("share.link")} className="font-mono text-xs" autoFocus onFocus={(event) => event.currentTarget.select()} />
			<p role="alert" className="text-caption leading-4 text-error">{t("share.copyFailed")}</p>
		</div>
	);
	const button = (
		<Button
			ref={buttonRef}
			type="button"
			variant="secondary"
			size={size}
			// Wide enough for every label, so the footer doesn't move as it changes.
			className="min-w-34 gap-1.5 aria-disabled:cursor-default aria-disabled:opacity-50"
			aria-disabled={!available || undefined}
			aria-describedby={hint ? hintId : undefined}
			onClick={() => void copy()}
			data-share-copy-link={state === "copied" ? "copied" : mode}
			data-share-copy-busy={access.copy.busy || undefined}
		>
			{state === "copied" ? <Check className="size-3.5" aria-hidden="true" /> : <Link2 className="size-3.5" aria-hidden="true" />}
			{label}
		</Button>
	);
	return (
		<div className="flex min-w-0 flex-col items-start gap-1.5">
			{hint ? (
				<>
					<Tooltip>
						<TooltipTrigger asChild>{button}</TooltipTrigger>
						<TooltipContent className="max-w-64">{hint}</TooltipContent>
					</Tooltip>
					{/* The reason, read with the button whether or not the tooltip is showing. */}
					<span id={hintId} className="sr-only">{hint}</span>
				</>
			) : button}
			{fallback ? (fallbackHost ? createPortal(copyFallback, fallbackHost) : copyFallback) : null}
		</div>
	);
}
