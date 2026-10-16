import { useQueryClient } from "@tanstack/react-query";
import { Check, ExternalLink, Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useCloudBilling, cloudBillingQueryKey } from "../../hooks/useCloudBilling";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudGate } from "../../hooks/useCloudGate";
import { aoBridge } from "../../lib/bridge";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import type { CloudCpBillingSummary, CloudCpPlanLimits, CloudCpPlanOffer } from "../../lib/cloud-cp/types";
import { useCloudSession } from "../../lib/cloud-session";
import { reportUpgradeViewed } from "../../lib/cloud-telemetry";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../ui/dialog";
import { useResolvedTheme, useUiStore } from "../../stores/ui-store";
import { BayerVortex } from "./BayerVortex";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

export { cloudBillingQueryKey };

/** After Checkout or the Portal opens, poll briefly so the new plan shows without a manual refresh. */
const POLL_WINDOW_MS = 3 * 60_000;
const RECOMMENDED_PLAN = "starter";
/** Enterprise is sold by the founders, not through Checkout. */
const ENTERPRISE_BOOKING_URL = "https://cal.com/agentwrapper/ao-design-partner?overlayCalendar=true";

/**
 * Upgrade: the AO Cloud plans (Free, Starter, Pro, Enterprise), the org's usage against its
 * plan, and the actions an admin takes on it. Payment happens on Stripe's pages
 * in the system browser, which return to the app afterwards.
 */
export function BillingSection({ titleHidden }: { titleHidden?: boolean }) {
	const { cloudEnabled } = useCloudGate();
	if (!cloudEnabled) return null;
	return <BillingSectionInner titleHidden={titleHidden} />;
}

function BillingSectionInner({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const { status } = useCloudSession();
	const { client } = useCloudCp();
	const queryClient = useQueryClient();
	const [pollUntil, setPollUntil] = useState(0);
	const billing = useCloudBilling({ pollUntil });
	const orgId = billing.orgId;
	const offering = billing.offering;
	// Plan actions (Checkout, Portal, reset) are for org owners and admins; the
	// control plane refuses them for anyone else.
	const manage = offering.canManageBilling;
	const [pending, setPending] = useState<string | null>(null);
	// Which plan card opened the billing portal, so only that card shows the spinner.
	const [portalFor, setPortalFor] = useState("");
	useEffect(() => {
		if (pending === null) setPortalFor("");
	}, [pending]);
	const [actionError, setActionError] = useState<string | null>(null);
	const [confirmReset, setConfirmReset] = useState(false);
	// The Upgrade page is the paywall's landing: count each visit once mounted.
	useEffect(() => {
		if (status === "authenticated") reportUpgradeViewed("settings");
	}, [status]);

	const title = t("settings.billing.title");
	if (status !== "authenticated") {
		return <Notice title={title} titleHidden={titleHidden} text={t("settings.billing.signIn")} />;
	}
	// Plans exist only for self-serve orgs. A contract org is billed outside the
	// app, and a deployment without Stripe has nothing to buy.
	if (offering.loaded && !offering.selfServeBilling) {
		return <Notice title={title} titleHidden={titleHidden} text={offering.billingMode === "contract" ? t("settings.billing.contract") : t("settings.billing.disabled")} />;
	}
	if (!offering.isError && (!offering.loaded || billing.isPending)) {
		return (
			<SettingsSection title={title} sectionId="billing" titleHidden={titleHidden} grouped>
				<p className="settings-row-bar justify-start gap-2 text-sm text-settings-muted">
					<Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />
					{t("settings.billing.loading")}
				</p>
			</SettingsSection>
		);
	}
	if (billing.isError || !billing.data) {
		return (
			<SettingsSection title={title} sectionId="billing" titleHidden={titleHidden} grouped>
				<div className="settings-row-bar">
					<p className="text-xs text-error">{t("settings.billing.loadFailed")}</p>
					<Button variant="ghost" size="sm" className="text-settings-muted hover:text-settings-label" onClick={() => void billing.refetch()}>{t("settings.billing.retry")}</Button>
				</div>
			</SettingsSection>
		);
	}
	const summary = billing.data;
	if (!summary.enabled || (summary.billingMode !== undefined && summary.billingMode !== "self_serve")) {
		return <Notice title={title} titleHidden={titleHidden} text={t("settings.billing.disabled")} />;
	}

	const runAction = async (name: string, action: () => Promise<void>) => {
		setPending(name);
		setActionError(null);
		try {
			await action();
		} catch (error) {
			setActionError(billingActionError(error, t));
		} finally {
			setPending(null);
		}
	};
	const openExternal = (name: string, link: () => Promise<{ url: string }>) =>
		runAction(name, async () => {
			const { url } = await link();
			await aoBridge.app.openExternal(url);
			setPollUntil(Date.now() + POLL_WINDOW_MS);
		});
	const openCheckout = (plan: string) => openExternal(`checkout:${plan}`, () => client.createBillingCheckout(orgId, plan));
	const openPortal = () => openExternal("portal", () => client.createBillingPortal(orgId));
	const openCancel = () => openExternal("portal:cancel", () => client.createBillingPortal(orgId, "cancel"));
	const reset = () =>
		runAction("reset", async () => {
			const next = await client.resetBillingUsage(orgId);
			queryClient.setQueryData([...cloudBillingQueryKey, orgId], { ...summary, ...next, plans: summary.plans });
			setConfirmReset(false);
		});

	const currentPlan = summary.entitled && !summary.exempt ? (summary.limits?.plan ?? summary.plan) : "free";
	const subscribed = currentPlan !== "free";
	const lapsed = summary.subscriptionStatus === "past_due" || summary.subscriptionStatus === "unpaid";
	// A plan set to cancel stays current until it ends, then the org moves to Free.
	const endsAt = subscribed && summary.cancelAt ? formatDay(summary.cancelAt) : "";
	const offers = summary.plans ?? [];
	const limits = summary.limits;
	const usage = summary.usage;
	const resetsLeft = usage ? Math.max(0, usage.manualResetsAllowed - usage.manualResetsUsed) : 0;
	const spinner = <Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />;

	return (
		<>
			<SettingsSection title={title} sectionId="billing" titleHidden={titleHidden}>
				{summary.exempt ? <p className="settings-row-bar text-sm text-settings-muted">{t("settings.billing.exempt")}</p> : null}
				{endsAt ? (
					<div className="settings-row-bar">
						<p className="min-w-0 text-pretty text-xs text-settings-muted">
							{t("settings.billing.cancelNotice", { plan: planName(currentPlan), date: endsAt })}
						</p>
						{manage ? (
							<Button size="sm" variant="ghost" className={ghostAction} disabled={pending !== null} onClick={() => void openPortal()}>
								{pending === "portal" ? spinner : <ExternalLink className="size-icon-sm" aria-hidden="true" />}
								{t("settings.billing.keepPlan", { plan: planName(currentPlan) })}
							</Button>
						) : null}
					</div>
				) : null}
				{lapsed && manage ? (
					<div role="alert" className="settings-row-bar">
						<p className="min-w-0 text-pretty text-xs text-error">{t("settings.billing.paymentFailed")}</p>
						<Button size="sm" disabled={pending !== null} onClick={() => void openPortal()}>
							{pending === "portal" ? spinner : <ExternalLink className="size-icon-sm" aria-hidden="true" />}
							{t("settings.billing.updatePayment")}
						</Button>
					</div>
				) : null}
				<div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4">
					<PlanCard
						plan="free"
						name={t("settings.billing.planFree")}
						amount="$0"
						interval={t("settings.billing.intervalMonth")}
						features={[
							t("settings.billing.freeLocal"),
							t("settings.billing.freeAgents"),
							t("settings.billing.freeOrchestrator"),
							t("settings.billing.freeNoCloud"),
							t("settings.billing.freeHosts"),
						]}
						current={currentPlan === "free"}
						action={endsAt ? (
							<span className="text-control text-settings-muted">{t("settings.billing.freeStarts", { date: endsAt })}</span>
						) : subscribed && manage ? (
							<Button size="sm" variant="secondary" className="w-full" disabled={pending !== null} onClick={() => void openCancel()}>
								{pending === "portal:cancel" ? spinner : null}
								{t("settings.billing.downgrade")}
							</Button>
						) : null}
						t={t}
					/>
					{offers.map((offer) => {
						const isCurrent = currentPlan === offer.name;
						const recommended = !subscribed && offer.name === RECOMMENDED_PLAN;
						const busy = pending === `checkout:${offer.name}` || (pending === "portal" && portalFor === offer.name);
						const openPlanPortal = () => {
							setPortalFor(offer.name);
							void openPortal();
						};
						let label = t("settings.billing.upgrade");
						let onClick: () => void = () => void openCheckout(offer.name);
						if (isCurrent) {
							label = t("settings.billing.manage");
							onClick = openPlanPortal;
						} else if (subscribed) {
							label = t("settings.billing.switchPlan");
							onClick = openPlanPortal;
						}
						return (
							<PlanCard
								key={offer.name}
								plan={offer.name}
								name={planName(offer.name)}
								{...offerPrice(offer, t)}
								{...(offer.limits ? planFeatures(offer.name, offer.limits, t) : { features: [] })}
								current={isCurrent}
								recommended={recommended}
								action={manage ? (
									<Button size="sm" variant={recommended ? "primary" : "secondary"}
										className="w-full" disabled={pending !== null} onClick={onClick}>
										{busy ? spinner : isCurrent ? <ExternalLink className="size-icon-sm" aria-hidden="true" /> : null}
										{label}
									</Button>
								) : null}
								t={t}
							/>
						);
					})}
					<PlanCard
						plan="enterprise"
						name={t("settings.billing.planEnterprise")}
						amount={t("settings.billing.enterprisePrice")}
						features={[
							t("settings.billing.enterpriseSessions"),
							t("settings.billing.enterpriseOrchestrators"),
							t("settings.billing.enterpriseHours"),
							t("settings.billing.enterpriseCoder"),
							t("settings.billing.enterpriseSso"),
							t("settings.billing.enterpriseSupport"),
						]}
						current={false}
						action={(
							<Button size="sm" variant="secondary" className="w-full" onClick={() => void aoBridge.app.openExternal(ENTERPRISE_BOOKING_URL)}>
								<ExternalLink className="size-icon-sm" aria-hidden="true" />
								{t("settings.billing.talkToFounder")}
							</Button>
						)}
						t={t}
					/>
				</div>
				{manage ? null : <p className="px-1 text-xs text-muted-foreground">{t("settings.billing.adminOnly")}</p>}
				{actionError && !confirmReset ? <p role="alert" className="px-1 text-xs text-destructive">{actionError}</p> : null}
			</SettingsSection>
			{subscribed && limits && usage ? (
					<SettingsSection title={t("settings.billing.usage")} grouped>
						<UsageRow
							label={t("settings.billing.windowUsage", { hours: usage.windowHours })}
							used={usage.windowUsedMinutes}
							limit={usage.windowLimitMinutes}
							resetsAt={usage.windowResetsAt}
							t={t}
						/>
						<UsageRow
							label={t("settings.billing.weeklyUsage")}
							used={usage.weeklyUsedMinutes}
							limit={usage.weeklyLimitMinutes}
							resetsAt={usage.weeklyResetsAt}
							absoluteReset
							t={t}
						/>
						<SettingsRow label={t("settings.billing.runningSessions")} description={t("settings.billing.runningDetail", {
							orchestrators: usage.runningOrchestrators, orchestratorSlots: limits.orchestratorSlots,
							workers: usage.runningWorkers, workerSlots: runningSlots(limits) - limits.orchestratorSlots,
						})}>
							<Count used={usage.runningOrchestrators + usage.runningWorkers} limit={runningSlots(limits)} />
						</SettingsRow>
						<SettingsRow label={t("settings.billing.totalSessions")} description={t("settings.billing.totalDetail")}>
							<Count used={usage.activeOrchestrators + usage.activeWorkers} limit={limits.maxActiveSandboxes} />
						</SettingsRow>
						{manage ? <SettingsRow
							label={t("settings.billing.reset")}
							description={resetsLeft > 0
								? t("settings.billing.resetDetail", { left: resetsLeft, allowed: usage.manualResetsAllowed })
								: t("settings.billing.resetNoneLeft", { date: formatDate(usage.manualResetsRenewAt) })}
						>
							<Button size="sm" variant="ghost" className={ghostAction} disabled={resetsLeft === 0 || pending !== null} onClick={() => setConfirmReset(true)}>
								{t("settings.billing.resetNow")}
							</Button>
						</SettingsRow> : null}
						<p className="settings-content-row text-pretty text-control leading-4 text-settings-muted">{t("settings.billing.usageNote")}</p>
					</SettingsSection>
			) : null}
			<Dialog open={confirmReset} onOpenChange={(open) => { if (pending !== "reset") setConfirmReset(open); }}>
				<DialogContent>
					<DialogHeader>
						<DialogTitle>{t("settings.billing.resetConfirmTitle")}</DialogTitle>
						<DialogDescription>{t("settings.billing.resetConfirmBody")}</DialogDescription>
					</DialogHeader>
					{actionError ? <p role="alert" className="text-xs text-destructive">{actionError}</p> : null}
					<DialogFooter>
						<Button variant="ghost" size="sm" disabled={pending === "reset"} onClick={() => setConfirmReset(false)}>{t("settings.billing.cancel")}</Button>
						<Button size="sm" disabled={pending === "reset"} onClick={() => void reset()}>
							{pending === "reset" ? spinner : null}
							{t("settings.billing.resetConfirm")}
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</>
	);
}

function Notice({ title, titleHidden, text }: { title: string; titleHidden?: boolean; text: string }) {
	return (
		<SettingsSection title={title} sectionId="billing" titleHidden={titleHidden} grouped>
			<p className="settings-row-bar text-sm text-settings-muted">{text}</p>
		</SettingsSection>
	);
}

/** Muted ghost row action, the settings default for every non-primary action. */
const ghostAction = "text-settings-muted hover:text-settings-label";

/** A theme token as a hex string, resolved from the live document so every theme style is followed. */
function tokenHex(name: string): string | null {
	try {
		const probe = document.createElement("span");
		probe.style.color = `var(${name})`;
		document.body.appendChild(probe);
		const computed = getComputedStyle(probe).color;
		probe.remove();
		if (!computed) return null;
		const channels = (rgb: number[]) => `#${rgb.map((channel) => Math.round(channel).toString(16).padStart(2, "0")).join("")}`;
		// Computed colors are rgb() for sRGB tokens; anything else (color-mix, oklch) goes through a canvas.
		const direct = /^rgba?\((\d+),\s*(\d+),\s*(\d+)/.exec(computed);
		if (direct) return channels([Number(direct[1]), Number(direct[2]), Number(direct[3])]);
		const canvas = document.createElement("canvas");
		canvas.width = 1;
		canvas.height = 1;
		const ctx = canvas.getContext("2d", { willReadFrequently: true });
		if (!ctx) return null;
		ctx.fillStyle = computed;
		ctx.fillRect(0, 0, 1, 1);
		const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
		return channels([r, g, b]);
	} catch {
		// A decorative effect must never break the page: no color, no glow.
		return null;
	}
}

/** The brand logo blue with its hue kept and its saturation raised: the logo token is a muted tone meant for text. */
function saturatedBrand(): string | null {
	const hex = tokenHex("--color-brand-logo");
	if (!hex) return null;
	const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
	const max = Math.max(r, g, b);
	const min = Math.min(r, g, b);
	const d = max - min;
	let h = 210;
	if (d > 0) {
		if (max === r) h = ((g - b) / d) % 6;
		else if (max === g) h = (b - r) / d + 2;
		else h = (r - g) / d + 4;
		h *= 60;
		if (h < 0) h += 360;
	}
	const l = 0.36;
	const sat = 0.9;
	const c = (1 - Math.abs(2 * l - 1)) * sat;
	const x = c * (1 - Math.abs(((h / 60) % 2) - 1));
	const m = l - c / 2;
	const [r1, g1, b1] = h < 60 ? [c, x, 0] : h < 120 ? [x, c, 0] : h < 180 ? [0, c, x] : h < 240 ? [0, x, c] : h < 300 ? [x, 0, c] : [c, 0, x];
	return `#${[r1, g1, b1].map((v) => Math.round((v + m) * 255).toString(16).padStart(2, "0")).join("")}`;
}


function usePrefersReducedMotion(): boolean {
	const [reduced, setReduced] = useState(() => typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
	useEffect(() => {
		if (typeof window.matchMedia !== "function") return;
		const query = window.matchMedia("(prefers-reduced-motion: reduce)");
		const onChange = () => setReduced(query.matches);
		query.addEventListener("change", onChange);
		return () => query.removeEventListener("change", onChange);
	}, []);
	return reduced;
}

/**
 * The recommended plan's highlight: a slow dithered vortex across the top half
 * of the card that fades into the surface: saturated brand-blue dither on a
 * transparent ground, so the card shows through. The color resolves on the first render, so it is there with the card and never
 * pops in. Held still for reduced motion.
 */
function PlanGlow() {
	const resolvedTheme = useResolvedTheme();
	const themeStyle = useUiStore((state) => state.themeStyle);
	const [color, setColor] = useState(saturatedBrand);
	useEffect(() => {
		// The theme class lands on the document after the store updates, so re-read a frame later.
		const frame = requestAnimationFrame(() => {
			const next = saturatedBrand();
			setColor((current) => next ?? current);
		});
		return () => cancelAnimationFrame(frame);
	}, [resolvedTheme, themeStyle]);
	const reducedMotion = usePrefersReducedMotion();
	if (!color) return null;
	return (
		<div
			aria-hidden="true"
			className="pointer-events-none absolute inset-x-0 top-0 -z-10 h-1/2 opacity-50 [mask-image:linear-gradient(to_bottom,black,transparent)]"
		>
			<BayerVortex key={color} color={color} pixel={12} arms={10} speed={reducedMotion ? 0 : 3} center={[0.5, 0]} />
		</div>
	);
}

/**
 * One plan as a column: name and status, a large price, the plan action, then
 * what it adds over the plan below it. The current or recommended plan sits on
 * the raised surface; the rest stay on the quiet one, with no outlines.
 */
function PlanCard({ plan, name, amount, interval, intro, features, current, recommended, action, t }: {
	/** Stable id (not the translated name) for tests and hooks. */
	plan: string;
	name: string;
	amount: string;
	interval?: string;
	intro?: string;
	features: string[];
	current: boolean;
	recommended?: boolean;
	action: React.ReactNode;
	t: TFunction;
}) {
	return (
		<div
			data-testid="plan-card"
			data-plan={plan}
			aria-current={current ? "true" : undefined}
			className={cn(
				"flex flex-col gap-4 rounded-lg p-4",
				current ? "bg-(--color-bg-tertiary)" : "bg-(--color-bg-secondary)",
				// The recommended plan carries a dithered glow behind its header.
				recommended && "relative isolate overflow-hidden",
			)}
		>
			{recommended ? <PlanGlow /> : null}
			{/* Fixed-height slots above the list keep the divider and the first
			    feature on one line across every column, whatever a plan omits. */}
			<div className="flex h-5 items-center justify-between gap-2">
				<h3 className="text-control font-medium text-settings-label">{name}</h3>
				{current || recommended ? (
					<span className="text-xs font-medium text-settings-label">
						{t(current ? "settings.billing.current" : "settings.billing.recommended")}
					</span>
				) : null}
			</div>
			<div className="flex h-9 items-baseline gap-1">
				<span className={cn("font-semibold tracking-tight tabular-nums text-settings-title", interval ? "text-3xl leading-9" : "text-xl leading-9")}>{amount}</span>
				{interval ? <span className="text-control text-settings-muted">/ {interval}</span> : null}
			</div>
			<div className="flex h-8 flex-col justify-center">{action}</div>
			<p className="min-h-4 text-xs text-settings-muted">{intro}</p>
			<ul className="flex flex-1 flex-col gap-2 border-t border-border pt-3">
				{features.map((feature) => (
					<li key={feature} className="flex items-start gap-2 text-control leading-5 text-settings-label">
						<Check className="mt-0.5 size-icon-sm shrink-0 text-settings-muted" aria-hidden="true" />
						{feature}
					</li>
				))}
			</ul>
		</div>
	);
}

function Count({ used, limit }: { used: number; limit: number }) {
	return (
		<span className={cn("text-sm tabular-nums", used >= limit ? "text-destructive" : "text-settings-label")}>
			{used}/{limit}
		</span>
	);
}

function UsageRow({ label, used, limit, resetsAt, absoluteReset, t }: {
	label: string;
	used: number;
	limit: number;
	resetsAt?: string;
	absoluteReset?: boolean;
	t: TFunction;
}) {
	const fraction = limit > 0 ? Math.min(1, used / limit) : 0;
	const values = { used: formatHours(used), limit: formatHours(limit) };
	const description = resetsAt
		? t("settings.billing.usageDetail", { ...values, resets: absoluteReset ? formatDate(resetsAt) : formatRelative(resetsAt, t) })
		: t("settings.billing.usageDetailNoReset", values);
	return (
		<SettingsRow label={label} description={description}>
			<div
				role="meter"
				aria-label={label}
				aria-valuemin={0}
				aria-valuemax={limit}
				aria-valuenow={Math.min(used, limit)}
				className="h-1.5 w-28 overflow-hidden rounded-full bg-[var(--color-bg-tertiary)]"
			>
				<div
					className={cn(
						"h-full rounded-full transition-[width] duration-(--duration-normal)",
						fraction >= 1 ? "bg-destructive" : fraction >= 0.8 ? "bg-[var(--color-status-in-review)]" : "bg-[var(--color-accent)]",
					)}
					style={{ width: `${fraction * 100}%` }}
				/>
			</div>
		</SettingsRow>
	);
}

function runningSlots(limits: CloudCpPlanLimits): number {
	return limits.maxRunningSandboxes && limits.maxRunningSandboxes > 0 ? limits.maxRunningSandboxes : limits.maxActiveSandboxes;
}

function planName(plan: string): string {
	return plan.charAt(0).toUpperCase() + plan.slice(1);
}

/**
 * What a paid plan includes: what cloud adds over local (Starter lists it, Pro
 * builds on Starter), then the limits from its Stripe product.
 */
function planFeatures(plan: string, limits: CloudCpPlanLimits, t: TFunction): { intro?: string; features: string[] } {
	const starter = plan === RECOMMENDED_PLAN;
	const pro = plan === "pro";
	const included = starter
		? [t("settings.billing.cloudAway"), t("settings.billing.cloudSandboxes"), t("settings.billing.cloudGithub"), t("settings.billing.starterHosts")]
		: pro
			? [t("settings.billing.proHosts")]
			: [];
	const features = [
		...included,
		t("settings.billing.featureRunning", { count: runningSlots(limits) }),
		t("settings.billing.featureTotal", { count: limits.maxActiveSandboxes }),
		t("settings.billing.featureOrchestrators", { count: limits.orchestratorSlots }),
		t("settings.billing.featureWindow", { hours: limits.windowSessionHours, window: limits.windowHours }),
		t("settings.billing.featureWeekly", { hours: limits.weeklySessionHours }),
		...(limits.manualResetsPerMonth > 0 ? [t("settings.billing.featureResets", { count: limits.manualResetsPerMonth })] : []),
	];
	return {
		intro: starter ? t("settings.billing.everythingInFree") : pro ? t("settings.billing.everythingInStarter") : undefined,
		features,
	};
}

function offerPrice(offer: CloudCpPlanOffer, t: TFunction): { amount: string; interval: string } {
	if (offer.unitAmount === undefined || !offer.currency) return { amount: "—", interval: "" };
	return {
		amount: formatMoney(offer.unitAmount, offer.currency),
		interval: offer.interval === "year" ? t("settings.billing.intervalYear") : t("settings.billing.intervalMonth"),
	};
}

function formatMoney(minorUnits: number, currency: string): string {
	try {
		return new Intl.NumberFormat(undefined, { style: "currency", currency: currency.toUpperCase(), maximumFractionDigits: minorUnits % 100 === 0 ? 0 : 2 }).format(minorUnits / 100);
	} catch {
		return `${(minorUnits / 100).toFixed(2)} ${currency.toUpperCase()}`;
	}
}

/** Session-minutes as hours with at most one decimal: 252 → "4.2". */
export function formatHours(minutes: number): string {
	const hours = Math.round((minutes / 60) * 10) / 10;
	return Number.isInteger(hours) ? String(hours) : hours.toFixed(1);
}

function formatDate(iso: string | undefined): string {
	if (!iso) return "";
	const date = new Date(iso);
	if (Number.isNaN(date.getTime())) return "";
	return date.toLocaleString(undefined, { weekday: "short", month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

/** A day such as "Tue, 10 Nov 2026", for when a cancelled plan ends. */
function formatDay(iso: string): string {
	const date = new Date(iso);
	if (Number.isNaN(date.getTime())) return "";
	return date.toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short", year: "numeric" });
}

/** Time until `iso` as "in 1 h 20 m" / "in 5 m" / "now". */
export function formatRelative(iso: string, t: TFunction, now = Date.now()): string {
	const ms = new Date(iso).getTime() - now;
	if (!Number.isFinite(ms) || ms <= 0) return t("settings.billing.resetsNow");
	const totalMinutes = Math.ceil(ms / 60_000);
	const hours = Math.floor(totalMinutes / 60);
	const minutes = totalMinutes % 60;
	if (hours === 0) return t("settings.billing.inMinutes", { minutes });
	return t("settings.billing.inHoursMinutes", { hours, minutes });
}

function billingActionError(error: unknown, t: TFunction): string {
	if (error instanceof CloudCpError) {
		if (error.status === 403) return t("settings.billing.adminOnly");
		if (error.code === "ALREADY_SUBSCRIBED") return t("settings.billing.alreadySubscribed");
		if (error.code === "NO_RESETS_LEFT") return t("settings.billing.noResetsLeft");
	}
	return t("settings.billing.actionFailed");
}

export type { CloudCpBillingSummary };
