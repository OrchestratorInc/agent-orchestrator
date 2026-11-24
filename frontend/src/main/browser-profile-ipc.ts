import type { IpcMain, IpcMainInvokeEvent, WebContents } from "electron";
import {
	normalizeBrowserProfileId,
	type BrowserMenuBounds,
	type BrowserProfile,
	type BrowserProfileListState,
	type BrowserProfileMenuInput,
	type BrowserProfileSwitchConfirmRequest,
	type BrowserProfileSwitchFailure,
	type BrowserProfileViewState,
	type BrowserProfileUsage,
	type BrowserProfileUsageResult,
	type BrowserProfileDeleteResult,
} from "../shared/browser-profiles";
import type { BrowserImportRequest } from "../shared/browser-profile-import";
import type { BrowserProfileImportService } from "./browser-profile-import";
import { BrowserProfileStore, BrowserProfileStoreError as StoreError } from "./browser-profile-store";

export type BrowserProfileMenuItem = {
	label?: string;
	type?: "separator" | "radio";
	checked?: boolean;
	enabled?: boolean;
	click?: () => void;
};

export type BrowserProfileMenu = {
	popup: (options: { window: unknown; x: number; y: number }) => void;
};

export type BrowserProfileIpcHost = {
	isRendererOwned: (event: IpcMainInvokeEvent, viewId: string) => boolean;
	getProfileState: (viewId: string) => BrowserProfileViewState | null;
	refreshProfileState?: (profileId: string) => void;
	getProfileSwitchInfo: (viewId: string) => { hasNavigated: boolean; agentActive: boolean } | null;
	switchProfile: (viewId: string, profileId: string | null) => Promise<BrowserProfileViewState>;
	getProfileUsage: (profileId: string) => BrowserProfileUsage[];
	detachProfile: (profileId: string) => Promise<void>;
	isProfileLive: (profileId: string) => boolean;
	clearProfileData: (profileId: string) => Promise<void>;
};

export type BrowserProfileIpcOptions = {
	ipcMain: Pick<IpcMain, "handle" | "removeHandler">;
	shellWebContents: WebContents;
	mainWindow: unknown;
	store: BrowserProfileStore;
	host: BrowserProfileIpcHost;
	importer: BrowserProfileImportService;
	buildMenu: (items: BrowserProfileMenuItem[]) => BrowserProfileMenu;
};

/**
 * The renderer that asked for a switch. Confirmation and failure are shown in
 * the app by that renderer, never as native dialogs.
 */
type BrowserProfileSwitchNotifier = Pick<WebContents, "send"> & Partial<Pick<WebContents, "isDestroyed">>;

function notifySwitch(notify: BrowserProfileSwitchNotifier, channel: string, payload: unknown): void {
	// A native-menu click can land after its renderer reloaded or closed.
	if (notify.isDestroyed?.()) return;
	notify.send(channel, payload);
}

export type BrowserProfileIpc = {
	dispose: () => void;
};

const EMPTY_PROFILE_STATE: BrowserProfileViewState = {
	viewId: "",
	profileId: null,
	temporary: true,
};

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function invalid(message: string): StoreError {
	return new StoreError("INVALID_ARGUMENT", message);
}

function profileIdFromInput(value: unknown): string {
	const id = normalizeBrowserProfileId(value);
	if (!id) throw invalid("Profile ID is invalid.");
	return id;
}

function viewIdFromInput(value: unknown): string {
	if (typeof value !== "string" || value.length === 0 || value.length > 300 || /[\u0000-\u001f\u007f]/u.test(value)) {
		throw invalid("Browser view ID is invalid.");
	}
	return value;
}

function menuBoundsFromInput(value: unknown): BrowserMenuBounds {
	if (!isRecord(value)) throw invalid("Browser profile menu bounds are invalid.");
	const x = value.x;
	const y = value.y;
	const width = value.width;
	const height = value.height;
	if (
		typeof x !== "number" ||
		typeof y !== "number" ||
		typeof width !== "number" ||
		typeof height !== "number" ||
		!Number.isFinite(x) ||
		!Number.isFinite(y) ||
		!Number.isFinite(width) ||
		!Number.isFinite(height) ||
		x < 0 ||
		y < 0 ||
		x > 100_000 ||
		y > 100_000 ||
		width <= 0 ||
		height <= 0 ||
		width > 2_000 ||
		height > 2_000
	) {
		throw invalid("Browser profile menu bounds are invalid.");
	}
	return { x, y, width, height };
}

function labelsFromInput(value: unknown): BrowserProfileMenuInput["labels"] {
	if (!isRecord(value)) throw invalid("Browser profile menu labels are invalid.");
	const keys = ["temporary", "manage"] as const;
	const labels = {} as BrowserProfileMenuInput["labels"];
	for (const key of keys) {
		const label = value[key];
		if (typeof label !== "string" || label.length === 0 || label.length > 300 || /[\u0000-\u001f\u007f]/u.test(label)) {
			throw invalid("Browser profile menu labels are invalid.");
		}
		labels[key] = label;
	}
	return labels;
}

function trustedShellSender(event: IpcMainInvokeEvent, shellWebContents: WebContents): boolean {
	return event.sender === shellWebContents;
}

function listStateWithStoreError(store: BrowserProfileStore): BrowserProfileListState {
	return {
		profiles: store.profiles,
		defaultProfileId: store.getDefaultProfileId(),
		...(store.error ? { error: store.error } : {}),
	};
}

function profileLabel(profile: BrowserProfile): string {
	return profile.name;
}

export function registerBrowserProfileIpc(options: BrowserProfileIpcOptions): BrowserProfileIpc {
	const disposers: Array<() => void> = [];
	const handle = <Args extends unknown[], Result>(
		channel: string,
		fn: (event: IpcMainInvokeEvent, ...args: Args) => Result,
	): void => {
		options.ipcMain.handle(channel, fn);
		disposers.push(() => options.ipcMain.removeHandler(channel));
	};

	handle("browserProfiles:list", async (event) => {
		if (!trustedShellSender(event, options.shellWebContents))
			return { profiles: [], defaultProfileId: null } satisfies BrowserProfileListState;
		await options.store.load();
		return listStateWithStoreError(options.store);
	});
	handle("browserProfiles:create", async (event, input: unknown) => {
		if (!trustedShellSender(event, options.shellWebContents)) throw invalid("Untrusted browser profile sender.");
		if (!isRecord(input)) throw invalid("Browser profile input is invalid.");
		return options.store.createProfile(input.name);
	});
	handle("browserProfiles:rename", async (event, input: unknown) => {
		if (!trustedShellSender(event, options.shellWebContents)) throw invalid("Untrusted browser profile sender.");
		if (!isRecord(input)) throw invalid("Browser profile input is invalid.");
		const profile = await options.store.renameProfile(profileIdFromInput(input.id), input.name);
		options.host.refreshProfileState?.(profile.id);
		return profile;
	});
	handle("browserProfiles:clear", async (event, input: unknown) => {
		if (!trustedShellSender(event, options.shellWebContents)) throw invalid("Untrusted browser profile sender.");
		const profileId = profileIdFromInput(isRecord(input) ? input.id : undefined);
		return options.store.runProfileOperation(profileId, () => options.host.isProfileLive(profileId), () => options.host.clearProfileData(profileId));
	});
	handle("browserProfiles:usage", async (event, input: unknown): Promise<BrowserProfileUsageResult> => {
		if (!trustedShellSender(event, options.shellWebContents)) return { sessions: [], error: "Browser profile access was denied." };
		try {
			const profileId = profileIdFromInput(isRecord(input) ? input.id : undefined);
			await options.store.load();
			if (!options.store.getProfile(profileId)) return { sessions: [], error: "This browser profile no longer exists." };
			return { sessions: options.host.getProfileUsage(profileId) };
		} catch {
			return { sessions: [], error: "Could not check browsers using this profile. Please try again." };
		}
	});
	handle("browserProfiles:delete", async (event, input: unknown): Promise<BrowserProfileDeleteResult> => {
		if (!trustedShellSender(event, options.shellWebContents)) return { error: "Browser profile access was denied." };
		try {
			const profileId = profileIdFromInput(isRecord(input) ? input.id : undefined);
			const confirmed = isRecord(input) ? input.confirmedViewIds : undefined;
			if (!Array.isArray(confirmed) || !confirmed.every((id) => typeof id === "string")) {
				return { error: "Review the affected sessions before deleting this profile." };
			}
			// Reserve against new selections before switching existing browsers.
			await options.store.runProfileOperation(profileId, () => false, async () => {
				const usage = options.host.getProfileUsage(profileId);
				if (usage.some((session) => !confirmed.includes(session.viewId))) {
					throw invalid("The affected sessions changed. Review the updated list and confirm again.");
				}
				await options.host.detachProfile(profileId);
				if (options.host.isProfileLive(profileId)) {
					throw invalid("A browser is still using this profile. Please try again.");
				}
				await options.host.clearProfileData(profileId);
				await options.store.deleteProfile(profileId);
			});
			return {};
		} catch (error) {
			// Only intentional domain messages cross IPC; storage/runtime errors may
			// include paths or Electron details and get a safe, actionable fallback.
			const code = error && typeof error === "object" && "code" in error ? error.code : undefined;
			const readable = ["INVALID_ARGUMENT", "BROWSER_PROFILE_ACTIVE", "BROWSER_PROFILE_SWITCH_FAILED", "BROWSER_PROFILE_NOT_FOUND"].includes(String(code));
			return { error: readable && error instanceof Error ? error.message :
				"Could not finish deleting the profile. Some browsers may already be temporary. Please check the session list and try again." };
		}
	});
	handle("browserProfiles:setDefault", async (event, input: unknown) => {
		if (!trustedShellSender(event, options.shellWebContents)) throw invalid("Untrusted browser profile sender.");
		if (!isRecord(input)) throw invalid("Browser profile input is invalid.");
		const profileId = input.id === null ? null : profileIdFromInput(input.id);
		await options.store.setDefaultProfileId(profileId);
		return undefined;
	});
	handle("browserProfiles:import:discover", async (event, input: unknown) => {
		if (!trustedShellSender(event, options.shellWebContents)) return { sources: [] };
		return options.importer.discover(isRecord(input) && typeof input.sourceId === "string" ? { sourceId: input.sourceId } : undefined);
	});
	handle("browserProfiles:import:start", async (event, input: unknown) => {
		if (!trustedShellSender(event, options.shellWebContents)) throw invalid("Untrusted browser profile sender.");
		return options.importer.import(input as BrowserImportRequest, (progress) => {
			options.shellWebContents.send("browserProfiles:import:progress", progress);
		});
	});
	handle("browser:profile:get", (event, rawViewId: unknown) => {
		const viewId = viewIdFromInput(rawViewId);
		if (!options.host.isRendererOwned(event, viewId)) return { ...EMPTY_PROFILE_STATE };
		return options.host.getProfileState(viewId) ?? { ...EMPTY_PROFILE_STATE, viewId };
	});
	handle("browser:profile:select", async (event, input: unknown) => {
		if (!isRecord(input)) throw invalid("Browser profile selection is invalid.");
		const viewId = viewIdFromInput(input.viewId);
		if (!options.host.isRendererOwned(event, viewId)) return undefined;
		const profileId = input.profileId === null ? null : profileIdFromInput(input.profileId);
		await selectProfile(viewId, profileId, input.confirmed === true, event.sender, options);
		return undefined;
	});
	handle("browser:profile:menu", (event, input: unknown) => {
		if (!isRecord(input)) throw invalid("Browser profile menu input is invalid.");
		const viewId = viewIdFromInput(input.viewId);
		if (!options.host.isRendererOwned(event, viewId)) return undefined;
		const bounds = menuBoundsFromInput(input.bounds);
		const labels = labelsFromInput(input.labels);
		const current = options.host.getProfileState(viewId);
		if (!current) return undefined;
		const switchInfo = options.host.getProfileSwitchInfo(viewId);
		const switchBlocked = !switchInfo || switchInfo.agentActive;
		const profiles = options.store.profiles;
		const items: BrowserProfileMenuItem[] = [
			{
				label: labels.temporary,
				type: "radio",
				checked: current.profileId === null,
				enabled: !switchBlocked,
				click: () => void selectProfile(viewId, null, false, event.sender, options),
			},
			...profiles.map((profile) => ({
				label: profileLabel(profile),
				type: "radio" as const,
				checked: current.profileId === profile.id,
				enabled: !switchBlocked && !options.store.isProfileOperationInProgress(profile.id),
				click: () => void selectProfile(viewId, profile.id, false, event.sender, options),
			})),
			{ type: "separator" },
			{
				label: labels.manage,
				click: () => options.shellWebContents.send("browser:profileManage", { viewId }),
			},
		];
		const rawZoomFactor = (event.sender as Partial<Pick<WebContents, "getZoomFactor">>).getZoomFactor?.() ?? 1;
		const zoomFactor = Number.isFinite(rawZoomFactor) && rawZoomFactor > 0 ? rawZoomFactor : 1;
		options.buildMenu(items).popup({
			window: options.mainWindow,
			x: Math.round(bounds.x * zoomFactor),
			y: Math.round((bounds.y + bounds.height) * zoomFactor),
		});
		return undefined;
	});

	return {
		dispose: () => disposers.splice(0).forEach((dispose) => dispose()),
	};
}

async function selectProfile(
	viewId: string,
	profileId: string | null,
	confirmed: boolean,
	notify: BrowserProfileSwitchNotifier,
	options: BrowserProfileIpcOptions,
): Promise<void> {
	const current = options.host.getProfileState(viewId);
	if (!current || current.profileId === profileId) return;
	const info = options.host.getProfileSwitchInfo(viewId);
	if (!info) return;
	if (info.agentActive) {
		// The menus disable switching while an agent drives the browser, so this
		// only happens when the agent started after the user confirmed. Say why
		// the profile did not change instead of closing the prompt silently.
		if (confirmed) {
			notifySwitch(notify, "browser:profileSwitchFailed", {
				viewId,
				message: "An agent is using this browser.",
				reason: "agent-active",
			} satisfies BrowserProfileSwitchFailure);
		}
		return;
	}
	if (info.hasNavigated && !confirmed) {
		// The shell confirms in its own dialog and selects again with `confirmed`;
		// the checks above run again then, against the state at that moment.
		notifySwitch(notify, "browser:profileSwitchConfirm", { viewId, profileId } satisfies BrowserProfileSwitchConfirmRequest);
		return;
	}
	try {
		await options.host.switchProfile(viewId, profileId);
	} catch (error) {
		console.error("browser profile switch failed:", error);
		const message = error instanceof Error ? error.message : "Browser profile could not be switched.";
		notifySwitch(notify, "browser:profileSwitchFailed", { viewId, message } satisfies BrowserProfileSwitchFailure);
	}
}
