import type { AgentAccount } from "../hooks/useAgentAccounts";

/** The closed palette an account can be marked with; the backend validates the same ids. */
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
