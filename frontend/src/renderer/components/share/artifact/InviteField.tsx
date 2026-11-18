import { useId, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { looksLikeEmail } from "../../../hooks/useTeams";
import { cn } from "../../../lib/utils";

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
 * One email field, as session sharing's: type an address and press Enter (or
 * a comma) to share the link with it at once; a pasted list shares with
 * every address in it. An address that isn't one is named under the field
 * and stays typed, to be fixed. There is no separate Add step.
 */
export function InviteField({ busy = false, disabled = false, onSubmit }: {
	busy?: boolean;
	disabled?: boolean;
	/** Called with every address; resolves true when they were taken, clearing the field. */
	onSubmit: (emails: string[]) => Promise<boolean> | boolean;
}) {
	const { t } = useTranslation();
	const [typed, setTyped] = useState("");
	const [invalid, setInvalid] = useState<string>();
	const errorId = useId();

	const submit = async () => {
		const emails = withEmails([], splitEmails(typed));
		if (emails.length === 0 || disabled || busy) return;
		const bad = emails.find((email) => !looksLikeEmail(email));
		if (bad) {
			setInvalid(bad);
			return;
		}
		setInvalid(undefined);
		if (await onSubmit(emails)) setTyped((current) => current === typed ? "" : current);
	};
	const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
		if (event.key === "Enter" || ((event.key === "," || event.key === ";") && typed.trim())) {
			// Enter that confirms an input method's composition isn't a submit.
			if (event.nativeEvent.isComposing) return;
			event.preventDefault();
			void submit();
		}
	};

	return (
		<div className="flex flex-col gap-1.5" data-invite-field="">
			<input
				aria-describedby={invalid ? errorId : undefined}
				aria-invalid={invalid ? true : undefined}
				aria-label={t("artifacts.share.people.addByEmail")}
				className={cn(
					"h-control-md w-full rounded-md bg-input/50 px-2.5 text-control text-foreground outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed",
					invalid && "ring-1 ring-destructive/60",
				)}
				disabled={disabled}
				onChange={(event) => {
					setTyped(event.target.value);
					setInvalid(undefined);
				}}
				onKeyDown={onKeyDown}
				placeholder={t("artifacts.share.people.addByEmail")}
				type="email"
				value={typed}
			/>
			{invalid ? (
				<p id={errorId} role="alert" className="text-caption leading-4 text-error">
					{t("artifacts.share.people.notEmail", { email: invalid })}
				</p>
			) : null}
		</div>
	);
}
