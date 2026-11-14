import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { InviteField, splitEmails } from "./InviteField";

function renderField(onSubmit = vi.fn(async () => true)) {
	render(<InviteField actionLabel="Invite" onSubmit={onSubmit} />);
	return { onSubmit, input: screen.getByRole("textbox", { name: "Add people by email" }) };
}

describe("InviteField", () => {
	it("splits a pasted list into chips, without duplicates", async () => {
		const { input } = renderField();
		await userEvent.click(input);
		await userEvent.paste("maya@acme.co, dev@acme.co; MAYA@acme.co\nravi@acme.co");
		expect(screen.getAllByText(/@acme\.co$/).map((chip) => chip.textContent)).toEqual(["maya@acme.co", "dev@acme.co", "ravi@acme.co"]);
		expect(splitEmails(" a@b.co,,c@d.co  ")).toEqual(["a@b.co", "c@d.co"]);
	});

	it("submits every address, including one still typed, and clears once taken", async () => {
		const { input, onSubmit } = renderField();
		await userEvent.type(input, "maya@acme.co dev@acme.co");
		await userEvent.click(screen.getByRole("button", { name: "Invite" }));
		expect(onSubmit).toHaveBeenCalledExactlyOnceWith(["maya@acme.co", "dev@acme.co"]);
		expect(screen.queryByText("maya@acme.co")).toBeNull();
	});

	it("keeps an address added while the invite was on its way", async () => {
		let finish: (taken: boolean) => void = () => undefined;
		const { input } = renderField(vi.fn(() => new Promise<boolean>((resolve) => { finish = resolve; })));
		await userEvent.type(input, "maya@acme.co{Enter}");
		await userEvent.click(screen.getByRole("button", { name: "Invite" }));
		await userEvent.type(input, "dev@acme.co{Enter}");
		await act(async () => finish(true));
		expect(screen.queryByText("maya@acme.co")).toBeNull();
		expect(screen.getByText("dev@acme.co")).toBeTruthy();
	});

	it("keeps the chips when the invite is not taken", async () => {
		const { input } = renderField(vi.fn(async () => false));
		await userEvent.type(input, "maya@acme.co{Enter}");
		await userEvent.click(screen.getByRole("button", { name: "Invite" }));
		expect(screen.getByText("maya@acme.co")).toBeTruthy();
	});

	it("removes the last chip with Backspace in an empty field", async () => {
		const { input } = renderField();
		await userEvent.type(input, "maya@acme.co,dev@acme.co,{Backspace}");
		expect(screen.getByText("maya@acme.co")).toBeTruthy();
		expect(screen.queryByText("dev@acme.co")).toBeNull();
	});

	it("is disabled with nothing to invite", () => {
		renderField();
		expect(screen.getByRole("button", { name: "Invite" })).toBeDisabled();
	});
});
