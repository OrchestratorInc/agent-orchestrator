import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { dictationClient } from "../../lib/dictation-client";
import { resetDictationInstall } from "../../lib/dictation-install";
import { dictationUpgradeKey, useDictationUpgrade } from "./DictationUpgradeDialog";

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn(), explore: vi.fn(), toast: vi.fn() }));
vi.mock("../../lib/api-client", () => ({ apiClient: api }));
vi.mock("../../stores/ui-store", () => ({
	useUiStore: { getState: () => ({ openGlobalSettings: api.explore, showGlobalToast: api.toast }) },
}));
const native = { id: "native-macos", installed: true, available: true, downloadBytes: 0 };
const model = { id: "phonon-2.5", installed: false, available: true, downloadBytes: 156181652 };
const initial = {
	selected: "native-macos",
	installing: "",
	stage: "",
	error: "",
	recording: false,
	models: [native, model],
};
const installed = { ...initial, models: [native, { ...model, installed: true }] };
const start = vi.fn();
function Harness({ scope = "chat-a" }: { scope?: string }) {
	const upgrade = useDictationUpgrade(scope);
	return (
		<>
			<button onClick={() => void upgrade.requestStart(start)}>Dictate</button>
			{upgrade.dialog}
		</>
	);
}
const authorize = vi.spyOn(dictationClient, "authorize");
beforeEach(() => {
	vi.clearAllMocks();
	resetDictationInstall();
	authorize.mockResolvedValue({ granted: true });
	localStorage.clear();
	api.GET.mockResolvedValue({ data: initial });
	api.POST.mockResolvedValue({ data: initial });
});
afterEach(() => {
	cleanup();
	resetDictationInstall();
});

it("offers a neutral choice on first use, including profiles that dismissed the old promotion", async () => {
	localStorage.setItem("ao.dictation.models-upgrade.v1", "seen");
	render(<Harness />);
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	await userEvent.click(screen.getByText("Dictate"));
	expect(await screen.findByRole("dialog")).toHaveTextContent("Choose how you dictate");
	expect(screen.getByRole("button", { name: "Use macOS dictation" })).toBeEnabled();
	expect(screen.getByRole("button", { name: "Use recommended model" })).toBeEnabled();
	expect(screen.getByRole("dialog")).toHaveTextContent("156 MB");
	expect(start).not.toHaveBeenCalled();
});
it("selects macOS and starts immediately, remembering the choice", async () => {
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	await userEvent.click(await screen.findByRole("button", { name: "Use macOS dictation" }));
	await waitFor(() => expect(start).toHaveBeenCalledOnce());
	expect(api.POST).toHaveBeenCalledWith("/api/v1/dictation", { body: { action: "select", model: "native-macos" } });
	expect(localStorage.getItem(dictationUpgradeKey)).toBe("seen");
	await userEvent.click(screen.getByText("Dictate"));
	await waitFor(() => expect(start).toHaveBeenCalledTimes(2));
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});
it("settles the macOS permission prompts once, before selecting macOS dictation", async () => {
	const order: string[] = [];
	authorize.mockImplementation(async () => {
		order.push("authorize");
		return { granted: true };
	});
	api.POST.mockImplementation(async (_path: string, init: { body: { action: string } }) => {
		order.push(init.body.action);
		return { data: initial };
	});
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	await userEvent.click(await screen.findByRole("button", { name: "Use macOS dictation" }));
	await waitFor(() => expect(start).toHaveBeenCalledOnce());
	expect(order).toEqual(["authorize", "select"]);
	expect(authorize).toHaveBeenCalledOnce();
});
it("stops with the reason, and does not select or start, when macOS access is refused", async () => {
	authorize.mockResolvedValue({
		granted: false,
		message: "Allow Microphone access for Orchestrator in System Settings > Privacy & Security.",
	});
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	await userEvent.click(await screen.findByRole("button", { name: "Use macOS dictation" }));
	expect(await screen.findByRole("alert")).toHaveTextContent("Allow Microphone access for Orchestrator");
	expect(api.POST).not.toHaveBeenCalled();
	expect(start).not.toHaveBeenCalled();
	expect(localStorage.getItem(dictationUpgradeKey)).toBeNull();
	// The choices come back so access can be granted and tried again.
	expect(screen.getByRole("button", { name: "Use macOS dictation" })).toBeEnabled();
});
it("never asks for macOS access when a downloaded model is chosen", async () => {
	api.GET.mockResolvedValue({ data: { ...installed, selected: model.id } });
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	await userEvent.click(await screen.findByRole("button", { name: "Use recommended model" }));
	await waitFor(() => expect(start).toHaveBeenCalledOnce());
	expect(authorize).not.toHaveBeenCalled();
});
it("offers download and exploration but no native option on Windows/Linux", async () => {
	api.GET.mockResolvedValue({ data: { ...initial, selected: model.id, models: [model] } });
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	expect(await screen.findByRole("button", { name: "Use recommended model" })).toBeEnabled();
	expect(screen.queryByRole("button", { name: "Use macOS dictation" })).not.toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "See all available models" }));
	expect(api.explore).toHaveBeenCalledWith("dictation");
	expect(start).not.toHaveBeenCalled();
});
it("installs inside the chosen row, selects the model, then starts dictating without another step", async () => {
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	const busy = {
		...initial,
		installing: model.id,
		stage: "Downloading model",
		progressDone: 78_000_000,
		progressTotal: 156_000_000,
	};
	api.POST.mockResolvedValue({ data: busy });
	api.GET.mockResolvedValue({ data: busy });
	await userEvent.click(await screen.findByRole("button", { name: "Use recommended model" }));
	expect(await screen.findByText("50%")).toBeInTheDocument();
	expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "50");
	// One view: the other choices stay on screen but cannot be picked mid-download.
	expect(screen.getByRole("dialog")).toHaveTextContent("Choose how you dictate");
	expect(screen.getByRole("button", { name: "Use macOS dictation" })).toBeDisabled();
	expect(start).not.toHaveBeenCalled();
	api.GET.mockResolvedValue({ data: installed });
	api.POST.mockResolvedValue({ data: { ...installed, selected: model.id } });
	await waitFor(() => expect(start).toHaveBeenCalledOnce(), { timeout: 3000 });
	expect(api.POST).toHaveBeenLastCalledWith("/api/v1/dictation", { body: { action: "select", model: model.id } });
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	expect(localStorage.getItem(dictationUpgradeKey)).toBe("seen");
});
it("starts the bar at zero before the first measurement instead of sweeping", async () => {
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	const busy = { ...initial, installing: model.id, stage: "Installing speech engine" };
	api.POST.mockResolvedValue({ data: busy });
	api.GET.mockResolvedValue({ data: busy });
	await userEvent.click(await screen.findByRole("button", { name: "Use recommended model" }));
	expect(await screen.findByRole("status")).toHaveTextContent("0%");
	expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "0");
});
it("shows an install that is already running as that row's progress, without starting another", async () => {
	const running = { ...initial, installing: model.id, progressDone: 39_000_000, progressTotal: 156_000_000 };
	api.GET.mockResolvedValue({ data: running });
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	expect(await screen.findByText("25%")).toBeInTheDocument();
	expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "25");
	expect(api.POST).not.toHaveBeenCalled();
	expect(start).not.toHaveBeenCalled();
});
it("does not start dictation when the chooser is closed while a selection is still pending", async () => {
	api.GET.mockResolvedValue({ data: { ...installed, selected: model.id } });
	let reply!: (value: { data: typeof installed }) => void;
	api.POST.mockImplementation(
		() =>
			new Promise((resolve) => {
				reply = resolve;
			}),
	);
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	await userEvent.click(await screen.findByRole("button", { name: "Use recommended model" }));
	await userEvent.click(screen.getByRole("button", { name: "Close" }));
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	reply({ data: { ...installed, selected: model.id } });
	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(start).not.toHaveBeenCalled();
});
it("starts straight away with an already installed recommendation, without downloading", async () => {
	api.GET.mockResolvedValue({ data: { ...installed, selected: model.id } });
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	await screen.findByText(/Already installed/);
	await userEvent.click(screen.getByRole("button", { name: "Use recommended model" }));
	await waitFor(() => expect(start).toHaveBeenCalledOnce());
	expect(api.POST).toHaveBeenCalledExactlyOnceWith("/api/v1/dictation", {
		body: { action: "select", model: model.id },
	});
});
it("keeps failures retryable and does not remember an unsuccessful choice", async () => {
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	api.POST.mockResolvedValue({ error: { message: "Download failed" } });
	await userEvent.click(await screen.findByRole("button", { name: "Use recommended model" }));
	expect(await screen.findByRole("alert")).toHaveTextContent("Download failed");
	expect(screen.getByRole("button", { name: "Use recommended model" })).toBeEnabled();
	expect(localStorage.getItem(dictationUpgradeKey)).toBeNull();
	expect(start).not.toHaveBeenCalled();
});
it("cancels without switching or starting", async () => {
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	api.POST.mockResolvedValue({ data: { ...initial, installing: model.id } });
	await userEvent.click(await screen.findByRole("button", { name: "Use recommended model" }));
	await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));
	await screen.findByRole("button", { name: "Use recommended model" });
	expect(api.POST).toHaveBeenLastCalledWith("/api/v1/dictation", { body: { action: "cancel", model: "" } });
	expect(start).not.toHaveBeenCalled();
});
it("reopens setup when a previously chosen model is no longer installed", async () => {
	localStorage.setItem(dictationUpgradeKey, "seen");
	api.GET.mockResolvedValue({ data: { ...initial, selected: model.id, models: [model] } });
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	expect(await screen.findByRole("dialog")).toBeInTheDocument();
	expect(start).not.toHaveBeenCalled();
});
it("ignores delayed responses after moving to a different chat", async () => {
	let resolve!: (value: { data: typeof initial }) => void;
	api.GET.mockImplementation(
		() =>
			new Promise((done) => {
				resolve = done;
			}),
	);
	const view = render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	view.rerender(<Harness scope="chat-b" />);
	resolve({ data: initial });
	await waitFor(() => expect(api.GET).toHaveBeenCalledOnce());
	expect(start).not.toHaveBeenCalled();
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("does not select or start in another chat after a delayed install response", async () => {
	let resolve!: (value: { data: typeof installed }) => void;
	api.POST.mockImplementation(
		() =>
			new Promise((done) => {
				resolve = done;
			}),
	);
	const view = render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	await userEvent.click(await screen.findByRole("button", { name: "Use recommended model" }));
	view.rerender(<Harness scope="chat-b" />);
	resolve({ data: installed });
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	expect(api.POST).toHaveBeenCalledTimes(1);
	expect(start).not.toHaveBeenCalled();
	expect(localStorage.getItem(dictationUpgradeKey)).toBeNull();
});

it("lets the user close the chooser mid-download: it finishes in the background, selects the model and says so, without starting dictation", async () => {
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	const busy = { ...initial, installing: model.id, progressDone: 78_000_000, progressTotal: 156_000_000 };
	api.POST.mockResolvedValue({ data: busy });
	api.GET.mockResolvedValue({ data: busy });
	await userEvent.click(await screen.findByRole("button", { name: "Use recommended model" }));
	await screen.findByText("50%");
	await userEvent.click(screen.getByRole("button", { name: "Close" }));
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	api.GET.mockResolvedValue({ data: installed });
	api.POST.mockResolvedValue({ data: { ...installed, selected: model.id } });
	await waitFor(
		() =>
			expect(api.POST).toHaveBeenLastCalledWith("/api/v1/dictation", { body: { action: "select", model: model.id } }),
		{ timeout: 3000 },
	);
	await waitFor(() =>
		expect(api.toast).toHaveBeenCalledWith(
			"Dictation is ready",
			undefined,
			expect.objectContaining({ tone: "success" }),
		),
	);
	expect(start).not.toHaveBeenCalled();
	expect(localStorage.getItem(dictationUpgradeKey)).toBe("seen");
});
it("shows a download that continues after the chooser was closed when the mic is pressed again", async () => {
	const running = { ...initial, installing: model.id, progressDone: 39_000_000, progressTotal: 156_000_000 };
	api.GET.mockResolvedValue({ data: running });
	api.POST.mockResolvedValue({ data: running });
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	await screen.findByText("25%");
	await userEvent.click(screen.getByRole("button", { name: "Close" }));
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	await userEvent.click(screen.getByText("Dictate"));
	expect(await screen.findByText("25%")).toBeInTheDocument();
	expect(start).not.toHaveBeenCalled();
});
it("does not open the chooser for a download of a model it does not offer", async () => {
	localStorage.setItem(dictationUpgradeKey, "seen");
	api.GET.mockResolvedValue({
		data: { ...installed, installing: "handy-computer/whisper-tiny", selected: model.id },
	});
	render(<Harness />);
	await userEvent.click(screen.getByText("Dictate"));
	await new Promise((resolve) => setTimeout(resolve, 20));
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	expect(start).not.toHaveBeenCalled();
});
