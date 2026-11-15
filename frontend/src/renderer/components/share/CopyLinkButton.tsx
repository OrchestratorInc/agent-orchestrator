import { Check, Link2 } from "@/components/app-icons";
import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import type { LinkAccess } from "./linkAccess";

export const COPIED_MS = 2_000;

export function canCopy(access: LinkAccess): boolean {
	const { mode, busy } = access.copy;
	return !busy && (mode === "ready" || mode === "new" || mode === "replace");
}

export function CopyLinkButton({ access, people, onCopied, onAsk, buttonRef, size, fallbackHost }: {
	access: LinkAccess;
	people: boolean;
	onCopied: () => void;
	onAsk: () => Promise<string | undefined>;
	buttonRef?: React.Ref<HTMLButtonElement>;
	size?: "sm";
	fallbackHost?: HTMLElement | null;
}) {
	const { t } = useTranslation();
	const hintId = useId();
	const [state, setState] = useState<"idle" | "copied">("idle");
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
					<span id={hintId} className="sr-only">{hint}</span>
				</>
			) : button}
			{fallback ? (fallbackHost ? createPortal(copyFallback, fallbackHost) : copyFallback) : null}
		</div>
	);
}
