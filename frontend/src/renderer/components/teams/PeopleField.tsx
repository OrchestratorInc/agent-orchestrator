import { Plus, Search, X } from "@/components/app-icons";
import { Popover as PopoverPrimitive } from "radix-ui";
import { type KeyboardEvent, type ReactNode, type RefObject, useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import type { CloudCpShareCandidate } from "../../lib/cloud-cp";
import { looksLikeEmail, useShareCandidates } from "../../hooks/useTeams";
import { useCloudSession } from "../../lib/cloud-session";
import { PersonAvatar, TeamTile } from "./TeamVisuals";
import { Button } from "../ui/button";

/** Someone picked in a people field: a team, or a person by email. */
export type Recipient =
	| { kind: "team"; teamId: string; name: string; memberCount: number; memberNames?: string[] }
	| { kind: "person"; email: string; name?: string };

export function recipientKey(recipient: Recipient): string {
	return recipient.kind === "team" ? `team:${recipient.teamId}` : `email:${recipient.email.toLowerCase()}`;
}

type Option =
	| { id: string; kind: "team"; candidate: CloudCpShareCandidate; disabled: boolean }
	| { id: string; kind: "person"; candidate: CloudCpShareCandidate; disabled: boolean; inWorkspace?: boolean; self?: boolean }
	| { id: string; kind: "email"; email: string; disabled: boolean; self?: boolean }
	| { id: string; kind: "newTeam"; disabled: false };

/**
 * One field for people and teams: type to search the workspace's teams and
 * the people it knows, or a full email to add anyone. Picks become tokens in
 * the field. Arrow keys move through suggestions, Enter or comma picks,
 * Backspace on an empty field removes the last pick, Escape closes.
 *
 * Enter picks the first suggestion only once the suggestions are for what is
 * typed (a fast paste must not pick a match for the text before it), else it
 * adds the typed email. "New team…" is never the first suggestion; it is
 * reached with the arrow keys or the pointer.
 */
export function PeopleField({
	orgId,
	value,
	onChange,
	allowTeams = true,
	unavailable,
	unavailableLabel,
	workspaceHasAccess = false,
	placeholder,
	trailing,
	onNewTeam,
	onSubmit,
	autoFocus,
	ariaLabel,
	invalid,
	className,
	inputRef: externalInputRef,
}: {
	orgId: string;
	value: Recipient[];
	onChange: (next: Recipient[]) => void;
	allowTeams?: boolean;
	/** Keys (recipientKey) of people and teams that can't be picked, such as those who already have access. */
	unavailable?: ReadonlySet<string>;
	unavailableLabel?: string;
	/** People in the workspace already have full access, so they can't be picked (sharing, not teams). */
	workspaceHasAccess?: boolean;
	placeholder: string;
	/** Sits at the field's trailing edge, such as an access menu. */
	trailing?: ReactNode;
	/** Offered as "New team…", with whoever is picked so far and what is typed. */
	onNewTeam?: (people: Recipient[], typed: string) => void;
	/** Enter in an empty field with picks in it. */
	onSubmit?: () => void;
	autoFocus?: boolean;
	ariaLabel: string;
	invalid?: boolean;
	className?: string;
	/** The text input, for a caller that moves focus back to the field. */
	inputRef?: RefObject<HTMLInputElement | null>;
}) {
	const { t } = useTranslation();
	const listId = useId();
	const ownInputRef = useRef<HTMLInputElement>(null);
	const inputRef = externalInputRef ?? ownInputRef;
	const fieldRef = useRef<HTMLDivElement>(null);
	// The list is portaled outside any modal dialog, whose scroll lock cancels
	// wheel events there; scroll it directly, as SearchablePicker does.
	const listRef = useCallback((list: HTMLDivElement | null) => {
		if (!list) return;
		const onWheel = (event: WheelEvent) => {
			if (list.scrollHeight <= list.clientHeight) return;
			list.scrollTop += event.deltaMode === 1 ? event.deltaY * 16 : event.deltaMode === 2 ? event.deltaY * list.clientHeight : event.deltaY;
			event.preventDefault();
		};
		list.addEventListener("wheel", onWheel, { passive: false });
		return () => list.removeEventListener("wheel", onWheel);
	}, []);
	const [text, setText] = useState("");
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const blurCloseTimer = useRef<number | undefined>(undefined);
	useEffect(() => () => window.clearTimeout(blurCloseTimer.current), []);
	// The suggestion the person moved to with the arrow keys or the pointer;
	// until they do, the highlight is the default one below.
	const [moved, setMoved] = useState<string | null>(null);
	useEffect(() => {
		const handle = window.setTimeout(() => setQuery(text.trim()), 120);
		return () => window.clearTimeout(handle);
	}, [text]);
	// Loads from the start, so the suggestions are there when the list opens
	// instead of the list growing under the pointer.
	const candidates = useShareCandidates(orgId, query, true);
	// The person using the app: their own email is never someone to add.
	const me = useCloudSession().session?.user.email?.toLowerCase() ?? "";
	const picked = useMemo(() => new Set(value.map(recipientKey)), [value]);
	const typed = text.trim();
	// The suggestions answer what is typed, not text from before the last keystrokes.
	const fresh = query === typed && candidates.data !== undefined && !candidates.isPlaceholderData;

	const options = useMemo<Option[]>(() => {
		const list: Option[] = [];
		// Until the answer for what's typed arrives, the last answer is narrowed
		// to what still matches (the control plane's rule: the name or email
		// contains the text), so nobody who doesn't match shows for a moment.
		const needle = typed.toLowerCase();
		const stillMatches = (candidate: CloudCpShareCandidate) =>
			fresh || !needle || `${candidate.name}\n${candidate.email ?? ""}`.toLowerCase().includes(needle);
		for (const candidate of candidates.data ?? []) {
			if (!stillMatches(candidate)) continue;
			if (candidate.kind === "team") {
				if (!allowTeams || !candidate.teamId) continue;
				const key = `team:${candidate.teamId}`;
				if (picked.has(key)) continue;
				list.push({ id: key, kind: "team", candidate, disabled: unavailable?.has(key) ?? false });
			} else if (candidate.email) {
				const key = `email:${candidate.email.toLowerCase()}`;
				if (picked.has(key)) continue;
				const self = Boolean(me) && candidate.email.toLowerCase() === me;
				const inWorkspace = workspaceHasAccess && candidate.workspaceMember === true;
				list.push({ id: key, kind: "person", candidate, disabled: self || inWorkspace || (unavailable?.has(key) ?? false), inWorkspace, self });
			}
		}
		const email = text.trim().toLowerCase();
		if (looksLikeEmail(email) && !picked.has(`email:${email}`) && !list.some((option) => option.id === `email:${email}`)) {
			const self = email === me;
			list.push({ id: `email:${email}`, kind: "email", email, disabled: self || (unavailable?.has(`email:${email}`) ?? false), self });
		}
		if (onNewTeam && allowTeams) list.push({ id: "new-team", kind: "newTeam", disabled: false });
		return list;
	}, [allowTeams, candidates.data, fresh, me, onNewTeam, picked, text, typed, unavailable, workspaceHasAccess]);

	// With something typed, the first suggestion that answers it is
	// highlighted: a match once the results are current, else the typed email.
	// A whole email typed means that address and no other: suggestions whose
	// email only contains it (tiwari@ in vaibhaavtiwari@) are never the default,
	// or Enter would share with someone else. With nothing typed nothing is
	// highlighted, so Enter shares what is picked.
	const exactId = looksLikeEmail(typed.toLowerCase()) ? `email:${typed.toLowerCase()}` : "";
	const defaultOption = !typed
		? undefined
		: exactId
			? options.find((option) => option.id === exactId && !option.disabled)
			: options.find((option) => !option.disabled && (option.kind === "email" || ((option.kind === "team" || option.kind === "person") && fresh)));
	const movedOption = moved ? options.find((option) => option.id === moved && !option.disabled) : undefined;
	const highlighted = movedOption ?? defaultOption;
	const highlight = highlighted ? options.indexOf(highlighted) : -1;

	// Keep the highlighted suggestion in view as the arrow keys move it.
	useEffect(() => {
		if (!open || highlight < 0) return;
		document.getElementById(`${listId}-${highlight}`)?.scrollIntoView({ block: "nearest" });
	}, [highlight, listId, open]);

	const pick = (option: Option) => {
		if (option.disabled) return;
		setMoved(null);
		if (option.kind === "newTeam") {
			setOpen(false);
			onNewTeam?.(value, typed);
			setText("");
			setQuery("");
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
		// Close so what was picked, and what it means, is in view; typing,
		// a click or an arrow key opens the suggestions again.
		setOpen(false);
		inputRef.current?.focus();
	};

	const commitTyped = (): boolean => {
		const typed = text.trim().replace(/,$/, "").toLowerCase();
		if (!looksLikeEmail(typed)) return false;
		// Not you, nor anyone the list says can't be added (in the workspace,
		// already has access): the list shows why.
		if (typed === me || options.some((option) => option.id === `email:${typed}` && option.disabled)) {
			setOpen(true);
			return false;
		}
		if (!picked.has(`email:${typed}`)) onChange([...value, { kind: "person", email: typed }]);
		setText("");
		setMoved(null);
		// Closed, so the suggestions don't open over what comes after the field.
		setOpen(false);
		return true;
	};

	const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
		if (event.key === "ArrowDown" || event.key === "ArrowUp") {
			event.preventDefault();
			setOpen(true);
			const selectable = options.filter((option) => !option.disabled);
			if (selectable.length === 0) return;
			const down = event.key === "ArrowDown";
			const at = highlighted ? selectable.indexOf(highlighted) : -1;
			const next = at < 0 ? (down ? 0 : selectable.length - 1) : (at + (down ? 1 : -1) + selectable.length) % selectable.length;
			setMoved(selectable[next]!.id);
			return;
		}
		if (event.key === "Enter") {
			// Enter that confirms an input method's composition isn't a pick.
			if (event.nativeEvent.isComposing) return;
			event.preventDefault();
			if (showList && highlighted) {
				pick(highlighted);
			} else if (!commitTyped() && !typed && value.length > 0) {
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

	// Until the first suggestions arrive, the list stays shut rather than
	// opening with "New team…" alone and growing.
	const loadingFirst = candidates.data === undefined && candidates.fetchStatus === "fetching";
	const showList = open && options.length > 0 && !loadingFirst;
	const firstPerson = options.findIndex((option) => option.kind !== "team" && option.kind !== "newTeam");
	const firstTeam = options.findIndex((option) => option.kind === "team");

	return (
		<PopoverPrimitive.Root open={showList}>
			<div className={cn("relative", className)}>
				<PopoverPrimitive.Anchor asChild>
					<div
						ref={fieldRef}
						className={cn(
							"flex min-h-9 w-full cursor-text flex-wrap items-center gap-1.5 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) py-1 pr-1 pl-2.5 text-control transition-[box-shadow,border-color] duration-(--duration-fast)",
							"focus-within:border-ring/70 focus-within:ring-3 focus-within:ring-ring/20",
							invalid && "border-destructive/60",
						)}
						onMouseDown={(event) => {
							if (event.target === event.currentTarget) {
								event.preventDefault();
								inputRef.current?.focus();
								setOpen(true);
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
								<Button
									type="button"
									variant="quiet"
									size="none"
									className="size-4 rounded-xs [&_svg]:size-2.5"
									aria-label={t("teams.field.remove", { name: recipient.kind === "team" ? recipient.name : recipient.name || recipient.email })}
									onClick={() => onChange(value.filter((other) => recipientKey(other) !== recipientKey(recipient)))}
								>
									<X aria-hidden="true" />
								</Button>
							</span>
						))}
						<input
							ref={inputRef}
							value={text}
							autoFocus={autoFocus}
							onChange={(event) => {
								setText(event.target.value);
								setMoved(null);
								setOpen(true);
							}}
							// Focus alone doesn't open the suggestions: a dialog opening
							// with focus here would cover what it shows. A click, typing
							// or an arrow key does.
							onClick={() => setOpen(true)}
							onBlur={() => {
								window.clearTimeout(blurCloseTimer.current);
								blurCloseTimer.current = window.setTimeout(() => setOpen(false), 120);
								commitTyped();
							}}
							onKeyDown={onKeyDown}
							placeholder={value.length === 0 ? placeholder : ""}
							aria-label={ariaLabel}
							role="combobox"
							aria-expanded={showList}
							aria-controls={listId}
							aria-autocomplete="list"
							aria-activedescendant={showList && highlight >= 0 ? `${listId}-${highlight}` : undefined}
							autoComplete="off"
							spellCheck={false}
							className="h-6.5 min-w-10 flex-[1_1_40px] bg-transparent text-control text-foreground outline-none placeholder:text-muted-foreground"
						/>
						{trailing}
					</div>
				</PopoverPrimitive.Anchor>
				{/* Above everything, so a short dialog doesn't clip the suggestions. */}
				<PopoverPrimitive.Portal>
					<PopoverPrimitive.Content
						ref={listRef}
						id={listId}
						role="listbox"
						align="start"
						sideOffset={6}
						collisionPadding={8}
						onOpenAutoFocus={(event) => event.preventDefault()}
						onCloseAutoFocus={(event) => event.preventDefault()}
						onEscapeKeyDown={() => setOpen(false)}
						onInteractOutside={(event) => {
							if (!fieldRef.current?.contains(event.target as Node)) setOpen(false);
						}}
						className="z-overlay flex max-h-[min(20rem,var(--radix-popover-content-available-height))] w-(--radix-popover-trigger-width) flex-col gap-px overflow-y-auto rounded-lg border border-(--color-border-overlay) bg-overlay p-1 shadow-(--shadow-popover) outline-none"
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
									onMouseEnter={() => !option.disabled && setMoved(option.id)}
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
									) : option.kind === "email" && option.self ? (
										<>
											<PersonAvatar email={option.email} />
											<span className="min-w-0 flex-1 truncate">{option.email}</span>
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
									{option.disabled ? (
									<span className="ml-auto shrink-0 text-caption text-passive">
										{(option.kind === "person" || option.kind === "email") && option.self
											? t("teams.field.you")
											: option.kind === "person" && option.inWorkspace ? t("teams.field.inWorkspace") : unavailableLabel ?? t("teams.field.hasAccess")}
									</span>
								) : null}
								</div>
							</div>
						))}
					</PopoverPrimitive.Content>
				</PopoverPrimitive.Portal>
			</div>
		</PopoverPrimitive.Root>
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
