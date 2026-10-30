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

function Harness({ onNewTeam, unavailable }: { onNewTeam?: (people: Recipient[]) => void; unavailable?: Set<string> }) {
	const [value, setValue] = useState<Recipient[]>([]);
	return (
		<>
			<PeopleField orgId="org" value={value} onChange={setValue} placeholder="Add people or teams" ariaLabel="Add people or teams" onNewTeam={onNewTeam} unavailable={unavailable} />
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
		expect(onNewTeam).toHaveBeenCalledWith([{ kind: "person", email: "dana@northwind.dev", name: "Dana Kim" }]);
	});
});
