import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "@/components/app-icons";
import { Button } from "../../ui/button";
import { PeopleField, type Recipient } from "../../teams/PeopleField";

/** What the field hands over: emails, and teams of the workspace. */
export interface ArtifactRecipients {
	emails: string[];
	teams: Extract<Recipient, { kind: "team" }>[];
}

/**
 * On a team workspace, who to share a link with: the workspace's people and
 * teams by name, or anyone by email, as the session share dialog's field
 * offers them, beside one action. Everyone in the workspace is General
 * access's choice, not a pick here, so being in the workspace doesn't stop
 * someone being added by name.
 */
export function ArtifactRecipientField({ orgId, actionLabel, busy = false, unavailable, onSubmit }: {
	orgId: string;
	/** Invite on a live link; Add while setting a link up before it is published. */
	actionLabel: string;
	busy?: boolean;
	/** People (email:…) and teams (team:…) the link is already shared with. */
	unavailable?: ReadonlySet<string>;
	/** Called with what is picked; resolves true when it was taken, clearing the field. */
	onSubmit: (recipients: ArtifactRecipients) => Promise<boolean> | boolean;
}) {
	const { t } = useTranslation();
	const [picked, setPicked] = useState<Recipient[]>([]);
	const submit = async () => {
		if (picked.length === 0 || busy) return;
		const emails = picked.flatMap((recipient) => recipient.kind === "person" ? [recipient.email] : []);
		const teams = picked.filter((recipient): recipient is Extract<Recipient, { kind: "team" }> => recipient.kind === "team");
		if (await onSubmit({ emails, teams })) setPicked([]);
	};
	return (
		<div className="flex items-start gap-1.5" data-artifact-recipient-field="">
			<PeopleField
				ariaLabel={t("artifacts.share.people.addPeopleOrTeams")}
				className="min-w-0 flex-1"
				onChange={setPicked}
				onSubmit={() => void submit()}
				orgId={orgId}
				placeholder={t("artifacts.share.people.addPeopleOrTeams")}
				unavailable={unavailable}
				value={picked}
			/>
			<Button disabled={picked.length === 0 || busy} onClick={() => void submit()} size="sm" type="button" variant="secondary">
				{busy ? <Loader2 aria-hidden="true" className="size-3 animate-spin motion-reduce:animate-none" /> : null}
				{actionLabel}
			</Button>
		</div>
	);
}
