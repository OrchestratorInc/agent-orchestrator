import { Check, Link2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import type { LinkAccess } from "./linkAccess";

/** How long the button says "Link copied". */
export const COPIED_MS = 2_000;

export type CopyOutcome = { url: string; copied: boolean };

/**
 * Copies the link general access describes. A link this device made is
 * copied as it is; one made elsewhere can't be shown again, so the button
 * makes a new link with the same access ("Copy new link"). With link access
 * off there is nothing to copy, and the button says how to get one.
 */
export async function copyGeneralLink(access: LinkAccess): Promise<CopyOutcome> {
	const url = access.url ?? await access.copyNew.mutateAsync();
	try {
		await aoBridge.clipboard.writeText(url);
		return { url, copied: true };
	} catch {
		return { url, copied: false };
	}
}

export function CopyLinkButton({ access, people, onCopied, buttonRef }: {
	access: LinkAccess;
	/** Whether people can be added in this dialog, for the hint when link access is off. */
	people: boolean;
	/** Announces the copy, with who the link lets in. */
	onCopied: () => void;
	buttonRef?: React.Ref<HTMLButtonElement>;
}) {
	const { t } = useTranslation();
	const [state, setState] = useState<"idle" | "copied">("idle");
	// The URL to select by hand when the clipboard refused it.
	const [fallback, setFallback] = useState<string>();
	const timer = useRef<number | undefined>(undefined);
	useEffect(() => () => window.clearTimeout(timer.current), []);
	const off = !access.broadest;
	const fresh = !off && !access.url && !access.urlsLoading;
	const pending = access.copyNew.isPending;

	const copy = async () => {
		setFallback(undefined);
		let outcome: CopyOutcome;
		try {
			outcome = await copyGeneralLink(access);
		} catch {
			// The error shows with the dialog's others.
			return;
		}
		if (!outcome.copied) {
			setFallback(outcome.url);
			return;
		}
		setState("copied");
		onCopied();
		window.clearTimeout(timer.current);
		timer.current = window.setTimeout(() => setState("idle"), COPIED_MS);
	};

	const label = state === "copied" ? t("share.linkCopied") : fresh ? t("share.copyNewLink") : t("share.copyLink");
	const hint = off ? (people ? t("share.copyNeedsLinkAccess") : t("share.copyNeedsLinkAccessNoPeople")) : fresh ? t("share.copyNewLinkHint") : undefined;
	const button = (
		<Button
			ref={buttonRef}
			type="button"
			variant="footer"
			// Wide enough for every label, so the footer doesn't move as it changes.
			className="min-w-34 gap-1.5"
			disabled={off || pending}
			aria-disabled={off || undefined}
			onClick={() => void copy()}
			data-share-copy-link={state === "copied" ? "copied" : fresh ? "new" : off ? "off" : "ready"}
		>
			{state === "copied" ? <Check className="size-3.5" aria-hidden="true" /> : <Link2 className="size-3.5" aria-hidden="true" />}
			{label}
		</Button>
	);
	return (
		<div className="flex min-w-0 flex-col items-start gap-1.5">
			{hint ? (
				<Tooltip>
					<TooltipTrigger asChild>
						{/* A disabled button gets no pointer events; the wrapper keeps its tooltip reachable. */}
						<span className="inline-flex" tabIndex={off ? 0 : undefined}>{button}</span>
					</TooltipTrigger>
					<TooltipContent className="max-w-64">{hint}</TooltipContent>
				</Tooltip>
			) : button}
			{fallback ? (
				<div className="flex w-80 max-w-full flex-col gap-1" data-share-copy-fallback="">
					<Input readOnly value={fallback} aria-label={t("share.link")} className="font-mono text-xs" autoFocus onFocus={(event) => event.currentTarget.select()} />
					<p role="alert" className="text-caption leading-4 text-error">{t("share.copyFailed")}</p>
				</div>
			) : null}
		</div>
	);
}
