import { useTranslation } from "react-i18next";
import { X } from "@/components/app-icons";
import { Button } from "../../ui/button";
import { PersonAvatar } from "../../teams/TeamVisuals";

/** Someone a link is shared with: on the live link (id), or added before it is published (pending). */
export interface ArtifactPerson {
	email: string;
	id?: string;
	pending?: boolean;
}

/**
 * People with access: the owner, then everyone the link is shared with, each
 * with Can view (the only role) and × to remove them. The list scrolls inside
 * the popover once it is long.
 */
export function ArtifactPeopleList({ owner, people, onRemove, removing }: {
	owner: { name?: string; email?: string };
	people: readonly ArtifactPerson[];
	onRemove: (person: ArtifactPerson) => void;
	/** The email being removed now, whose × is disabled. */
	removing?: string;
}) {
	const { t } = useTranslation();
	const ownerName = owner.name?.trim() || owner.email || t("artifacts.share.people.you");
	return (
		<section className="flex flex-col gap-2" aria-label={t("artifacts.share.people.title")} data-artifact-people="">
			<p className="text-xs text-muted-foreground">{t("artifacts.share.people.title")}</p>
			<ul className="-mx-1 flex max-h-44 flex-col overflow-y-auto">
				<li className="flex min-h-9 items-center gap-2 px-1">
					<PersonAvatar email={owner.email} name={owner.name || owner.email} size="sm" />
					<span className="flex min-w-0 flex-1 flex-col">
						<span className="truncate text-control text-foreground">{t("artifacts.share.people.ownerName", { name: ownerName })}</span>
						{owner.name && owner.email ? <span className="truncate text-caption text-muted-foreground">{owner.email}</span> : null}
					</span>
					<span className="shrink-0 text-caption text-muted-foreground">{t("artifacts.share.people.owner")}</span>
				</li>
				{people.map((person) => (
					<li key={person.email} className="flex min-h-9 items-center gap-2 px-1" data-artifact-person={person.pending ? "pending" : ""}>
						<PersonAvatar email={person.email} size="sm" />
						<span className="flex min-w-0 flex-1 flex-col">
							<span className="truncate text-control text-foreground">{person.email}</span>
							{person.pending ? <span className="truncate text-caption text-muted-foreground">{t("artifacts.share.people.pending")}</span> : null}
						</span>
						<span className="shrink-0 text-caption text-muted-foreground">{t("artifacts.share.people.canView")}</span>
						<Button
							aria-label={t("artifacts.share.people.remove", { email: person.email })}
							disabled={removing === person.email}
							onClick={() => onRemove(person)}
							size="icon-xs"
							type="button"
							variant="quiet"
						>
							<X aria-hidden="true" className="size-3" />
						</Button>
					</li>
				))}
			</ul>
		</section>
	);
}
