import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { GitHubRepoPicker } from "./GitHubRepoPicker";

type Repo = { fullName: string; description: string; isPrivate: boolean; url: string; avatarUrl: string };

function repo(fullName: string, overrides: Partial<Repo> = {}): Repo {
	return {
		fullName,
		description: "",
		isPrivate: false,
		url: `https://github.com/${fullName}`,
		avatarUrl: `https://github.com/${fullName.split("/")[0]}.png?size=40`,
		...overrides,
	};
}

const SEARCH_PLACEHOLDER = "Search your repositories or paste a URL";

let own: Repo[] = [];
let remote: Record<string, Repo[]> = {};
let listGitHubRepos: ReturnType<typeof vi.fn>;

function renderPicker() {
	const onChange = vi.fn();
	render(<GitHubRepoPicker value="" onChange={onChange} />);
	fireEvent.click(screen.getByRole("combobox"));
	return { onChange };
}

// The picker keeps the user's own repositories in a module-level cache once a
// load succeeds, so the empty-list case runs first and every later case shares
// the same own-repository list.
describe("GitHubRepoPicker", () => {
	beforeEach(() => {
		listGitHubRepos = vi.fn(async (query: string) => (query === "" ? own : (remote[query] ?? [])));
		window.ao!.app.listGitHubRepos = listGitHubRepos as never;
		remote = {};
	});

	it("explains how to recover when no repositories load", async () => {
		own = [];
		renderPicker();
		expect(await screen.findByText("Couldn't load your repositories. Run gh auth login, or paste a URL.")).toBeInTheDocument();
	});

	it("filters the preloaded repositories as you type and picks one", async () => {
		own = [repo("acme/web-app"), repo("acme/api", { isPrivate: true })];
		const { onChange } = renderPicker();
		expect(await screen.findByText("acme/web-app")).toBeInTheDocument();
		expect(screen.getByText("acme/api")).toBeInTheDocument();
		expect(screen.getByLabelText("Private")).toBeInTheDocument();

		fireEvent.change(screen.getByPlaceholderText(SEARCH_PLACEHOLDER), { target: { value: "web" } });
		await waitFor(() => expect(screen.queryByText("acme/api")).not.toBeInTheDocument());
		fireEvent.click(screen.getByText("acme/web-app"));
		expect(onChange).toHaveBeenCalledWith("https://github.com/acme/web-app");
	});

	it("adds GitHub-wide search results after a pause without repeating your own repositories", async () => {
		own = [repo("acme/web-app"), repo("acme/api")];
		remote = { acme: [repo("acme/web-app"), repo("other/acme-tools")] };
		const { onChange } = renderPicker();
		await screen.findByText("acme/web-app");

		fireEvent.change(screen.getByPlaceholderText(SEARCH_PLACEHOLDER), { target: { value: "acme" } });
		expect(await screen.findByText("other/acme-tools")).toBeInTheDocument();
		expect(screen.getAllByText("acme/web-app")).toHaveLength(1);
		expect(listGitHubRepos).toHaveBeenCalledWith("acme");

		fireEvent.click(screen.getByText("other/acme-tools"));
		expect(onChange).toHaveBeenCalledWith("https://github.com/other/acme-tools");
	});

	it("offers a pasted URL as-is and does not search GitHub for it", async () => {
		own = [repo("acme/web-app")];
		const { onChange } = renderPicker();
		await screen.findByText("acme/web-app");

		const url = "https://github.com/someone/else.git";
		fireEvent.change(screen.getByPlaceholderText(SEARCH_PLACEHOLDER), { target: { value: url } });
		fireEvent.click(await screen.findByText(`Use ${url}`));
		expect(onChange).toHaveBeenCalledWith(url);
		await new Promise((resolve) => setTimeout(resolve, 500));
		expect(listGitHubRepos).not.toHaveBeenCalledWith(url);
	});
});
