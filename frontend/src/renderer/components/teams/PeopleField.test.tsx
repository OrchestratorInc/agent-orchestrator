import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpShareCandidate } from "../../lib/cloud-cp";
import { PeopleField, type Recipient } from "./PeopleField";

const mocks = vi.hoisted(() => ({ candidates: [] as CloudCpShareCandidate[], queries: [] as string[] }));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		ready: true,
		baseUrl: "https://cp.test",
		userId: "me",
		client: {
			listShareCandidates: async (_org: string, query: string) => {
				mocks.queries.push(query);
				const q = query.toLowerCase();
				return { candidates: mocks.candidates.filter((c) => !q || c.name.toLowerCase().includes(q) || c.email?.includes(q)) };
			},
		},
	}),
}));

vi.mock("../../lib/cloud-session", () => ({
	useCloudSession: () => ({ session: { authProvider: "workos", user: { id: "u-me", email: "Me@Acme.co", displayName: "Me" } }, status: "authenticated" }),
}));

function Harness({ onNewTeam, onSubmit, unavailable, workspaceHasAccess }: { onNewTeam?: (people: Recipient[], typed: string) => void; onSubmit?: () => void; unavailable?: Set<string>; workspaceHasAccess?: boolean }) {
	const [value, setValue] = useState<Recipient[]>([]);
	return (
		<>
			<PeopleField orgId="org" value={value} onChange={setValue} placeholder="Add people or teams" ariaLabel="Add people or teams" onNewTeam={onNewTeam} onSubmit={onSubmit} unavailable={unavailable} workspaceHasAccess={workspaceHasAccess} />
			<output data-testid="value">{JSON.stringify(value)}</output>
		</>
	);
}

function renderField(props: Parameters<typeof Harness>[0] = {}) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><Harness {...props} /></QueryClientProvider>);
}

const value = () => JSON.parse(screen.getByTestId("value").textContent ?? "[]") as Recipient[];

async function type(input: HTMLElement, text: string) {
	fireEvent.change(input, { target: { value: text } });
	await act(async () => {
		await new Promise((resolve) => setTimeout(resolve, 160));
	});
}

describe("PeopleField", () => {
	beforeEach(() => {
		mocks.queries = [];
		mocks.candidates = [
			{ kind: "team", teamId: "t-platform", name: "Platform", memberCount: 6, memberNames: ["Ben", "Chen", "Dana"] },
			{ kind: "team", teamId: "t-data", name: "Data infra", memberCount: 4 },
			{ kind: "user", name: "Dana Kim", email: "dana@northwind.dev" },
			{ kind: "user", name: "Riya Sen", email: "riya@acme.co" },
		];
	});

	it("suggests teams first with their size and first names, then people", async () => {
		renderField();
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		await type(input, "a");
		const options = await screen.findAllByRole("option");
		expect(options.map((option) => option.getAttribute("data-people-option"))).toEqual(["team", "team", "person", "person"]);
		expect(options[0]!.textContent).toContain("6 people · Ben, Chen, Dana and 3 more");
	});

	it("picks with the keyboard, skips what can't be picked, and removes with Backspace", async () => {
		renderField({ unavailable: new Set(["team:t-platform"]) });
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		await type(input, "a");
		await screen.findAllByRole("option");
		const platform = screen.getAllByRole("option")[0]!;
		expect(platform.getAttribute("aria-disabled")).toBe("true");
		expect(platform.textContent).toContain("Has access");
		fireEvent.keyDown(input, { key: "Enter" });
		expect(value()).toEqual([{ kind: "team", teamId: "t-data", name: "Data infra", memberCount: 4 }]);
		fireEvent.keyDown(input, { key: "Backspace" });
		expect(value()).toEqual([]);
	});

	it("adds any full email, lower-cased, without looking it up", async () => {
		renderField();
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		await type(input, "Sam@Contractor.io");
		const email = await screen.findByText("Add sam@contractor.io");
		fireEvent.click(email);
		expect(value()).toEqual([{ kind: "person", email: "sam@contractor.io" }]);
		await type(input, "lee@acme.co,");
		fireEvent.keyDown(input, { key: "," });
		expect(value()).toEqual([{ kind: "person", email: "sam@contractor.io" }, { kind: "person", email: "lee@acme.co" }]);
	});

	it("shows people in the workspace as already having access when sharing, but not for teams", async () => {
		mocks.candidates = [{ kind: "user", name: "Farah Nasser", email: "farah@northwind.dev", workspaceMember: true }, { kind: "user", name: "Riya Sen", email: "riya@acme.co" }];
		const sharing = renderField({ workspaceHasAccess: true });
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		await type(input, "a");
		const [farah, riya] = await screen.findAllByRole("option");
		expect(farah!.getAttribute("aria-disabled")).toBe("true");
		expect(farah!.textContent).toContain("In the workspace");
		expect(riya!.getAttribute("aria-disabled")).toBeNull();
		sharing.unmount();
		renderField();
		const teamInput = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(teamInput);
		await type(teamInput, "a");
		expect((await screen.findAllByRole("option"))[0]!.getAttribute("aria-disabled")).toBeNull();
	});

	it("closes the suggestions after a pick so the pick is in view", async () => {
		renderField();
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		act(() => input.focus());
		await type(input, "dana");
		fireEvent.click(await screen.findByText("Dana Kim"));
		expect(document.activeElement).toBe(input);
		expect(screen.queryByRole("listbox")).toBeNull();
		fireEvent.click(input);
		expect(await screen.findByRole("listbox")).toBeInTheDocument();
	});

	it("clears the delayed blur close when the field unmounts", () => {
		const setTimeoutSpy = vi.spyOn(window, "setTimeout");
		const clearTimeoutSpy = vi.spyOn(window, "clearTimeout");
		try {
			const view = renderField();
			fireEvent.blur(screen.getByRole("combobox", { name: "Add people or teams" }));
			const blurTimer = setTimeoutSpy.mock.results.at(-1)?.value;
			expect(blurTimer).toBeDefined();
			view.unmount();
			expect(clearTimeoutSpy).toHaveBeenCalledWith(blurTimer);
		} finally {
			setTimeoutSpy.mockRestore();
			clearTimeoutSpy.mockRestore();
		}
	});

	it("offers New team with whoever is picked so far", async () => {
		const onNewTeam = vi.fn();
		renderField({ onNewTeam });
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		await type(input, "dana");
		fireEvent.click(await screen.findByText("Dana Kim"));
		fireEvent.focus(input);
		await type(input, "riya");
		fireEvent.click(await screen.findByText("New team…"));
		// What was typed goes along, for the new team's name.
		expect(onNewTeam).toHaveBeenCalledWith([{ kind: "person", email: "dana@northwind.dev", name: "Dana Kim" }], "riya");
	});

	it("narrows the suggestions to what's typed before the answer for it arrives", async () => {
		renderField();
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		await type(input, "a");
		await screen.findByText("Dana Kim");
		// Typed on, with no time for the next answer: only matches stay.
		fireEvent.change(input, { target: { value: "riya" } });
		expect(screen.queryByText("Dana Kim")).toBeNull();
		expect(screen.queryByText("Platform")).toBeNull();
		expect(screen.getByText("Riya Sen")).toBeTruthy();
	});

	it("takes a whole email as that address, never someone whose email only contains it", async () => {
		mocks.candidates.push({ kind: "user", name: "Vaibhaav Tiwari", email: "vaibhaavtiwari@gmail.com" });
		renderField();
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		await type(input, "tiwari@gmail.com");
		const options = await screen.findAllByRole("option");
		// The look-alike is offered, but it isn't what Enter picks.
		expect(options.some((option) => option.textContent?.includes("Vaibhaav Tiwari"))).toBe(true);
		fireEvent.keyDown(input, { key: "Enter" });
		expect(value()).toEqual([{ kind: "person", email: "tiwari@gmail.com" }]);
	});

	it("never adds your own email, and says it's you", async () => {
		renderField();
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		await type(input, "me@acme.co");
		const you = (await screen.findAllByRole("option")).find((option) => option.textContent?.includes("me@acme.co"))!;
		expect(you.getAttribute("aria-disabled")).toBe("true");
		expect(you.textContent).toContain("That's you");
		for (const key of ["Enter", "Tab", ","]) fireEvent.keyDown(input, { key });
		expect(value()).toEqual([]);
	});

	it("closes the suggestions when Tab adds a typed email, so they don't cover what follows", async () => {
		renderField();
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		fireEvent.click(input);
		await type(input, "new.person@northwind.dev");
		await screen.findAllByRole("option");
		fireEvent.keyDown(input, { key: "Tab" });
		expect(value()).toEqual([{ kind: "person", email: "new.person@northwind.dev" }]);
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 160));
		});
		expect(screen.queryAllByRole("option")).toEqual([]);
	});

	it("adds a pasted email on Enter instead of a match for the text before it", async () => {
		renderField();
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		await type(input, "dana");
		await screen.findByText("Dana Kim");
		// Pasted and entered before the suggestions for it arrive.
		fireEvent.change(input, { target: { value: "riya@acme.co" } });
		fireEvent.keyDown(input, { key: "Enter" });
		expect(value()).toEqual([{ kind: "person", email: "riya@acme.co" }]);
	});

	it("picks the suggestion moved to in an empty field rather than sharing", async () => {
		const onSubmit = vi.fn();
		renderField({ onSubmit });
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		await type(input, "dana");
		await screen.findByText("Dana Kim");
		fireEvent.keyDown(input, { key: "Enter" });
		expect(value()).toEqual([{ kind: "person", email: "dana@northwind.dev", name: "Dana Kim" }]);
		await type(input, "");
		fireEvent.keyDown(input, { key: "ArrowDown" });
		await screen.findByRole("listbox");
		fireEvent.keyDown(input, { key: "Enter" });
		expect(value().map((recipient) => (recipient.kind === "team" ? recipient.teamId : recipient.email))).toEqual(["dana@northwind.dev", "t-platform"]);
		expect(onSubmit).not.toHaveBeenCalled();
		// With the list shut and nothing typed, Enter shares.
		fireEvent.keyDown(input, { key: "Enter" });
		expect(onSubmit).toHaveBeenCalledTimes(1);
	});

	it("never opens New team by default; it takes an arrow key", async () => {
		const onNewTeam = vi.fn();
		renderField({ onNewTeam });
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		fireEvent.focus(input);
		await type(input, "zzz");
		await screen.findByText("New team…");
		fireEvent.keyDown(input, { key: "Enter" });
		expect(onNewTeam).not.toHaveBeenCalled();
		fireEvent.keyDown(input, { key: "ArrowDown" });
		fireEvent.keyDown(input, { key: "Enter" });
		expect(onNewTeam).toHaveBeenCalledWith([], "zzz");
	});

	it("loads the suggestions before they are asked for, and doesn't open them on focus alone", async () => {
		renderField();
		const input = screen.getByRole("combobox", { name: "Add people or teams" });
		expect(mocks.queries).toEqual([""]);
		fireEvent.focus(input);
		await act(async () => {
			await new Promise((resolve) => setTimeout(resolve, 20));
		});
		expect(screen.queryByRole("listbox")).toBeNull();
		fireEvent.keyDown(input, { key: "ArrowDown" });
		expect(await screen.findByRole("listbox")).toBeInTheDocument();
	});
});
