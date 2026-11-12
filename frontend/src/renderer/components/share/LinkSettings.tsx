import { useTranslation } from "react-i18next";
import type { CloudCpShareLink } from "../../lib/cloud-cp";
import { Button } from "../ui/button";
import { Label } from "../ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { capLabel, roleLabel, type ShareScope } from "./accessLabels";
import { actualLinkCap, expiryText, type LinkAccess, type ShareExpiry } from "./linkAccess";

/**
 * The link settings behind the gear: how long a new link lasts, and every
 * live link of this session or project, each revocable. A session can have
 * several, made at different times with different access; general access
 * describes the broadest.
 */
export function LinkSettings({ scope, access, expiry, onExpiryChange, onRevoke, busy, locale }: {
	scope: ShareScope;
	access: LinkAccess;
	expiry: ShareExpiry;
	onExpiryChange: (expiry: ShareExpiry) => void;
	/** The link and its row's place, so focus can move to the next row once it is gone. */
	onRevoke: (link: CloudCpShareLink, index: number) => void;
	/** A revoke is running or being checked: Revoke waits, but keeps focus. */
	busy: boolean;
	locale: string;
}) {
	const { t } = useTranslation();
	// Standard is what writing means; another cap (a Trusted link made before links stopped offering it) is named.
	const accessText = (link: CloudCpShareLink) => {
		const cap = actualLinkCap(link.role, link.modeCap);
		const level = roleLabel(t, link.role, scope);
		return link.role === "editor" && cap !== "standard" ? `${level} · ${capLabel(t, cap)}` : level;
	};
	const date = (value: string) => new Date(value).toLocaleDateString(locale, { month: "short", day: "numeric" });
	return (
		<div className="flex flex-col gap-4" data-share-link-settings-view="">
			<div className="flex flex-col gap-1">
				<div className="flex items-center gap-3">
					<Label htmlFor="share-link-expiry" className="flex-1 text-control">{t("share.settings.expiry")}</Label>
					<Select value={expiry} onValueChange={(value) => onExpiryChange(value as ShareExpiry)}>
						<SelectTrigger id="share-link-expiry" className="w-36 min-w-0"><SelectValue /></SelectTrigger>
						<SelectContent>
							<SelectItem value="24">{t("share.expiryDay")}</SelectItem>
							<SelectItem value="168">{t("share.expiryWeek")}</SelectItem>
							<SelectItem value="720">{t("share.expiryMonth")}</SelectItem>
							<SelectItem value="never">{t("share.expiryNever")}</SelectItem>
						</SelectContent>
					</Select>
				</div>
				<p className="text-xs leading-5 text-muted-foreground">{t("share.settings.expiryHint")}</p>
			</div>
			<div className="flex flex-col gap-1.5">
				<p className="text-xs text-muted-foreground">{t("share.settings.activeLinks", { count: access.links.length })}</p>
				{access.links.length === 0 ? (
					<p className="text-xs leading-5 text-muted-foreground" data-share-no-links="">{t("share.settings.noLinks")}</p>
				) : (
					<ul className="flex flex-col divide-y divide-border rounded-md bg-muted/40" data-share-active-links="">
						{access.links.map((link, index) => (
							<li key={link.id} className="flex min-h-11 items-center gap-2.5 py-1.5 pr-1.5 pl-2.5" data-share-link-row={link.id}>
								<div className="min-w-0 flex-1">
									<p className="truncate text-control text-foreground">
										{accessText(link)}
										{access.urls[link.id] ? <span className="text-muted-foreground">{` · ${t("share.settings.onThisDevice")}`}</span> : null}
									</p>
									<p className="truncate text-xs text-muted-foreground">
										{[
											t("share.settings.created", { date: date(link.createdAt) }),
											expiryText(t, link, locale),
											typeof link.joinedCount === "number" ? t("share.settings.joined", { count: link.joinedCount }) : undefined,
										].filter(Boolean).join(" · ")}
									</p>
								</div>
								<Button
									type="button"
									variant="destructive-ghost"
									size="sm"
									className="aria-disabled:cursor-default aria-disabled:opacity-50"
									aria-disabled={busy || undefined}
									onClick={() => {
										if (!busy) onRevoke(link, index);
									}}
									data-share-revoke={link.id}
								>
									{t("share.revoke")}
								</Button>
							</li>
						))}
					</ul>
				)}
			</div>
		</div>
	);
}
