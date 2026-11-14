import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AoBridge } from "../../../preload";
import { BrowserPasswordsSection } from "./BrowserPasswordsSection";

const item = {
	id: "item-1",
	label: "Demo login",
	origins: ["http://localhost:4173"],
	username: "alice",
	hasPassword: true,
	hasTotp: true,
	allowedProjects: [],
	createdAt: "2026-11-01T00:00:00.000Z",
	updatedAt: "2026-11-01T00:00:00.000Z",
};

function mockBridge(overrides: Partial<AoBridge["vault"]> = {}): AoBridge["vault"] {
	return {
		list: vi.fn(async () => ({ available: true, items: [item] })),
		save: vi.fn(async () => item),
		delete: vi.fn(async () => undefined),
		audit: vi.fn(async () => [
			{ time: "2026-11-15T10:00:00.000Z", origin: "http://localhost:4173", sessionId: "ao-7", itemId: "item-1", itemLabel: "Demo login", field: "password" as const, outcome: "filled" as const },
			{ time: "2026-11-15T09:00:00.000Z", origin: "https://evil.test", sessionId: "ao-7", itemId: "item-1", itemLabel: "Demo login", field: "password" as const, outcome: "denied" as const, reason: "origin mismatch" },
		]),
		...overrides,
	};
}

describe("BrowserPasswordsSection", () => {
	const original = window.ao!.vault;
	afterEach(() => {
		window.ao!.vault = original;
	});

	it("lists saved passwords without secrets and shows sign-in activity", async () => {
		window.ao!.vault = mockBridge();
		render(<BrowserPasswordsSection />);
		const row = await screen.findByTestId("vault-item");
		expect(row).toHaveTextContent("Demo login");
		expect(row).toHaveTextContent("alice · http://localhost:4173 · 2FA code · All projects");
		const audit = screen.getAllByTestId("vault-audit");
		expect(audit[0]).toHaveTextContent("Demo login · password");
		expect(audit[0]).toHaveTextContent("Filled");
		expect(audit[1]).toHaveTextContent("Blocked");
		expect(audit[1]).toHaveTextContent("origin mismatch");
	});

	it("adds a password through the dialog", async () => {
		const bridge = mockBridge({ list: vi.fn(async () => ({ available: true, items: [] })) });
		window.ao!.vault = bridge;
		render(<BrowserPasswordsSection />);
		await userEvent.click(await screen.findByRole("button", { name: "Add password" }));
		const dialog = await screen.findByRole("dialog");
		const save = within(dialog).getByRole("button", { name: "Save" });
		expect(save).toBeDisabled();
		await userEvent.type(within(dialog).getByRole("textbox", { name: /Sites/ }), "localhost:4173");
		await userEvent.type(within(dialog).getByRole("textbox", { name: "Username" }), "alice");
		await userEvent.type(within(dialog).getByLabelText("Password"), "s3cret-pass");
		await userEvent.type(within(dialog).getByLabelText("2FA setup key"), "JBSWY3DPEHPK3PXP");
		await userEvent.click(save);
		await waitFor(() =>
			expect(bridge.save).toHaveBeenCalledWith({
				label: "",
				origins: ["localhost:4173"],
				username: "alice",
				password: "s3cret-pass",
				totpSeed: "JBSWY3DPEHPK3PXP",
				allowedProjects: [],
			}),
		);
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	});

	it("edits without resending the password and can remove the 2FA key", async () => {
		const bridge = mockBridge();
		window.ao!.vault = bridge;
		render(<BrowserPasswordsSection />);
		await userEvent.click(await screen.findByRole("button", { name: "Edit Demo login" }));
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByLabelText("Password")).toHaveValue("");
		await userEvent.click(within(dialog).getByRole("checkbox", { name: "Remove 2FA key" }));
		await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
		await waitFor(() =>
			expect(bridge.save).toHaveBeenCalledWith(expect.objectContaining({ id: "item-1", password: "", totpSeed: null })),
		);
	});

	it("shows the save error next to the form", async () => {
		window.ao!.vault = mockBridge({
			save: vi.fn(async () => {
				throw new Error("Error invoking remote method 'vault:save': Error: Only http and https sites can hold credentials");
			}),
		});
		render(<BrowserPasswordsSection />);
		await userEvent.click(await screen.findByRole("button", { name: "Edit Demo login" }));
		const dialog = await screen.findByRole("dialog");
		await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));
		expect(await within(dialog).findByRole("alert")).toHaveTextContent(/^Only http and https sites can hold credentials$/);
	});

	it("confirms before deleting", async () => {
		const bridge = mockBridge();
		window.ao!.vault = bridge;
		render(<BrowserPasswordsSection />);
		await userEvent.click(await screen.findByRole("button", { name: "Delete Demo login" }));
		expect(bridge.delete).not.toHaveBeenCalled();
		await userEvent.click(await screen.findByRole("button", { name: "Delete" }));
		await waitFor(() => expect(bridge.delete).toHaveBeenCalledWith("item-1"));
	});

	it("disables adding when the keychain is unavailable", async () => {
		window.ao!.vault = mockBridge({ list: vi.fn(async () => ({ available: false, items: [] })) });
		render(<BrowserPasswordsSection />);
		expect(await screen.findByText(/system keychain is unavailable/)).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Add password" })).toBeDisabled();
	});
});
