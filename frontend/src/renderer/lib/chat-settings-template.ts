import type { ChatConfigOption, ChatSkill } from "../types/conversation";

const STORAGE_KEY = "ao.chat.settingsTemplate.v1";

/**
 * The turn settings a session of this agent reported, kept so a chat that has no
 * session yet can draw the very same controls the session will. The choices are
 * the agent's; the values a new chat shows are its own.
 */
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
		// Only polish: without it a new chat draws AO's own controls.
	}
}

const SKILLS_KEY = "ao.chat.skills.v1";
const PROJECT_SKILLS_KEY = "ao.chat.projectSkills.v1";
/**
 * Projects whose own list is kept. A list can run to hundreds of skills with long
 * descriptions, so only the most recently used projects keep theirs; the rest fall
 * back to the agent-wide list.
 */
export const REMEMBERED_SKILL_PROJECTS = 12;

type SkillLists = Record<string, ChatSkill[]>;

// Every chat that opens reads these, and together they can run to hundreds of
// kilobytes. Parsing that again on every chat switch would cost the switch time,
// so the parsed copy is kept for as long as the stored text is unchanged (another
// window's write changes the text, so it is still picked up).
const parsedLists = new Map<string, { raw: string; lists: SkillLists }>();

function readSkillLists(key: string): SkillLists {
	try {
		const raw = storage()?.getItem(key) ?? "{}";
		const cached = parsedLists.get(key);
		if (cached?.raw === raw) return cached.lists;
		const parsed: unknown = JSON.parse(raw);
		const lists = parsed && typeof parsed === "object" && !Array.isArray(parsed) ? (parsed as SkillLists) : {};
		parsedLists.set(key, { raw, lists });
		return lists;
	} catch {
		return {};
	}
}

function writeSkillLists(key: string, lists: SkillLists): void {
	const raw = JSON.stringify(lists);
	storage()?.setItem(key, raw);
	parsedLists.set(key, { raw, lists });
}

/**
 * The skills a session of this agent last listed, for a "/" menu that has no live
 * list yet: a new chat, or a session whose agent is still starting or asleep.
 *
 * With a project, that project's own list wins, since project skills
 * (`.claude/skills`, `.agents/skills`) differ between repos; otherwise the list
 * any session of this agent last reported.
 */
export function readChatSkills(harness: string, hostId?: string, projectId?: string): ChatSkill[] {
	if (!harness) return [];
	if (projectId) {
		const own = readSkillLists(PROJECT_SKILLS_KEY)[`${keyFor(harness, hostId)}:${projectId}`];
		if (Array.isArray(own)) return own;
	}
	const skills = readSkillLists(SKILLS_KEY)[keyFor(harness, hostId)];
	return Array.isArray(skills) ? skills : [];
}

export function rememberChatSkills(harness: string, hostId: string | undefined, skills: ChatSkill[], projectId?: string): void {
	try {
		const key = keyFor(harness, hostId);
		const all = readSkillLists(SKILLS_KEY);
		if (JSON.stringify(all[key]) !== JSON.stringify(skills)) writeSkillLists(SKILLS_KEY, { ...all, [key]: skills });
		if (!projectId) return;
		// Insertion order is recency: each write moves its project to the end, even
		// when its list is unchanged, so a project in daily use is never the one dropped.
		const projects = readSkillLists(PROJECT_SKILLS_KEY);
		const projectKey = `${key}:${projectId}`;
		const keys = Object.keys(projects);
		if (keys[keys.length - 1] === projectKey && JSON.stringify(projects[projectKey]) === JSON.stringify(skills)) return;
		const next: SkillLists = { ...projects };
		delete next[projectKey];
		next[projectKey] = skills;
		writeSkillLists(PROJECT_SKILLS_KEY, Object.fromEntries(Object.entries(next).slice(-REMEMBERED_SKILL_PROJECTS)));
	} catch {
		// Only polish: without it a new chat offers no "/" skills.
	}
}
