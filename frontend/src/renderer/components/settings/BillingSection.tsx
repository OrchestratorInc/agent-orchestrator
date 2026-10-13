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
			<SettingsSection title={title} sectionId="billing" titleHidden={titleHidden}>
				<p className="flex items-center gap-2 px-3 text-xs text-muted-foreground">
					<Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />
					{t("settings.billing.loading")}
				</p>
			</SettingsSection>
		);
	}
	if (billing.isError || !billing.data) {
		return (
			<SettingsSection title={title} sectionId="billing" titleHidden={titleHidden}>
				<div className="flex items-center gap-2 px-3 text-xs text-destructive">
					{t("settings.billing.loadFailed")}
					<Button variant="ghost" size="sm" onClick={() => void billing.refetch()}>{t("settings.billing.retry")}</Button>
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
				{summary.exempt ? <p className="px-1 text-xs text-muted-foreground">{t("settings.billing.exempt")}</p> : null}
				{endsAt ? (
					<p className="flex items-center justify-between gap-3 px-1 text-xs text-settings-muted">
						{t("settings.billing.cancelNotice", { plan: planName(currentPlan), date: endsAt })}
						{manage ? (
							<Button size="sm" variant="secondary" disabled={pending !== null} onClick={() => void openPortal()}>
								{pending === "portal" ? spinner : <ExternalLink className="size-icon-sm" aria-hidden="true" />}
								{t("settings.billing.keepPlan", { plan: planName(currentPlan) })}
							</Button>
						) : null}
					</p>
				) : null}
				{lapsed && manage ? (
					<p role="alert" className="flex items-center justify-between gap-3 px-1 text-xs text-destructive">
						{t("settings.billing.paymentFailed")}
						<Button size="sm" disabled={pending !== null} onClick={() => void openPortal()}>
							{pending === "portal" ? spinner : <ExternalLink className="size-icon-sm" aria-hidden="true" />}
							{t("settings.billing.updatePayment")}
						</Button>
					</p>
				) : null}
				<div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
					<PlanCard
						name={t("settings.billing.planFree")}
						price={t("settings.billing.freePrice")}
						features={[
							t("settings.billing.freeLocal"),
							t("settings.billing.freeAgents"),
							t("settings.billing.freeOrchestrator"),
							t("settings.billing.freeNoCloud"),
						]}
						current={currentPlan === "free"}
						action={endsAt ? (
							<Button size="sm" variant="secondary" className="w-full" disabled>
								{t("settings.billing.freeStarts", { date: endsAt })}
							</Button>
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
						const busy = pending === `checkout:${offer.name}` || (pending === "portal" && subscribed);
						let label = t("settings.billing.upgrade");
						let onClick = () => void openCheckout(offer.name);
						if (isCurrent) {
							label = t("settings.billing.manage");
							onClick = () => void openPortal();
						} else if (subscribed) {
							label = t("settings.billing.switchPlan");
							onClick = () => void openPortal();
						}
						return (
							<PlanCard
								key={offer.name}
								name={planName(offer.name)}
								price={offerPrice(offer, t)}
								features={offer.limits ? planFeatures(offer.name, offer.limits, t) : []}
								current={isCurrent}
								recommended={!subscribed && offer.name === RECOMMENDED_PLAN}
								highlighted={subscribed ? isCurrent : offer.name === RECOMMENDED_PLAN}
								action={manage ? (
									<Button size="sm" variant={offer.name === RECOMMENDED_PLAN && !isCurrent ? "primary" : "secondary"}
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
						name={t("settings.billing.planEnterprise")}
						price={t("settings.billing.enterprisePrice")}
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
				{actionError ? <p role="alert" className="px-1 text-xs text-destructive">{actionError}</p> : null}
			</SettingsSection>
			{subscribed && limits && usage ? (
				<>
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
						<Button size="sm" variant="secondary" disabled={resetsLeft === 0 || pending !== null} onClick={() => setConfirmReset(true)}>
							{t("settings.billing.resetNow")}
						</Button>
					</SettingsRow> : null}
				</SettingsSection>
				<p className="-mt-2 px-1 text-xs text-settings-muted">{t("settings.billing.usageNote")}</p>
				</>
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
		<SettingsSection title={title} sectionId="billing" titleHidden={titleHidden}>
			<p className="px-3 text-xs leading-relaxed text-muted-foreground">{text}</p>
		</SettingsSection>
	);
}

function PlanCard({ name, price, features, current, recommended, highlighted, action, t }: {
	name: string;
	price: string;
	features: string[];
	current: boolean;
	recommended?: boolean;
	highlighted?: boolean;
	action: React.ReactNode;
	t: TFunction;
}) {
	return (
		<div
			data-testid="plan-card"
			data-plan={name.toLowerCase()}
			className={cn(
				"relative flex flex-col gap-2 rounded-lg border bg-[var(--color-bg-settings-row)] p-3",
				highlighted ? "border-[var(--color-status-working)]" : "border-[var(--color-border)]",
			)}
		>
			{current ? (
				<span className="absolute right-2 top-2 rounded-full bg-[var(--color-status-working)] px-1.5 py-px text-[length:var(--font-size-xs)] font-medium leading-4 text-white">
					{t("settings.billing.current")}
				</span>
			) : null}
			<div className="flex flex-col gap-1">
				{recommended ? (
					<span className="w-fit rounded-full bg-[var(--color-status-working)] px-1.5 py-px text-[length:var(--font-size-xs)] font-medium leading-4 text-white">
						{t("settings.billing.recommended")}
					</span>
				) : null}
				<span className="text-sm font-semibold text-settings-label">{name}</span>
				<span className="text-xs tabular-nums text-settings-muted">{price}</span>
			</div>
			<ul className="flex flex-1 flex-col gap-1">
				{features.map((feature) => (
					<li key={feature} className="flex items-start gap-1.5 text-xs leading-4 text-settings-muted">
						<Check className="mt-px size-icon-sm shrink-0" aria-hidden="true" />
						{feature}
					</li>
				))}
			</ul>
			{action}
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
function planFeatures(plan: string, limits: CloudCpPlanLimits, t: TFunction): string[] {
	const included = plan === RECOMMENDED_PLAN
		? [t("settings.billing.cloudAway"), t("settings.billing.cloudSandboxes"), t("settings.billing.cloudGithub")]
		: [t("settings.billing.everythingInStarter")];
	return [
		...included,
		t("settings.billing.featureRunning", { count: runningSlots(limits) }),
		t("settings.billing.featureTotal", { count: limits.maxActiveSandboxes }),
		t("settings.billing.featureOrchestrators", { count: limits.orchestratorSlots }),
		t("settings.billing.featureWindow", { hours: limits.windowSessionHours, window: limits.windowHours }),
		t("settings.billing.featureWeekly", { hours: limits.weeklySessionHours }),
		...(limits.manualResetsPerMonth > 0 ? [t("settings.billing.featureResets", { count: limits.manualResetsPerMonth })] : []),
	];
}

function offerPrice(offer: CloudCpPlanOffer, t: TFunction): string {
	if (offer.unitAmount === undefined || !offer.currency) return "";
	return t("settings.billing.price", {
		price: formatMoney(offer.unitAmount, offer.currency),
		interval: offer.interval === "year" ? t("settings.billing.intervalYear") : t("settings.billing.intervalMonth"),
	});
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
