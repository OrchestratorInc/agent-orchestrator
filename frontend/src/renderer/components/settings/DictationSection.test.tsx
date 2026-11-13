import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { DictationSection } from "./DictationSection";

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));
vi.mock("../../lib/api-client", () => ({ apiClient: api }));
const client = vi.hoisted(() => ({ start: vi.fn(async () => undefined), stop: vi.fn(async () => undefined) }));
vi.mock("../../lib/dictation-client", () => ({
	dictationClient: {
		onEvent: () => () => {},
		cancel: async () => {},
		authorize: async () => ({ granted: true }),
		start: client.start,
		stop: client.stop,
	},
}));
const initial = {
	selected: "native-macos",
	installing: "",
	stage: "",
	error: "",
	recording: false,
	models: [
		{ id: "native-macos", name: "macOS · on-device", installed: true, available: true, downloadBytes: 0 },
		{ id: "phonon-2.5", name: "Phonon 2.5", installed: false, available: true, downloadBytes: 156181652 },
	],
};
beforeEach(() => {
	vi.clearAllMocks();
	api.GET.mockResolvedValue({ data: initial });
});
afterEach(cleanup);
function show() {
	return render(
		<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
			<DictationSection />
		</QueryClientProvider>,
	);
}
it("downloads a model and shows cancellable installation progress", async () => {
	api.POST.mockResolvedValue({ data: { ...initial, installing: "phonon-2.5", stage: "Downloading and verifying" } });
	show();
	await userEvent.click(await screen.findByRole("button", { name: "Download" }));
	expect(api.POST).toHaveBeenCalledWith("/api/v1/dictation", { body: { action: "install", model: "phonon-2.5" } });
	expect(await screen.findByRole("status")).toHaveTextContent("Downloading and verifying");
	await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
	expect(api.POST).toHaveBeenLastCalledWith("/api/v1/dictation", { body: { action: "cancel", model: "" } });
});
it("switches to an installed model and prevents deleting the selected model", async () => {
	const installed = { ...initial, models: initial.models.map((model) => ({ ...model, installed: true })) };
	api.GET.mockResolvedValue({ data: installed });
	api.POST.mockResolvedValue({ data: { ...installed, selected: "phonon-2.5" } });
	show();
	await userEvent.click(await screen.findByRole("button", { name: "Use model" }));
	expect(api.POST).toHaveBeenCalledWith("/api/v1/dictation", { body: { action: "select", model: "phonon-2.5" } });
	await waitFor(() => expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled());
});
it("deletes an unselected downloaded model", async () => {
	api.GET.mockResolvedValue({
		data: { ...initial, models: initial.models.map((model) => ({ ...model, installed: true })) },
	});
	api.POST.mockResolvedValue({ data: initial });
	show();
	await userEvent.click(await screen.findByRole("button", { name: "Delete" }));
	expect(api.POST).toHaveBeenCalledWith("/api/v1/dictation", { body: { action: "delete", model: "phonon-2.5" } });
	expect(await screen.findByRole("button", { name: "Download" })).toBeEnabled();
});
it("shows failed installs and allows retry", async () => {
	api.GET.mockResolvedValue({ data: { ...initial, error: "Download failed checksum verification" } });
	show();
	expect(await screen.findByRole("alert")).toHaveTextContent("checksum verification");
	expect(screen.getByRole("button", { name: "Download" })).toBeEnabled();
});

it("filters the expanded catalog by model name and language", async () => {
	api.GET.mockResolvedValue({
		data: {
			...initial,
			models: [
				...initial.models,
				{
					id: "handy-computer/whisper-tiny.en-gguf",
					name: "Whisper Tiny English",
					description: "English",
					languages: ["en"],
					installed: false,
					available: true,
					downloadBytes: 45904544,
				},
				{
					id: "handy-computer/parakeet-tdt-0.6b-v3-gguf",
					name: "Parakeet v3",
					languages: ["de", "fr"],
					installed: false,
					available: true,
					downloadBytes: 600000000,
				},
			],
		},
	});
	show();
	await screen.findByText("Whisper Tiny English");
	const search = screen.getByRole("textbox", { name: "Search models or languages" });
	await userEvent.type(search, "whisper");
	expect(screen.getByText("Whisper Tiny English")).toBeInTheDocument();
	expect(screen.queryByText("Parakeet v3")).not.toBeInTheDocument();
	await userEvent.clear(search);
	await userEvent.type(search, "fr");
	expect(screen.getByText("Parakeet v3")).toBeInTheDocument();
	expect(screen.queryByText("Whisper Tiny English")).not.toBeInTheDocument();
});

it("tests the selected model directly, without reopening the first-use chooser", async () => {
	const installed = {
		...initial,
		selected: "phonon-2.5",
		models: initial.models.map((model) => ({ ...model, installed: true })),
	};
	api.GET.mockResolvedValue({ data: installed });
	show();
	const test = await screen.findByRole("button", { name: "Test microphone" });
	await waitFor(() => expect(test).toBeEnabled());
	await userEvent.click(test);
	await waitFor(() => expect(client.start).toHaveBeenCalledOnce());
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});
