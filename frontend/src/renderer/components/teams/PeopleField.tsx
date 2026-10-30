import { Plus, Search, X } from "lucide-react";
import { type KeyboardEvent, type ReactNode, useEffect, useId, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import type { CloudCpShareCandidate } from "../../lib/cloud-cp";
import { looksLikeEmail, useShareCandidates } from "../../hooks/useTeams";
import { PersonAvatar, TeamTile } from "./TeamVisuals";

/** Someone picked in a people field: a team, or a person by email. */
export type Recipient =
	| { kind: "team"; teamId: string; name: string; memberCount: number; memberNames?: string[] }
	| { kind: "person"; email: string; name?: string };

export function recipientKey(recipient: Recipient): string {
	return recipient.kind === "team" ? `team:${recipient.teamId}` : `email:${recipient.email.toLowerCase()}`;
}

type Option =
	| { id: string; kind: "team"; candidate: CloudCpShareCandidate; disabled: boolean }
	| { id: string; kind: "person"; candidate: CloudCpShareCandidate; disabled: boolean }
	| { id: string; kind: "email"; email: string; disabled: false }
	| { id: string; kind: "newTeam"; disabled: false };

/**
 * One field for people and teams: type to search the workspace's teams and
 * the people it knows, or a full email to add anyone. Picks become tokens in
 * the field. Arrow keys move through suggestions, Enter or comma picks,
 * Backspace on an empty field removes the last pick, Escape closes.
 */
export function PeopleField({
	orgId,
	value,
	onChange,
	allowTeams = true,
	unavailable,
	unavailableLabel,
	placeholder,
	trailing,
	onNewTeam,
	onSubmit,
	autoFocus,
	ariaLabel,
	invalid,
	className,
}: {
	orgId: string;
	value: Recipient[];
	onChange: (next: Recipient[]) => void;
	allowTeams?: boolean;
	/** Keys (recipientKey) of people and teams that can't be picked, such as those who already have access. */
	unavailable?: ReadonlySet<string>;
	unavailableLabel?: string;
	placeholder: string;
	/** Sits at the field's trailing edge, such as an access menu. */
	trailing?: ReactNode;
	/** Offered as "New team…", with whoever is picked so far. */
	onNewTeam?: (people: Recipient[]) => void;
	/** Enter in an empty field with picks in it. */
	onSubmit?: () => void;
	autoFocus?: boolean;
	ariaLabel: string;
	invalid?: boolean;
	className?: string;
}) {
	const { t } = useTranslation();
	const listId = useId();
	const inputRef = useRef<HTMLInputElement>(null);
	const [text, setText] = useState("");
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const [highlight, setHighlight] = useState(0);
	useEffect(() => {
		const handle = window.setTimeout(() => setQuery(text.trim()), 120);
		return () => window.clearTimeout(handle);
	}, [text]);
	const candidates = useShareCandidates(orgId, query, open);
	const picked = useMemo(() => new Set(value.map(recipientKey)), [value]);

	const options = useMemo<Option[]>(() => {
		const list: Option[] = [];
		for (const candidate of candidates.data ?? []) {
			if (candidate.kind === "team") {
				if (!allowTeams || !candidate.teamId) continue;
				const key = `team:${candidate.teamId}`;
				if (picked.has(key)) continue;
				list.push({ id: key, kind: "team", candidate, disabled: unavailable?.has(key) ?? false });
			} else if (candidate.email) {
				const key = `email:${candidate.email.toLowerCase()}`;
				if (picked.has(key)) continue;
				list.push({ id: key, kind: "person", candidate, disabled: unavailable?.has(key) ?? false });
			}
		}
		const typed = text.trim().toLowerCase();
		if (looksLikeEmail(typed) && !picked.has(`email:${typed}`) && !list.some((option) => option.id === `email:${typed}`)) {
			list.push({ id: `email:${typed}`, kind: "email", email: typed, disabled: false });
		}
		if (onNewTeam && allowTeams) list.push({ id: "new-team", kind: "newTeam", disabled: false });
		return list;
	}, [allowTeams, candidates.data, onNewTeam, picked, text, unavailable]);

	const selectable = options.map((option, index) => (option.disabled ? -1 : index)).filter((index) => index >= 0);
	useEffect(() => {
		setHighlight(selectable[0] ?? 0);
		// Reset to the first pickable option whenever the list changes.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [options.length, query]);

	const pick = (option: Option) => {
		if (option.disabled) return;
		if (option.kind === "newTeam") {
			setOpen(false);
			onNewTeam?.(value);
			return;
		}
		const next: Recipient =
			option.kind === "team"
				? { kind: "team", teamId: option.candidate.teamId!, name: option.candidate.name, memberCount: option.candidate.memberCount ?? 0, memberNames: option.candidate.memberNames }
				: option.kind === "person"
					? { kind: "person", email: option.candidate.email!.toLowerCase(), name: option.candidate.name || undefined }
					: { kind: "person", email: option.email };
		onChange([...value, next]);
		setText("");
		setQuery("");
		inputRef.current?.focus();
	};

	const commitTyped = (): boolean => {
		const typed = text.trim().replace(/,$/, "").toLowerCase();
		if (!looksLikeEmail(typed)) return false;
		if (!picked.has(`email:${typed}`)) onChange([...value, { kind: "person", email: typed }]);
		setText("");
		return true;
	};

	const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
		if (event.key === "ArrowDown" || event.key === "ArrowUp") {
			event.preventDefault();
			setOpen(true);
			if (selectable.length === 0) return;
			const at = selectable.indexOf(highlight);
			const step = event.key === "ArrowDown" ? 1 : -1;
			setHighlight(selectable[(at + step + selectable.length) % selectable.length]!);
			return;
		}
		if (event.key === "Enter") {
			event.preventDefault();
			const option = options[highlight];
			if (open && text.trim() && option && !option.disabled) {
				pick(option);
			} else if (!commitTyped() && !text.trim() && value.length > 0) {
				onSubmit?.();
			}
			return;
		}
		if (event.key === "," || event.key === "Tab") {
			if (commitTyped()) event.preventDefault();
			return;
		}
		if (event.key === "Backspace" && text === "" && value.length > 0) {
			onChange(value.slice(0, -1));
			return;
		}
		if (event.key === "Escape" && open) {
			event.stopPropagation();
			setOpen(false);
		}
	};

	const showList = open && options.length > 0;
	const firstPerson = options.findIndex((option) => option.kind !== "team" && option.kind !== "newTeam");
	const firstTeam = options.findIndex((option) => option.kind === "team");

	return (
		<div className={cn("relative", className)}>
			<div
				className={cn(
					"flex min-h-9 w-full cursor-text flex-wrap items-center gap-1.5 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) py-1 pr-1 pl-2.5 text-control transition-[box-shadow,border-color] duration-(--duration-fast)",
					"focus-within:border-ring/70 focus-within:ring-3 focus-within:ring-ring/20",
					invalid && "border-destructive/60",
				)}
				onMouseDown={(event) => {
					if (event.target === event.currentTarget) {
						event.preventDefault();
						inputRef.current?.focus();
					}
				}}
				data-people-field=""
			>
				{value.length === 0 ? <Search className="size-icon-md shrink-0 text-muted-foreground" aria-hidden="true" /> : null}
				{value.map((recipient) => (
					<span
						key={recipientKey(recipient)}
						className={cn(
							"inline-flex h-6.5 max-w-full items-center gap-1.5 rounded-sm py-0 pr-1 pl-0.75 text-[12.5px] text-foreground",
							recipient.kind === "person" && !recipient.name ? "ring-1 ring-foreground/15 ring-inset" : "bg-foreground/10",
						)}
						data-people-token={recipient.kind}
					>
						{recipient.kind === "team" ? <TeamTile name={recipient.name} size="sm" /> : <PersonAvatar name={recipient.name} email={recipient.email} size="xs" />}
						<span className="min-w-0 truncate">{recipient.kind === "team" ? recipient.name : recipient.name || recipient.email}</span>
						<button
							type="button"
							className="grid size-4 shrink-0 place-items-center rounded-xs text-muted-foreground hover:bg-interactive-hover hover:text-foreground"
							aria-label={t("teams.field.remove", { name: recipient.kind === "team" ? recipient.name : recipient.name || recipient.email })}
							onClick={() => onChange(value.filter((other) => recipientKey(other) !== recipientKey(recipient)))}
						>
							<X className="size-2.5" aria-hidden="true" />
						</button>
					</span>
				))}
				<input
					ref={inputRef}
					value={text}
					autoFocus={autoFocus}
					onChange={(event) => {
						setText(event.target.value);
						setOpen(true);
					}}
					onFocus={() => setOpen(true)}
					onBlur={() => {
						window.setTimeout(() => setOpen(false), 120);
						commitTyped();
					}}
					onKeyDown={onKeyDown}
					placeholder={value.length === 0 ? placeholder : ""}
					aria-label={ariaLabel}
					role="combobox"
					aria-expanded={showList}
					aria-controls={listId}
					aria-autocomplete="list"
					aria-activedescendant={showList && options[highlight] ? `${listId}-${highlight}` : undefined}
					autoComplete="off"
					spellCheck={false}
					className="h-6.5 min-w-10 flex-[1_1_40px] bg-transparent text-control text-foreground outline-none placeholder:text-muted-foreground"
				/>
				{trailing}
			</div>
			{showList ? (
				<div
					id={listId}
					role="listbox"
					className="absolute top-[calc(100%+6px)] right-0 left-0 z-overlay flex max-h-80 flex-col gap-px overflow-y-auto rounded-lg border border-(--color-border-overlay) bg-overlay p-1 shadow-(--shadow-popover)"
					onMouseDown={(event) => event.preventDefault()}
					data-people-suggestions=""
				>
					{options.map((option, index) => (
						<div key={option.id} className="contents">
							{index === firstTeam ? <p className="px-2 pt-1.5 pb-1 text-caption text-passive">{t("teams.field.teams")}</p> : null}
							{index === firstPerson ? (
								<>
									{firstTeam >= 0 ? <div className="-mx-1 my-1 h-px bg-border" /> : null}
									<p className="px-2 pt-1.5 pb-1 text-caption text-passive">{t("teams.field.people")}</p>
								</>
							) : null}
							{option.kind === "newTeam" && index > 0 ? <div className="-mx-1 my-1 h-px bg-border" /> : null}
							<div
								id={`${listId}-${index}`}
								role="option"
								aria-selected={index === highlight}
								aria-disabled={option.disabled || undefined}
								className={cn(
									"flex min-h-8 cursor-default items-center gap-2.5 rounded-md px-2 py-1.5 text-control",
									option.disabled ? "text-muted-foreground" : "text-foreground",
									index === highlight && !option.disabled ? "bg-foreground/10" : "",
								)}
								onMouseEnter={() => !option.disabled && setHighlight(index)}
								onClick={() => pick(option)}
								data-people-option={option.kind}
							>
								{option.kind === "team" ? (
									<>
										<TeamTile name={option.candidate.name} />
										<span className="min-w-0 flex-1">
											<span className="block truncate">{option.candidate.name}</span>
											<span className="block truncate text-xs text-muted-foreground">{teamSummary(t, option.candidate)}</span>
										</span>
									</>
								) : option.kind === "person" ? (
									<>
										<PersonAvatar name={option.candidate.name} email={option.candidate.email} />
										<span className="min-w-0 flex-1">
											<span className="block truncate">{option.candidate.name || option.candidate.email}</span>
											{option.candidate.name ? <span className="block truncate text-xs text-muted-foreground">{option.candidate.email}</span> : null}
										</span>
									</>
								) : option.kind === "email" ? (
									<>
										<PersonAvatar email={option.email} invited />
										<span className="min-w-0 flex-1">
											<span className="block truncate">{t("teams.field.addEmail", { email: option.email })}</span>
											<span className="block truncate text-xs text-muted-foreground">{t("teams.field.addEmailHint")}</span>
										</span>
									</>
								) : (
									<>
										<Plus className="size-icon-md text-muted-foreground" aria-hidden="true" />
										<span className="text-muted-foreground">{t("teams.field.newTeam")}</span>
									</>
								)}
								{option.disabled ? <span className="ml-auto shrink-0 text-caption text-passive">{unavailableLabel ?? t("teams.field.hasAccess")}</span> : null}
							</div>
						</div>
					))}
				</div>
			) : null}
		</div>
	);
}

function teamSummary(t: (key: "teams.people" | "teams.field.teamMembers" | "teams.field.teamMembersAll", options?: Record<string, unknown>) => string, candidate: CloudCpShareCandidate): string {
	const people = t("teams.people", { count: candidate.memberCount ?? 0 });
	const names = candidate.memberNames ?? [];
	if (names.length === 0) return people;
	const others = Math.max(0, (candidate.memberCount ?? 0) - names.length);
	if (others === 0) return t("teams.field.teamMembersAll", { people, names: names.join(", ") });
	return t("teams.field.teamMembers", { count: others, people, names: names.join(", ") });
}
