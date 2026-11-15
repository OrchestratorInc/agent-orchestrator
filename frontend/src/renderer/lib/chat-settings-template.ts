import type { ChatConfigOption, ChatSkill } from "../types/conversation";

const STORAGE_KEY = "ao.chat.settingsTemplate.v1";

export type ChatSettingsTemplate = {
	options: ChatConfigOption[];
};

const keyFor = (harness: string, hostId?: string) => `${hostId ?? "local"}:${harness}`;

function storage(): Storage | undefined {
	try {
		return window.localStorage;
	} catch {
		return undefined;
	}
}

function readAll(): Record<string, ChatSettingsTemplate> {
	try {
		const parsed: unknown = JSON.parse(storage()?.getItem(STORAGE_KEY) ?? "{}");
		return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as Record<string, ChatSettingsTemplate>) : {};
	} catch {
		return {};
	}
}

export function readChatSettingsTemplate(harness: string, hostId?: string): ChatSettingsTemplate | undefined {
	if (!harness) return undefined;
	const template = readAll()[keyFor(harness, hostId)];
	return template && Array.isArray(template.options) ? template : undefined;
}

export function rememberChatSettingsTemplate(harness: string, hostId: string | undefined, template: ChatSettingsTemplate): void {
	const all = readAll();
	const key = keyFor(harness, hostId);
	const next = { options: template.options };
	if (JSON.stringify(all[key]) === JSON.stringify(next)) return;
	all[key] = next;
	try {
		storage()?.setItem(STORAGE_KEY, JSON.stringify(all));
	} catch {
	}
}

const SKILLS_KEY = "ao.chat.skills.v1";

export function readChatSkills(harness: string, hostId?: string): ChatSkill[] {
	if (!harness) return [];
	try {
		const all: unknown = JSON.parse(storage()?.getItem(SKILLS_KEY) ?? "{}");
		const skills = all && typeof all === "object" ? (all as Record<string, unknown>)[keyFor(harness, hostId)] : undefined;
		return Array.isArray(skills) ? (skills as ChatSkill[]) : [];
	} catch {
		return [];
	}
}

export function rememberChatSkills(harness: string, hostId: string | undefined, skills: ChatSkill[]): void {
	try {
		const all: Record<string, ChatSkill[]> = JSON.parse(storage()?.getItem(SKILLS_KEY) ?? "{}");
		const key = keyFor(harness, hostId);
		if (JSON.stringify(all[key]) === JSON.stringify(skills)) return;
		all[key] = skills;
		storage()?.setItem(SKILLS_KEY, JSON.stringify(all));
	} catch {
	}
}
