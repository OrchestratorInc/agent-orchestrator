import { useId, useRef, useState, type ClipboardEvent, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { X } from "@/components/app-icons";
import { looksLikeEmail } from "../../../hooks/useTeams";
import { cn } from "../../../lib/utils";
import { Button } from "../../ui/button";

/** Splits typed or pasted text into addresses: commas, semicolons and whitespace separate them. */
export function splitEmails(text: string): string[] {
	return text.split(/[\s,;]+/).map((part) => part.trim()).filter(Boolean);
}

/** Adds addresses to chips, lowercased, skipping ones already there. */
function withEmails(chips: readonly string[], added: readonly string[]): string[] {
	const next = [...chips];
	for (const email of added) {
		const lower = email.toLowerCase();
		if (!next.some((chip) => chip.toLowerCase() === lower)) next.push(email);
	}
	return next;
}

/**
 * Email chips beside one action: Enter, a comma or a space turns what was typed
 * into a chip, and a pasted list becomes several. The action stays disabled
 * while a chip is not an email address, and the first such chip is named under
 * the field. Submitting takes what is still typed too.
 */
export function InviteField({ actionLabel, busy = false, disabled = false, onSubmit }: {
	/** Invite on a live link; Add while setting a link up before it is published. */
	actionLabel: string;
	busy?: boolean;
	disabled?: boolean;
	/** Called with every address; resolves true when they were taken, clearing the field. */
	onSubmit: (emails: string[]) => Promise<boolean> | boolean;
}) {
	const { t } = useTranslation();
	const [chips, setChips] = useState<string[]>([]);
	const [typed, setTyped] = useState("");
	const inputRef = useRef<HTMLInputElement>(null);
	const errorId = useId();
	const invalid = chips.find((chip) => !looksLikeEmail(chip));
	const pending = withEmails(chips, splitEmails(typed));
	const canSubmit = !disabled && !busy && pending.length > 0 && pending.every(looksLikeEmail);

	const commit = (text: string) => {
		const added = splitEmails(text);
		if (added.length > 0) setChips((current) => withEmails(current, added));
		setTyped("");
	};
	const submit = async () => {
		if (!canSubmit) {
			commit(typed);
			return;
		}
		setChips(pending);
		setTyped("");
		// Only what was sent leaves the field: anything added while it was on
		// its way stays, still to be sent.
		if (await onSubmit(pending)) {
			const sent = new Set(pending.map((email) => email.toLowerCase()));
			setChips((current) => current.filter((chip) => !sent.has(chip.toLowerCase())));
		}
	};
	const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
		if (event.key === "Enter") {
			event.preventDefault();
			// Enter on a typed address makes it a chip; on nothing typed, it submits.
			if (typed.trim()) commit(typed);
			else void submit();
		} else if ((event.key === "," || event.key === " " || event.key === ";") && typed.trim()) {
			event.preventDefault();
			commit(typed);
		} else if (event.key === "Backspace" && typed === "" && chips.length > 0) {
			setChips((current) => current.slice(0, -1));
		}
	};
	const onPaste = (event: ClipboardEvent<HTMLInputElement>) => {
		const text = event.clipboardData.getData("text");
		if (splitEmails(text).length < 2) return;
		event.preventDefault();
		commit(`${typed} ${text}`);
	};

	return (
		<div className="flex flex-col gap-1.5" data-invite-field="">
			<div className="flex items-start gap-1.5">
				<div
					className={cn(
						"flex min-h-control-md min-w-0 flex-1 cursor-text flex-wrap items-center gap-1 rounded-md bg-input/50 px-1.5 py-1 focus-within:ring-1 focus-within:ring-ring",
						invalid && "ring-1 ring-destructive/60",
					)}
					onClick={() => inputRef.current?.focus()}
				>
					{chips.map((chip) => {
						const bad = !looksLikeEmail(chip);
						return (
							<span
								key={chip}
								className={cn(
									"inline-flex h-5 max-w-full items-center gap-0.5 rounded-sm pl-1.5 text-caption",
									bad ? "bg-destructive/15 text-destructive" : "bg-muted text-foreground",
								)}
								data-invite-chip={bad ? "invalid" : ""}
							>
								<span className="truncate">{chip}</span>
								<Button
									aria-label={t("artifacts.share.people.removeChip", { email: chip })}
									className="size-4 rounded-sm"
									onClick={(event) => {
										event.stopPropagation();
										setChips((current) => current.filter((value) => value !== chip));
									}}
									size="none"
									type="button"
									variant="ghost"
								>
									<X aria-hidden="true" className="size-2.5" />
								</Button>
							</span>
						);
					})}
					<input
						ref={inputRef}
						aria-describedby={invalid ? errorId : undefined}
						aria-invalid={invalid ? true : undefined}
						aria-label={t("artifacts.share.people.addByEmail")}
						className="h-5 min-w-16 flex-1 bg-transparent px-1 text-caption text-foreground outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed"
						disabled={disabled}
						onBlur={() => commit(typed)}
						onChange={(event) => setTyped(event.target.value)}
						onKeyDown={onKeyDown}
						onPaste={onPaste}
						placeholder={chips.length === 0 ? t("artifacts.share.people.addByEmail") : undefined}
						type="email"
						value={typed}
					/>
				</div>
				<Button disabled={!canSubmit} onClick={() => void submit()} size="sm" type="button" variant="secondary">
					{actionLabel}
				</Button>
			</div>
			{invalid ? (
				<p id={errorId} role="alert" className="text-caption leading-4 text-error">
					{t("artifacts.share.people.notEmail", { email: invalid })}
				</p>
			) : null}
		</div>
	);
}
