import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { InviteField, splitEmails } from "./InviteField";

function renderField(onSubmit = vi.fn(async () => true)) {
	render(<InviteField onSubmit={onSubmit} />);
	return { onSubmit, input: screen.getByRole<HTMLInputElement>("textbox", { name: "Add people by email" }) };
}

describe("InviteField", () => {
	it("shares with a typed address on Enter, with no Add button, and clears once taken", async () => {
		const { input, onSubmit } = renderField();
		expect(screen.queryByRole("button")).toBeNull();
		await userEvent.type(input, "maya@acme.co{Enter}");
		expect(onSubmit).toHaveBeenCalledExactlyOnceWith(["maya@acme.co"]);
		expect(input.value).toBe("");
	});

	it("shares with every address of a pasted list once, without duplicates", async () => {
		const { input, onSubmit } = renderField();
		await userEvent.click(input);
		await userEvent.paste("maya@acme.co, dev@acme.co; MAYA@acme.co ravi@acme.co");
		await userEvent.keyboard("{Enter}");
		expect(onSubmit).toHaveBeenCalledExactlyOnceWith(["maya@acme.co", "dev@acme.co", "ravi@acme.co"]);
		expect(splitEmails(" a@b.co,,c@d.co  ")).toEqual(["a@b.co", "c@d.co"]);
	});

	it("shares on a comma too", async () => {
		const { input, onSubmit } = renderField();
		await userEvent.type(input, "maya@acme.co,");
		expect(onSubmit).toHaveBeenCalledExactlyOnceWith(["maya@acme.co"]);
	});

	it("names an address that isn't one, and keeps it typed to fix", async () => {
		const { input, onSubmit } = renderField();
		await userEvent.type(input, "maya@acme{Enter}");
		expect(onSubmit).not.toHaveBeenCalled();
		expect(screen.getByRole("alert").textContent).toContain("maya@acme");
		expect(input.value).toBe("maya@acme");
	});

	it("keeps the address when the share is not taken", async () => {
		const { input } = renderField(vi.fn(async () => false));
		await userEvent.type(input, "maya@acme.co{Enter}");
		expect(input.value).toBe("maya@acme.co");
	});

	it("never shares by leaving the field", async () => {
		const { input, onSubmit } = renderField();
		await userEvent.type(input, "maya@acme.co");
		await userEvent.tab();
		expect(onSubmit).not.toHaveBeenCalled();
	});
});
