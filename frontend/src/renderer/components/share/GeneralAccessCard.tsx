import { Check, ChevronDown, Globe, Lock } from "@/components/app-icons";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import type { CloudCpShareRole } from "../../lib/cloud-cp";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Skeleton } from "../ui/skeleton";
import { AccessMenu } from "./AccessMenu";
import type { ShareModeCap, ShareScope } from "./accessLabels";
import { actualLinkCap, anyoneSentence, expiryText, isTrustedLink, type LinkAccess } from "./linkAccess";

const LINK_CAPS: readonly ShareModeCap[] = ["read-only", "standard"];
/** A link made before links stopped offering Trusted shows it, checked; choosing another cap replaces the link. */
const TRUSTED_LINK_CAPS: readonly ShareModeCap[] = ["read-only", "standard", "trusted"];

/**
 * General access: who the link lets in, as one sentence under the choice.
 * "Only people with access" or "Anyone with the link"; with a link, what it
 * lets people do, when it ends, and how many other links are live. The icon
 * stays neutral: the sentence carries the risk, and color is for state.
 */
export function GeneralAccessCard({ scope, access, restricted, locale, label = true, onTurnOn, onTurnOff, onChangeRole, onOpenSettings }: {
	scope: ShareScope;
	access: LinkAccess;
	/** The sentence for "Only people with access" in this workspace. */
	restricted: string;
	locale: string;
	/** "General access" over the card; off where a section heading says it. */
	label?: boolean;
	onTurnOn: () => void;
	onTurnOff: () => void;
	onChangeRole: (next: { role: CloudCpShareRole; modeCap: ShareModeCap }) => void;
	onOpenSettings: () => void;
}) {
	const { t } = useTranslation();
	const sentenceId = useId();
	const link = access.broadest;
	const busy = access.turnOn.isPending || access.turnOff.isPending || access.replace.isPending;
	const others = access.links.length - 1;
	const Icon = link ? Globe : Lock;
	const sentence = link ? anyoneSentence(t, scope, link.role) : restricted;
	// A whole-project link opens this session too, whatever this session's own setting says.
	const viaProject = !link && access.projectLinks.length > 0;
	return (
		<section className="flex flex-col gap-1.5" aria-label={t("share.generalAccess")} data-share-general-access={link ? "anyone" : "restricted"}>
			{label ? <p className="text-xs text-muted-foreground">{t("share.generalAccess")}</p> : null}
			<div className={cn("flex items-start gap-2.5 rounded-md bg-muted/40 py-2 pr-1.5 pl-2.5", busy && "opacity-70")} aria-busy={busy || undefined}>
				<span className="grid size-6 shrink-0 place-items-center rounded-full bg-muted text-muted-foreground" aria-hidden="true">
					<Icon className="size-3.5" />
				</span>
				<div className="flex min-w-0 flex-1 flex-col">
					{access.loading ? (
						<div className="flex flex-col gap-1.5 py-1" aria-busy="true">
							<Skeleton className="h-3 w-36 rounded" />
							<Skeleton className="h-3 w-56 rounded" />
						</div>
					) : (
						<>
							<div className="flex min-h-7 items-center gap-2">
								<DropdownMenu modal={false}>
									<DropdownMenuTrigger asChild disabled={busy}>
										<Button
											type="button"
											variant="quiet"
											size="none"
											className="-ml-2 h-7 gap-1 px-2 text-control font-medium"
											aria-describedby={sentenceId}
											data-general-access-menu=""
										>
											{link ? t("share.general.anyone") : t("share.general.restricted")}
											<ChevronDown className="size-3 shrink-0" aria-hidden="true" />
										</Button>
									</DropdownMenuTrigger>
									<DropdownMenuContent align="start" className="w-72">
										<AudienceOption
											icon={<Lock />}
											label={t("share.general.restricted")}
											description={t("share.general.restrictedOption")}
											selected={!link}
											onSelect={() => {
												if (link) onTurnOff();
											}}
										/>
										<AudienceOption
											icon={<Globe />}
											label={t("share.general.anyone")}
											description={t("share.general.anyoneOption")}
											selected={Boolean(link)}
											onSelect={() => {
												if (!link) onTurnOn();
											}}
										/>
									</DropdownMenuContent>
								</DropdownMenu>
								<span className="flex-1" />
								{link ? (
									<AccessMenu
										role={link.role}
										modeCap={actualLinkCap(link.role, link.modeCap)}
										scope={scope}
										caps={isTrustedLink(link) ? TRUSTED_LINK_CAPS : LINK_CAPS}
										onChange={onChangeRole}
										disabled={busy}
										describedBy={sentenceId}
									/>
								) : null}
							</div>
							<p id={sentenceId} className="pr-2 text-xs leading-5 text-muted-foreground" data-general-access-sentence="">
								{viaProject ? t("share.twoSentences", { first: sentence, second: t("share.general.projectLinkToo") }) : sentence}
							</p>
							{link ? (
								<p className="flex flex-wrap items-baseline gap-x-1 text-xs leading-5 text-muted-foreground">
									<span>{expiryText(t, link, locale)}</span>
									{others > 0 ? <span>{`· ${t("share.general.andMore", { count: others })}`}</span> : null}
									<span aria-hidden="true">·</span>
									<Button type="button" variant="link" className="text-xs text-muted-foreground no-underline hover:text-foreground hover:underline" onClick={onOpenSettings} data-share-link-settings="">
										{t("share.general.linkSettings")}
									</Button>
								</p>
							) : null}
						</>
					)}
				</div>
			</div>
		</section>
	);
}

function AudienceOption({ icon, label, description, selected, onSelect }: {
	icon: React.ReactNode;
	label: string;
	description: string;
	selected: boolean;
	onSelect: () => void;
}) {
	return (
		<DropdownMenuItem onSelect={onSelect} className={cn("items-start py-1.5", selected && "bg-interactive-active")} aria-checked={selected} role="menuitemradio">
			<span className="mt-0.5">{icon}</span>
			<span className="flex min-w-0 flex-1 flex-col">
				<span className="text-foreground">{label}</span>
				<span className="text-xs text-muted-foreground">{description}</span>
			</span>
			{selected ? <Check className="mt-0.5 text-foreground!" aria-hidden="true" /> : null}
		</DropdownMenuItem>
	);
}
