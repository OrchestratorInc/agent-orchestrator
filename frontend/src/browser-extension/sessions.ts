// AO session ↔ Chrome tab group ownership. Each AO session works only in its
// own tab group ("AO · <session>") in the user's window. Tabs get AO's
// logical ids (t1, t2, …). A tab the user drags out of the group is no longer
// the agent's; a tab the user drags in becomes the agent's.

import type { ChromeApi, Tab } from "./chrome-api";
import { BridgeError } from "./protocol";

const GROUP_COLORS = ["blue", "cyan", "green", "purple", "pink", "orange", "yellow", "red"];
const STORAGE_KEY = "aoSessions";

export type SessionTabs = {
	sessionId: string;
	groupId?: number;
	windowId?: number;
	tabs: Record<string, number>;
	activeTabId?: string;
	nextTab: number;
};

export type ActiveTab = { logicalId: string; tabId: number; tab: Tab };

export function groupTitle(sessionId: string): string {
	return `AO · ${sessionId.length > 24 ? `${sessionId.slice(0, 23)}…` : sessionId}`;
}

export function groupColor(sessionId: string): string {
	let hash = 0;
	for (const char of sessionId) hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
	return GROUP_COLORS[hash % GROUP_COLORS.length]!;
}

export class SessionTabRegistry {
	private sessions = new Map<string, SessionTabs>();
	private loaded?: Promise<void>;
	/** Tabs this registry is creating; their grouping is not a user hand-off. */
	private readonly creating = new Set<number>();
	private readonly ownershipListeners = new Set<() => void>();

	constructor(private readonly chrome: ChromeApi) {}

	onOwnershipChanged(listener: () => void): void {
		this.ownershipListeners.add(listener);
	}

	/** Restore ownership after a service-worker restart and track user changes. */
	start(): void {
		this.chrome.tabs.onRemoved.addListener((tabId) => void this.forgetTab(tabId));
		this.chrome.tabs.onUpdated.addListener((tabId, change) => {
			if (change.groupId !== undefined) void this.groupChanged(tabId, change.groupId);
		});
		this.chrome.tabGroups.onRemoved.addListener((group) => void this.groupRemoved(group.id));
	}

	private async load(): Promise<void> {
		this.loaded ??= this.chrome.storage.session.get(STORAGE_KEY).then((stored) => {
			const saved = stored[STORAGE_KEY];
			if (saved && typeof saved === "object") {
				for (const session of Object.values(saved as Record<string, SessionTabs>)) this.sessions.set(session.sessionId, session);
			}
		});
		return this.loaded;
	}

	private async save(): Promise<void> {
		for (const listener of this.ownershipListeners) listener();
		await this.chrome.storage.session.set({ [STORAGE_KEY]: Object.fromEntries(this.sessions) });
	}

	async get(sessionId: string): Promise<SessionTabs | undefined> {
		await this.load();
		return this.sessions.get(sessionId);
	}

	async all(): Promise<SessionTabs[]> {
		await this.load();
		return [...this.sessions.values()];
	}

	/** The session's state, creating its tab group on first use. */
	async ensure(sessionId: string): Promise<SessionTabs> {
		await this.load();
		let session = this.sessions.get(sessionId);
		if (session) await this.prune(session);
		if (session && Object.keys(session.tabs).length > 0) return session;
		session ??= { sessionId, tabs: {}, nextTab: 1 };
		this.sessions.set(sessionId, session);
		await this.createTab(session, undefined);
		return session;
	}

	/** The session's active tab, verified to still belong to its group. */
	async active(session: SessionTabs): Promise<ActiveTab> {
		const logicalId = session.activeTabId && session.tabs[session.activeTabId] !== undefined ? session.activeTabId : Object.keys(session.tabs)[0];
		if (!logicalId) throw new BridgeError("BROWSER_TARGET_UNAVAILABLE", "The session has no Chrome tab");
		return this.tab(session, logicalId);
	}

	async tab(session: SessionTabs, logicalId: string): Promise<ActiveTab> {
		const tabId = session.tabs[logicalId];
		if (tabId === undefined) throw new BridgeError("TAB_NOT_FOUND", `Browser tab ${logicalId} does not exist`);
		let tab: Tab;
		try {
			tab = await this.chrome.tabs.get(tabId);
		} catch {
			delete session.tabs[logicalId];
			await this.save();
			throw new BridgeError("TAB_NOT_FOUND", `Browser tab ${logicalId} was closed`);
		}
		if (session.groupId !== undefined && tab.groupId !== session.groupId) {
			delete session.tabs[logicalId];
			await this.save();
			throw new BridgeError("BROWSER_TARGET_MISMATCH", `Tab ${logicalId} left this session's AO tab group`);
		}
		return { logicalId, tabId, tab };
	}

	async createTab(session: SessionTabs, url: string | undefined): Promise<ActiveTab> {
		let windowId = session.windowId;
		if (windowId !== undefined) {
			const windows = await this.chrome.tabs.query({ windowId }).catch(() => []);
			if (windows.length === 0) windowId = undefined;
		}
		if (windowId === undefined) {
			const focused = await this.chrome.windows.getLastFocused({ windowTypes: ["normal"] }).catch(() => undefined);
			windowId = focused?.id ?? (await this.chrome.windows.create({ type: "normal", focused: false })).id;
		}
		const tab = await this.chrome.tabs.create({ url: url ?? "about:blank", active: false, windowId });
		if (tab.id === undefined) throw new BridgeError("BROWSER_TARGET_UNAVAILABLE", "Chrome did not create a tab");
		this.creating.add(tab.id);
		try {
			return await this.adopt(session, tab, windowId!);
		} finally {
			// Chrome reports the grouping asynchronously; let that event pass.
			const id = tab.id;
			setTimeout(() => this.creating.delete(id), 1_000);
		}
	}

	private async adopt(session: SessionTabs, tab: Tab & { id?: number }, windowId: number): Promise<ActiveTab> {
		const tabId = tab.id!;
		let groupId = session.groupId;
		try {
			groupId = await this.chrome.tabs.group(
				groupId !== undefined ? { tabIds: tabId, groupId } : { tabIds: tabId, createProperties: { windowId } },
			);
		} catch {
			// The old group is gone (closed or in another window): start a new one.
			groupId = await this.chrome.tabs.group({ tabIds: tabId, createProperties: { windowId } });
		}
		if (groupId !== session.groupId) {
			await this.chrome.tabGroups.update(groupId, { title: groupTitle(session.sessionId), color: groupColor(session.sessionId), collapsed: false });
		}
		session.groupId = groupId;
		session.windowId = windowId;
		const logicalId = `t${session.nextTab++}`;
		session.tabs[logicalId] = tabId;
		session.activeTabId = logicalId;
		await this.save();
		return { logicalId, tabId, tab };
	}

	async select(session: SessionTabs, logicalId: string): Promise<ActiveTab> {
		const active = await this.tab(session, logicalId);
		session.activeTabId = logicalId;
		await this.save();
		return active;
	}

	async close(session: SessionTabs, logicalId: string): Promise<void> {
		if (session.tabs[logicalId] === undefined) throw new BridgeError("TAB_NOT_FOUND", `Browser tab ${logicalId} does not exist`);
		if (Object.keys(session.tabs).length === 1) throw new BridgeError("CANNOT_CLOSE_LAST_TAB", "The only browser tab cannot be closed");
		const tabId = session.tabs[logicalId]!;
		delete session.tabs[logicalId];
		if (session.activeTabId === logicalId) session.activeTabId = Object.keys(session.tabs)[0];
		await this.save();
		await this.chrome.tabs.remove(tabId).catch(() => undefined);
	}

	/**
	 * Release a session: close tabs still on about:blank, ungroup the rest so
	 * the user keeps any page they were looking at.
	 */
	async destroy(sessionId: string): Promise<void> {
		await this.load();
		const session = this.sessions.get(sessionId);
		if (!session) return;
		this.sessions.delete(sessionId);
		await this.save();
		const keep: number[] = [];
		for (const tabId of Object.values(session.tabs)) {
			const tab = await this.chrome.tabs.get(tabId).catch(() => undefined);
			if (!tab) continue;
			if (!tab.url || tab.url === "about:blank") await this.chrome.tabs.remove(tabId).catch(() => undefined);
			else keep.push(tabId);
		}
		if (keep.length > 0) await this.chrome.tabs.ungroup(keep).catch(() => undefined);
	}

	/** Find which session owns a Chrome tab, if any. */
	ownerOf(tabId: number): { session: SessionTabs; logicalId: string } | undefined {
		for (const session of this.sessions.values()) {
			for (const [logicalId, id] of Object.entries(session.tabs)) if (id === tabId) return { session, logicalId };
		}
		return undefined;
	}

	private async prune(session: SessionTabs): Promise<void> {
		let changed = false;
		for (const [logicalId, tabId] of Object.entries(session.tabs)) {
			const tab = await this.chrome.tabs.get(tabId).catch(() => undefined);
			if (!tab || (session.groupId !== undefined && tab.groupId !== session.groupId)) {
				delete session.tabs[logicalId];
				changed = true;
			}
		}
		if (changed) {
			if (session.activeTabId && session.tabs[session.activeTabId] === undefined) session.activeTabId = Object.keys(session.tabs)[0];
			await this.save();
		}
	}

	private async forgetTab(tabId: number): Promise<void> {
		await this.load();
		const owner = this.ownerOf(tabId);
		if (!owner) return;
		delete owner.session.tabs[owner.logicalId];
		if (owner.session.activeTabId === owner.logicalId) owner.session.activeTabId = Object.keys(owner.session.tabs)[0];
		await this.save();
	}

	private async groupChanged(tabId: number, groupId: number): Promise<void> {
		await this.load();
		const owner = this.ownerOf(tabId);
		if (owner && owner.session.groupId !== groupId) {
			// Dragged out of the AO group: the agent loses it.
			delete owner.session.tabs[owner.logicalId];
			if (owner.session.activeTabId === owner.logicalId) owner.session.activeTabId = Object.keys(owner.session.tabs)[0];
			await this.save();
			return;
		}
		if (!owner && groupId >= 0 && !this.creating.has(tabId)) {
			// Dragged into an AO group: the user handed the tab to the agent.
			const session = [...this.sessions.values()].find((candidate) => candidate.groupId === groupId);
			if (!session) return;
			session.tabs[`t${session.nextTab++}`] = tabId;
			await this.save();
		}
	}

	private async groupRemoved(groupId: number): Promise<void> {
		await this.load();
		for (const session of this.sessions.values()) {
			if (session.groupId === groupId) {
				session.groupId = undefined;
				session.tabs = {};
				session.activeTabId = undefined;
			}
		}
		await this.save();
	}
}
