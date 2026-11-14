import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
	cancelDictationInstall,
	DictationInstallCancelled,
	installDictationModel,
	publishDictationStatus,
	resetDictationInstall,
	useDictationInstall,
	type DictationStatus,
} from "./dictation-install";

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));
vi.mock("./api-client", () => ({ apiClient: api }));

const texts = { loadFailed: "load failed", updateFailed: "update failed" };
const idle: DictationStatus = {
	selected: "native-macos",
	installing: "",
	stage: "",
	error: "",
	recording: false,
	progressDone: 0,
	progressTotal: 0,
	models: [{ id: "phonon-2.5", name: "Phonon 2.5", installed: false, available: true, downloadBytes: 1 }],
};
const busy = (done: number): DictationStatus => ({
	...idle,
	installing: "phonon-2.5",
	progressDone: done,
	progressTotal: 1000,
});
const installed: DictationStatus = { ...idle, models: [{ ...idle.models[0], installed: true }] };

beforeEach(() => {
	vi.clearAllMocks();
	resetDictationInstall();
	api.GET.mockResolvedValue({ data: installed });
	api.POST.mockResolvedValue({ data: { ...installed, selected: "phonon-2.5" } });
});
afterEach(resetDictationInstall);

it("reports the model being downloaded and how far along it is, and clears when it finishes", () => {
	const { result } = renderHook(() => useDictationInstall());
	expect(result.current).toEqual({ model: "", fraction: 0 });
	act(() => publishDictationStatus(busy(250)));
	expect(result.current).toEqual({ model: "phonon-2.5", fraction: 0.25 });
	act(() => publishDictationStatus(idle));
	expect(result.current).toEqual({ model: "", fraction: 0 });
});

it("follows a download to its end, selects the model once, and lets a second request join it", async () => {
	const first = installDictationModel("phonon-2.5", busy(100), texts);
	const second = installDictationModel("phonon-2.5", busy(100), texts);
	await Promise.all([first, second]);
	const selects = api.POST.mock.calls.filter(([, init]) => init.body.action === "select");
	expect(selects).toHaveLength(1);
	expect(selects[0][1]).toEqual({ body: { action: "select", model: "phonon-2.5" } });
	expect(localStorage.getItem("ao.dictation.chooser.v2")).toBe("seen");
});

it("fails with the daemon's reason when the download ends without the model", async () => {
	api.GET.mockResolvedValue({ data: { ...idle, error: "Download failed" } });
	await expect(installDictationModel("phonon-2.5", busy(100), texts)).rejects.toThrow("Download failed");
	expect(api.POST).not.toHaveBeenCalled();
});

it("tells a waiting caller it was cancelled, not that it failed", async () => {
	api.POST.mockResolvedValue({ data: idle });
	const waiting = installDictationModel("phonon-2.5", busy(100), texts);
	const rejection = expect(waiting).rejects.toBeInstanceOf(DictationInstallCancelled);
	await cancelDictationInstall();
	await rejection;
	await waitFor(() => expect(api.POST).toHaveBeenCalledTimes(1));
});
