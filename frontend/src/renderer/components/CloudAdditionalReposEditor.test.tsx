import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import type { CloudCpGitHubAppRepository } from "../lib/cloud-cp";
import { CloudAdditionalReposEditor } from "./CloudAdditionalReposEditor";

const repository = (name: string): CloudCpGitHubAppRepository => ({
	githubRepositoryId: name, name, fullName: `acme/${name}`, htmlUrl: `https://github.com/acme/${name}`,
	defaultBranch: "main", visibility: "private", isPrivate: true, isArchived: false, access: "write", grantedAt: "",
} as CloudCpGitHubAppRepository);

function Editor({ available, loading }: { available: CloudCpGitHubAppRepository[]; loading: boolean }) {
	const [value, setValue] = useState<string[]>([]);
	return <CloudAdditionalReposEditor available={available} mainUrl="https://github.com/acme/alice" value={value} onChange={setValue} loading={loading} />;
}

afterEach(() => vi.useRealTimers());

it("opens a newly added row while repositories load, saying they are loading rather than that nothing matches", async () => {
	const view = render(<Editor available={[]} loading />);
	await userEvent.click(screen.getByRole("button", { name: "Add a repository" }));
	const list = await screen.findByRole("listbox", { name: "Additional repository 1" });
	expect(list).toHaveTextContent("Loading repositories...");
	expect(list).not.toHaveTextContent("No matches");

	view.rerender(<Editor available={[repository("alice"), repository("11x")]} loading={false} />);
	expect(await screen.findByRole("option", { name: "acme/11x" })).toBeInTheDocument();
	expect(screen.getByRole("listbox", { name: "Additional repository 1" })).not.toHaveTextContent("Loading repositories...");
});

it("still says nothing matches once loaded with no repository to offer", async () => {
	render(<Editor available={[repository("alice")]} loading={false} />);
	await userEvent.click(screen.getByRole("button", { name: "Add a repository" }));
	expect(await screen.findByRole("listbox", { name: "Additional repository 1" })).toHaveTextContent("No matches");
});

it("drops its pending focus move when it unmounts", async () => {
	const view = render(<Editor available={[repository("11x")]} loading={false} />);
	await userEvent.click(screen.getByRole("button", { name: "Add a repository" }));
	await screen.findByRole("listbox", { name: "Additional repository 1" });
	const setTimer = vi.spyOn(globalThis, "setTimeout");
	const clearTimer = vi.spyOn(globalThis, "clearTimeout");
	// Escape closes the list without a choice: the blank row goes, and focus is
	// moved back to the add button on the next tick.
	act(() => { fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" }); });
	expect(screen.queryByRole("combobox", { name: "Additional repository 1" })).toBeNull();
	const focusMoves = setTimer.mock.results
		.filter((_, index) => setTimer.mock.calls[index]?.[1] === 0)
		.map((result) => result.value as unknown);
	expect(focusMoves.length).toBeGreaterThan(0);
	view.unmount();
	const cleared = clearTimer.mock.calls.map(([id]) => id as unknown);
	expect(focusMoves.some((id) => cleared.includes(id))).toBe(true);
});
