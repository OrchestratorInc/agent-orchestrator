import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { looksLikeEmail } from "../../../hooks/useTeams";
import { NewTeamDialog } from "../../teams/NewTeamDialog";
import { PeopleField, recipientKey, type Recipient } from "../../teams/PeopleField";

/** What the field hands over: emails, and teams of the workspace. */
export interface ArtifactRecipients {
	emails: string[];
	teams: Extract<Recipient, { kind: "team" }>[];
}

// A dialog opened from the share popover, such as New team, sits outside it:
// while one is open, a click or focus in it must not close the popover, which
// would take the dialog with it.
let dialogsOverPopover = 0;
export function dialogOverSharePopover(): boolean {
	return dialogsOverPopover > 0;
}

/**
 * Who to share a link with, in the session share dialog's field: people the
 * workspace knows by name, or anyone by email, with its suggestions as you
 * type. An artifact link has one role, Can view, so a pick (Enter, a comma,
 * or a click on a suggestion) shares at once; there is no Add step. A pick
 * stays in the field until it is shared, and one that is refused (the
 * recipient limit, a failed request) stays there for another Enter. On a
 * team workspace (teams) its teams are offered too, and "New team…" makes
 * one, as session sharing does, and shares the link with it. Everyone in
 * the workspace is General access's choice, not a pick here.
 */
export function ArtifactRecipientField({ orgId, teams: teamsOffered, unavailable, onSubmit }: {
	/** The workspace whose people (and teams) are suggested; empty until it is known. */
	orgId: string;
	/** Teams are offered: a team workspace on a paid Cloud plan. */
	teams: boolean;
	/** People (email:…) and teams (team:…) the link is already shared with. */
	unavailable?: ReadonlySet<string>;
	/** Called with each pick; resolves true when it was taken. */
	onSubmit: (recipients: ArtifactRecipients) => Promise<boolean> | boolean;
}) {
	const { t } = useTranslation();
	const label = t(teamsOffered ? "artifacts.share.people.addPeopleOrTeams" : "artifacts.share.people.addByEmail");
	const [newTeamFor, setNewTeamFor] = useState<{ people: Recipient[]; name: string } | null>(null);
	useEffect(() => {
		if (!newTeamFor) return;
		dialogsOverPopover += 1;
		return () => {
			dialogsOverPopover -= 1;
		};
	}, [newTeamFor !== null]); // eslint-disable-line react-hooks/exhaustive-deps
	// Picks not yet shared: being sent, or refused and waiting for a retry.
	const [held, setHeld] = useState<Recipient[]>([]);
	const send = (picked: readonly Recipient[]) => {
		const emails = picked.flatMap((recipient) => recipient.kind === "person" ? [recipient.email] : []);
		const teams = picked.filter((recipient): recipient is Extract<Recipient, { kind: "team" }> => recipient.kind === "team");
		if (emails.length === 0 && teams.length === 0) return;
		const sent = new Set(picked.map(recipientKey));
		void Promise.resolve()
			.then(() => onSubmit({ emails, teams }))
			.catch(() => false)
			.then((taken) => {
				if (taken) setHeld((current) => current.filter((recipient) => !sent.has(recipientKey(recipient))));
			});
	};
	const change = (next: Recipient[]) => {
		const before = new Set(held.map(recipientKey));
		setHeld(next);
		send(next.filter((recipient) => !before.has(recipientKey(recipient))));
	};
	const take = (picked: readonly Recipient[]) => {
		setHeld((current) => [...current, ...picked.filter((recipient) => !current.some((other) => recipientKey(other) === recipientKey(recipient)))]);
		send(picked);
	};
	return (
		<div data-artifact-recipient-field="">
			<PeopleField
				allowTeams={teamsOffered}
				ariaLabel={label}
				commitOnBlur={false}
				onChange={change}
				onNewTeam={teamsOffered ? (_people, typed) => {
					// A typed email joins the new team; other text names it.
					const email = looksLikeEmail(typed) ? typed.toLowerCase() : "";
					setNewTeamFor({ people: email ? [{ kind: "person", email }] : [], name: email ? "" : typed });
				} : undefined}
				// Enter on an empty field tries the refused picks again.
				onSubmit={() => send(held)}
				orgId={orgId}
				placeholder={label}
				unavailable={unavailable}
				value={held}
			/>
			{newTeamFor ? (
				<NewTeamDialog
					initialName={newTeamFor.name}
					initialPeople={newTeamFor.people}
					onCreated={(team) => {
						setNewTeamFor(null);
						take([{ kind: "team", teamId: team.team.id, name: team.team.name, memberCount: team.team.memberCount }]);
					}}
					onOpenChange={(next) => {
						if (!next) setNewTeamFor(null);
					}}
					open
					orgId={orgId}
				/>
			) : null}
		</div>
	);
}
