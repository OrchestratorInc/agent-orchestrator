import type { AgentAccount } from "../hooks/useAgentAccounts";

/** The closed palette an account can be marked with; the backend validates the same ids, and gives
 * each account of a provider its own color while one is free. */
export const ACCOUNT_COLORS = ["red", "orange", "amber", "green", "teal", "blue", "violet", "pink"] as const;
export type AccountColor = (typeof ACCOUNT_COLORS)[number];

const PROVIDER_LABELS: Record<string, string> = { claude: "Claude", codex: "Codex" };
const first = (text: string, count: number) => Array.from(text).slice(0, count).join("");

/**
 * The short letters an account shows on its badge, derived from what is already known:
 * two words give their initials, one word its first two characters, then the email's
 * local part, then the provider's name. Always uppercase.
 */
export function accountAbbreviation(account: { name?: string; email?: string; provider: AgentAccount["provider"] | string }): string {
	const words = (account.name ?? "").trim().split(/\s+/).filter(Boolean);
	let letters = "";
	if (words.length >= 2) letters = first(words[0], 1) + first(words[1], 1);
	else if (words.length === 1) letters = first(words[0], 2);
	else {
		const local = (account.email ?? "").split("@")[0].trim();
		letters = local ? first(local, 2) : first(PROVIDER_LABELS[account.provider] ?? account.provider, 2);
	}
	return letters.toUpperCase();
}

type LetterAccount = { id: string; name?: string; email?: string; provider: AgentAccount["provider"] | string; device?: boolean };

/** The letters an account could show, best first: its own abbreviation, then the initials of its
 * email's words (ronish.rohan gives RR), the email's first two characters, and the first characters
 * of the email's name and domain (ronish@untrivial.ai gives RU). */
function letterCandidates(account: LetterAccount): string[] {
	const [local = "", domain = ""] = (account.email ?? "").trim().split("@");
	const words = local.split(/[._+\-]+/).filter(Boolean);
	const candidates = [accountAbbreviation(account)];
	if (words.length >= 2) candidates.push(first(words[0], 1) + first(words[1], 1));
	if (local) candidates.push(first(local, 2));
	if (local && domain) candidates.push(first(local, 1) + first(domain, 1));
	return [...new Set(candidates.map((letters) => letters.toUpperCase()).filter(Boolean))];
}

/**
 * The letters each account shows, by account id, so no two accounts of a provider show the same
 * ones. Saved accounts take letters in the order given (the daemon lists them oldest first), each
 * its first candidate no earlier account of its provider took; when every candidate is taken, its
 * first letter and a number (R2, R3). The computer's own login goes last: its email is known only
 * while it is signed in, so going first would move every other account's letters as it signs in
 * and out.
 */
export function accountLetters(accounts: readonly LetterAccount[]): Map<string, string> {
	const taken = new Map<string, Set<string>>();
	const out = new Map<string, string>();
	const ordered = [...accounts.filter((account) => !account.device), ...accounts.filter((account) => account.device)];
	for (const account of ordered) {
		const used = taken.get(account.provider) ?? new Set<string>();
		taken.set(account.provider, used);
		const candidates = letterCandidates(account);
		let letters = candidates.find((candidate) => !used.has(candidate));
		if (!letters) {
			const lead = first(candidates[0] ?? "?", 1);
			for (let n = 2; !letters; n++) if (!used.has(lead + n)) letters = lead + n;
		}
		used.add(letters);
		out.set(account.id, letters);
	}
	return out;
}
