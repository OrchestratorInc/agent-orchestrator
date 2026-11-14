// "Continue in Chrome" for pages that refuse the Browser panel. The main
// process recognises the refusal (browser-site-block.ts) and reports it on
// the tab's nav state; this row offers the way out at that moment, and the
// browser menu offers the same action for any page.

import { ShieldAlert, X } from "@/components/app-icons";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import type { BrowserSiteBlock } from "../../shared/browser-site-block";
import { Button } from "./ui/button";

export type UserBrowserInfo = { name: string; chrome: boolean };

let userBrowserLookup: Promise<UserBrowserInfo | null> | undefined;

/** The person's own browser, asked once per app run. */
export function useUserBrowser(): UserBrowserInfo | null {
	const [browser, setBrowser] = useState<UserBrowserInfo | null>(null);
	useEffect(() => {
		let live = true;
		userBrowserLookup ??= (window.ao?.browser.userBrowser?.() ?? Promise.resolve(null)).catch(() => null);
		void userBrowserLookup.then((found) => {
			if (live) setBrowser(found);
		});
		return () => {
			live = false;
		};
	}, []);
	return browser;
}

export function resetUserBrowserForTest(): void {
	userBrowserLookup = undefined;
	dismissedHosts.clear();
}

/** "Continue in Chrome", or the default browser's name, or a generic label. */
export function continueLabel(t: TFunction, browser: UserBrowserInfo | null, verb: "continue" | "open"): string {
	if (verb === "continue") {
		return browser?.name ? t("browser.continueElsewhere.continueIn", { browser: browser.name }) : t("browser.continueElsewhere.continueInYourBrowser");
	}
	return browser?.name ? t("browser.continueElsewhere.openIn", { browser: browser.name }) : t("browser.continueElsewhere.openInYourBrowser");
}

type OpenState = { status: "idle" | "opening" } | { status: "opened"; name: string } | { status: "failed" };

/** Open the view's page in the person's browser, tracking the outcome. */
export function useOpenInUserBrowser(viewId: string): { state: OpenState; open: () => Promise<boolean>; reset: () => void } {
	const [state, setState] = useState<OpenState>({ status: "idle" });
	const open = useCallback(async () => {
		if (!viewId) return false;
		setState({ status: "opening" });
		try {
			const used = await window.ao?.browser.openInUserBrowser?.(viewId);
			setState({ status: "opened", name: used?.name ?? "" });
			return true;
		} catch {
			setState({ status: "failed" });
			return false;
		}
	}, [viewId]);
	const reset = useCallback(() => setState({ status: "idle" }), []);
	return { state, open, reset };
}

// Hosts the person waved off this run: the bar never nags twice for one site.
const dismissedHosts = new Set<string>();

const OPENED_VISIBLE_MS = 2_500;

export function BrowserSiteBlockRow({ viewId, siteBlock }: { viewId: string; siteBlock: BrowserSiteBlock | undefined }) {
	const { t } = useTranslation();
	const prefersReducedMotion = useReducedMotion();
	const browser = useUserBrowser();
	const { state, open, reset } = useOpenInUserBrowser(viewId);
	const host = siteBlock?.host;
	// Read during render, so a dismissed site never flashes back for a frame.
	const [, rerender] = useState(0);
	const dismissed = host ? dismissedHosts.has(host) : false;
	const doneTimer = useRef<number | null>(null);

	// A different site (or none) starts fresh.
	useEffect(() => {
		reset();
	}, [host, reset]);

	useEffect(() => () => {
		if (doneTimer.current !== null) window.clearTimeout(doneTimer.current);
	}, []);

	const dismiss = useCallback(() => {
		if (host) dismissedHosts.add(host);
		rerender((count) => count + 1);
	}, [host]);

	const continueElsewhere = useCallback(async () => {
		if (!(await open())) return;
		// Leave the confirmation up long enough to read, then fold away.
		doneTimer.current = window.setTimeout(() => {
			doneTimer.current = null;
			dismiss();
		}, OPENED_VISIBLE_MS);
	}, [dismiss, open]);

	const visible = Boolean(siteBlock) && !dismissed;
	const message = !siteBlock
		? ""
		: siteBlock.reason === "signin"
			? t("browser.continueElsewhere.reason.signin")
			: siteBlock.reason === "verification"
				? t("browser.continueElsewhere.reason.verification", { host: siteBlock.host })
				: t("browser.continueElsewhere.reason.blocked", { host: siteBlock.host });

	return (
		<AnimatePresence initial={false}>
			{visible ? (
				<motion.div
					key="site-block"
					animate={{ height: "auto", opacity: 1 }}
					className="browser-panel__notice-row"
					data-testid="browser-site-block"
					exit={prefersReducedMotion ? undefined : { height: 0, opacity: 0 }}
					initial={prefersReducedMotion ? false : { height: 0, opacity: 0 }}
					role="status"
					transition={prefersReducedMotion ? { duration: 0 } : { duration: 0.18, ease: [0.22, 1, 0.36, 1] }}
				>
					<ShieldAlert aria-hidden="true" className="size-icon-base shrink-0 text-status-needs-you" />
					<span className="min-w-0 flex-1 truncate" title={message}>
						{state.status === "opened"
							? state.name
								? t("browser.continueElsewhere.openedIn", { browser: state.name })
								: t("browser.continueElsewhere.openedInYourBrowser")
							: message}
					</span>
					{state.status === "failed" ? (
						<span className="shrink-0 text-destructive">{t("browser.continueElsewhere.failed")}</span>
					) : null}
					{state.status === "opened" ? null : (
						<Button
							className="shrink-0"
							disabled={state.status === "opening"}
							onClick={() => void continueElsewhere()}
							size="sm"
							type="button"
							variant="primary"
						>
							{continueLabel(t, browser, "continue")}
						</Button>
					)}
					<Button
						aria-label={t("browser.continueElsewhere.dismiss")}
						className="shrink-0"
						onClick={dismiss}
						size="icon-sm"
						type="button"
						variant="quiet"
					>
						<X aria-hidden="true" className="size-icon-base" />
					</Button>
				</motion.div>
			) : null}
		</AnimatePresence>
	);
}
