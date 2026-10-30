import { useMutation, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { useCloudCp } from "../../hooks/useCloudCp";
import { invalidateTeamsAndShares } from "../../hooks/useTeams";
import { CloudCpError, type CloudCpTeamDetail } from "../../lib/cloud-cp";
import { Button } from "../ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { PeopleField, type Recipient } from "./PeopleField";

/** A readable message for a failed team request. */
export function teamErrorMessage(error: unknown, fallback: string): string {
	if (error instanceof CloudCpError && error.message) return error.message;
	if (error instanceof Error && error.message) return error.message;
	return fallback;
}

/**
 * Two fields: a name and the people in it. Everything else (owners,
 * renaming, removing) lives on the team page, so making a team is quick. The
 * person creating it becomes its owner.
 */
export function NewTeamDialog({ orgId, open, onOpenChange, onCreated, initialPeople = [] }: {
	orgId: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onCreated: (team: CloudCpTeamDetail) => void;
	initialPeople?: Recipient[];
}) {
	const { t } = useTranslation();
	const { client, ready } = useCloudCp();
	const queryClient = useQueryClient();
	const [name, setName] = useState("");
	const [people, setPeople] = useState<Recipient[]>(initialPeople);
	const create = useMutation({
		mutationFn: () =>
			client.createTeam(orgId, {
				name: name.trim(),
				members: people.filter((person): person is Extract<Recipient, { kind: "person" }> => person.kind === "person").map((person) => ({ email: person.email })),
			}),
		onSuccess: (team) => {
			invalidateTeamsAndShares(queryClient);
			onCreated(team);
		},
	});
	const trimmed = name.trim();
	const pending = people.filter((person) => person.kind === "person" && !person.name).map((person) => (person.kind === "person" ? person.email : ""));
	const submit = () => {
		if (!trimmed || create.isPending) return;
		create.mutate();
	};

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(500px,calc(100vw-24px))]")}
				onClick={(event) => event.stopPropagation()}
				data-new-team-dialog=""
			>
				<DialogClose asChild>
					<button type="button" className="settings-dialog-close-button settings-close-button" aria-label={t("confirm.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12 pb-0")}>
					<DialogTitle className="settings-dialog-title text-subtitle">{t("teams.newTeam")}</DialogTitle>
				</div>
				<form
					className={cn(settingsDialogBodyClass, "gap-4.5 px-5 pt-4 pb-5")}
					onSubmit={(event) => {
						event.preventDefault();
						submit();
					}}
				>
					<div className="flex flex-col gap-1.5">
						<Label htmlFor="new-team-name" className="text-control">{t("teams.name")}</Label>
						<Input
							id="new-team-name"
							value={name}
							autoFocus
							maxLength={80}
							onChange={(event) => {
								setName(event.target.value);
								create.reset();
							}}
							aria-invalid={Boolean(create.error) || undefined}
							data-new-team-name=""
						/>
					</div>
					<div className="flex flex-col gap-1.5">
						<span className="text-control text-foreground" id="new-team-people-label">{t("teams.peopleLabel")}</span>
						<PeopleField
							orgId={orgId}
							value={people}
							onChange={setPeople}
							allowTeams={false}
							placeholder={t("teams.addByNameOrEmail")}
							ariaLabel={t("teams.peopleLabel")}
							onSubmit={submit}
						/>
						{pending.length > 0 ? (
							<p className="text-xs text-muted-foreground" data-new-team-pending="">
								{t("teams.pendingHint", { count: pending.length, emails: pending.join(", ") })}
							</p>
						) : null}
					</div>
					{create.error ? (
						<p role="alert" className="-mt-2 text-caption leading-4 text-error">{teamErrorMessage(create.error, t("teams.createFailed"))}</p>
					) : null}
					<button type="submit" className="hidden" aria-hidden="true" tabIndex={-1} />
				</form>
				<div className={cn(settingsDialogFooterClass, "px-5 py-3.5")}>
					<Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>{t("confirm.cancel")}</Button>
					<Button type="button" variant="primary" onClick={submit} disabled={!ready || !trimmed || create.isPending} data-new-team-create="">
						{create.isPending ? t("teams.creating") : t("teams.create")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
