import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { ProjectEnvironmentSettings } from "./ProjectEnvironmentSettings";

const { getMock, putMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock, PUT: putMock }, apiErrorMessage: () => "request failed" }));

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { status: "ok", project: { id: "p", name: "Example", config: { env: { EXISTING: "old" }, autoReview: true } } } });
	putMock.mockReset().mockResolvedValue({ data: { status: "ok" } });
});

it("saves validated variables without dropping other project settings", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><ProjectEnvironmentSettings projectId="p" /></QueryClientProvider>);
	const value = await screen.findByLabelText("Value 1");
	expect(value).toHaveAttribute("type", "password");
	await userEvent.clear(value);
	await userEvent.type(value, "new");
	await userEvent.click(screen.getByRole("button", { name: "Add variable" }));
	await userEvent.type(screen.getByLabelText("Name 2"), "SECOND");
	await userEvent.type(screen.getByLabelText("Value 2"), "another");
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
	expect(putMock.mock.calls[0][1].body).toEqual({ displayName: "Example", config: { env: { EXISTING: "new", SECOND: "another" }, autoReview: true } });
});
