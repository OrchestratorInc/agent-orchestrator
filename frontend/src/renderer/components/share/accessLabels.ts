import type { TFunction } from "i18next";
import type { CloudCpEffectiveAccess, CloudCpShareRole } from "../../lib/cloud-cp";

export type ShareScope = "session" | "project";
export type ShareModeCap = "read-only" | "standard" | "trusted";

/** "Watch", or what writing means at this scope. */
export function roleLabel(t: TFunction, role: CloudCpShareRole, scope: ShareScope): string {
	if (role === "viewer") return t("share.accessViewer");
	return scope === "project" ? t("share.accessStartSessions") : t("share.accessEditor");
}

export function capLabel(t: TFunction, cap: ShareModeCap | undefined): string {
	if (cap === "read-only") return t("share.modeReadOnly");
	if (cap === "trusted") return t("share.modeTrusted");
	return t("share.modeStandard");
}

/** The default limit for a level: read only to watch, standard to write. */
export function defaultCap(role: CloudCpShareRole): ShareModeCap {
	return role === "viewer" ? "read-only" : "standard";
}

/**
 * What one person ends up with and why, for a team's expanded member list:
 * "Write to the agent", "Trusted · shared directly", "Watch · via Data infra",
 * "Full access · workspace member".
 */
export function effectiveAccessText(t: TFunction, access: CloudCpEffectiveAccess, teamId: string, scope: ShareScope): { level: string; source?: string } {
	if (access.role === "member" || access.source === "workspace") {
		return { level: t("teams.access.full"), source: t("teams.access.workspaceMember") };
	}
	if (!access.role) return { level: t("teams.access.none") };
	const level = access.role === "editor" && access.modeCap && access.modeCap !== "standard"
		? capLabel(t, access.modeCap)
		: roleLabel(t, access.role, scope);
	switch (access.source) {
		case "direct":
		case "link":
			return { level, source: t("teams.access.sharedDirectly") };
		case "project":
			return { level, source: t("teams.access.fromProject") };
		case "team":
			return access.teamId && access.teamId !== teamId && access.teamName
				? { level, source: t("teams.access.viaTeam", { team: access.teamName }) }
				: { level };
		default:
			return { level };
	}
}

/** "Ben, Dana and Eli" in the person's language, with "and 4 more" past three. */
export function listNames(names: string[], locale: string, more: (count: number) => string): string {
	const shown = names.slice(0, 3);
	const rest = names.length - shown.length;
	const parts = rest > 0 ? [...shown, more(rest)] : shown;
	try {
		return new Intl.ListFormat(locale, { style: "long", type: "conjunction" }).format(parts);
	} catch {
		return parts.join(", ");
	}
}
