import { useId } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Check, ChevronDown, Users } from "@/components/app-icons";
import type { CloudShareAccess } from "../../../../shared/cloud-share";
import { cn } from "../../../lib/utils";
import { shareAccessIcon } from "../../ArtifactShareMark";
import { Button } from "../../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../../ui/dropdown-menu";

/**
 * Who a link opens for, as one choice: on a team workspace, Everyone at it
 * is a private link shared with the whole workspace.
 */
export type ArtifactAudience = CloudShareAccess | "workspace";

/**
 * Who a link opens for, as a short name: "Only you" is how Only people
 * invited reads while nobody is invited, not a third choice.
 */
export function accessName(t: TFunction, access: ArtifactAudience, invited: number, workspaceName = ""): string {
	if (access === "anyone") return t("artifacts.share.access.anyone");
	if (access === "workspace") return t("artifacts.share.access.workspace", { name: workspaceName });
	return invited > 0 ? t("artifacts.share.access.invited") : t("artifacts.share.access.onlyYou");
}

/** The sentence under the access name. */
export function accessSentence(t: TFunction, access: ArtifactAudience, invited: number, workspaceName = ""): string {
	if (access === "anyone") return t("artifacts.share.access.anyoneSentence");
	if (access === "workspace") return t("artifacts.share.access.workspaceSentence", { name: workspaceName });
	return invited > 0 ? t("artifacts.share.access.invitedSentence") : t("artifacts.share.access.onlyYouSentence");
}

/**
 * General access: Only people invited or Anyone with the link, as one choice
 * with the sentence it means under it, and, on a team workspace (workspace
 * set), Everyone at it between them. Where links cannot be private
 * (choosable false) it only says the link is public. The change applies to
 * the link at once; its URL stays the same.
 */
export function ArtifactGeneralAccess({ access, invited, onChange, choosable = true, disabled = false, workspace }: {
	access: ArtifactAudience;
	/** How many people and teams the link is shared with. */
	invited: number;
	onChange: (access: ArtifactAudience) => void;
	choosable?: boolean;
	disabled?: boolean;
	/** The team workspace whose members the link can be shared with, by its name. */
	workspace?: { name: string };
}) {
	const { t } = useTranslation();
	const sentenceId = useId();
	const { Icon, className: iconClass } = shareAccessIcon(access === "workspace" ? "restricted" : access);
	const PrivateIcon = shareAccessIcon("restricted");
	const PublicIcon = shareAccessIcon("anyone");
	const workspaceName = workspace?.name ?? "";
	const name = accessName(t, access, invited, workspaceName);
	return (
		<section className="flex flex-col gap-2" aria-label={t("artifacts.share.access.title")} data-artifact-access={access}>
			<p className="text-xs text-muted-foreground">{t("artifacts.share.access.title")}</p>
			<div className={cn("flex items-start gap-2.5 rounded-md bg-muted/40 py-2 pr-2 pl-2.5", disabled && "opacity-70")} aria-busy={disabled || undefined}>
				<span className={cn("grid size-6 shrink-0 place-items-center rounded-full bg-muted", iconClass)} aria-hidden="true">
					<Icon className="size-3.5" />
				</span>
				<div className="flex min-w-0 flex-1 flex-col">
					{choosable ? (
						<DropdownMenu modal={false}>
							<DropdownMenuTrigger asChild disabled={disabled}>
								<Button
									aria-describedby={sentenceId}
									className="-ml-2 h-7 self-start gap-1 px-2 text-control font-medium"
									data-artifact-access-menu=""
									size="none"
									type="button"
									variant="quiet"
								>
									{name}
									<ChevronDown aria-hidden="true" className="size-3 shrink-0" />
								</Button>
							</DropdownMenuTrigger>
							<DropdownMenuContent align="start" className="w-72">
								<AccessOption
									description={t("artifacts.share.access.invitedOption")}
									icon={<PrivateIcon.Icon className="text-status-in-review!" />}
									label={t("artifacts.share.access.invited")}
									onSelect={() => access !== "restricted" && onChange("restricted")}
									selected={access === "restricted"}
								/>
								{workspace ? (
									<AccessOption
										description={t("artifacts.share.access.workspaceOption")}
										icon={<Users aria-hidden="true" className="text-status-in-review!" />}
										label={t("artifacts.share.access.workspace", { name: workspaceName })}
										onSelect={() => access !== "workspace" && onChange("workspace")}
										selected={access === "workspace"}
									/>
								) : null}
								<AccessOption
									description={t("artifacts.share.access.anyoneOption")}
									icon={<PublicIcon.Icon className="text-success!" />}
									label={t("artifacts.share.access.anyone")}
									onSelect={() => access !== "anyone" && onChange("anyone")}
									selected={access === "anyone"}
								/>
							</DropdownMenuContent>
						</DropdownMenu>
					) : (
						<span className="flex min-h-7 items-center text-control font-medium text-foreground">{name}</span>
					)}
					<p id={sentenceId} className="pr-2 text-xs leading-5 text-muted-foreground" data-artifact-access-sentence="">
						{accessSentence(t, access, invited, workspaceName)}
					</p>
				</div>
			</div>
		</section>
	);
}

function AccessOption({ icon, label, description, selected, onSelect }: {
	icon: React.ReactNode;
	label: string;
	description: string;
	selected: boolean;
	onSelect: () => void;
}) {
	return (
		<DropdownMenuItem
			aria-checked={selected}
			className={cn("items-start py-1.5", selected && "bg-interactive-active")}
			onSelect={onSelect}
			role="menuitemradio"
		>
			<span className="mt-0.5">{icon}</span>
			<span className="flex min-w-0 flex-1 flex-col">
				<span className="text-foreground">{label}</span>
				<span className="text-xs text-muted-foreground">{description}</span>
			</span>
			{selected ? <Check aria-hidden="true" className="mt-0.5 text-foreground!" /> : null}
		</DropdownMenuItem>
	);
}
