import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test, vi } from "vitest";
import { SessionInstructionsEditor } from "./SessionInstructionsEditor";
import { appI18n } from "../i18n";

const api = vi.hoisted(() => ({ GET: vi.fn(), PATCH: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: api, apiErrorMessage: () => "保存失败" }));
vi.mock("./ui/dropdown-menu", () => ({ DropdownMenuItem: ({ children, onSelect }: { children: React.ReactNode; onSelect: (event: { preventDefault(): void }) => void }) => <button onClick={() => onSelect({ preventDefault() {} })}>{children}</button> }));

test("loads, saves, reports failure and clears the session instructions", async () => {
	api.GET.mockResolvedValue({ data: { text: "原规则" } });
	api.PATCH.mockResolvedValueOnce({ error: {} }).mockResolvedValue({ data: { text: "" } });
	render(<SessionInstructionsEditor sessionId="session-1" />);
	await userEvent.click(screen.getByRole("button", { name: appI18n.t("sessionInstructions.title") }));
	const editor = await screen.findByRole("textbox", { name: appI18n.t("sessionInstructions.label") });
	await waitFor(() => expect(editor).toHaveValue("原规则"));
	await userEvent.type(editor, "新规则");
	await userEvent.click(screen.getByRole("button", { name: appI18n.t("sessionInstructions.save") }));
	expect(await screen.findByRole("alert")).toHaveTextContent("保存失败");
	expect(editor).toHaveValue("原规则新规则");
	await userEvent.click(screen.getByRole("button", { name: appI18n.t("sessionInstructions.clear") }));
	await waitFor(() => expect(api.PATCH).toHaveBeenLastCalledWith(expect.any(String), { params: { path: { sessionId: "session-1" } }, body: { text: "" } }));
});
