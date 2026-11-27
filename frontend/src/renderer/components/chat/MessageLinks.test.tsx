import { fireEvent, render as rtlRender, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ReactElement } from "react";
import { ChatLinkProvider, ChatMarkdown } from "./ChatMarkdown";
import { TurnActions } from "./ChatTimelineItems";
import { linkSiteLabel, messageWebLinks } from "./MessageLinks";
import { TooltipProvider } from "../ui/tooltip";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider><ChatLinkProvider onLinkOpen={() => {}}>{ui}</ChatLinkProvider></TooltipProvider>);
}

const REPLY = [
	"I can't decompile the client. See https://www.anthropic.com/legal/commercial-terms.",
	"Instead I fixed the crash (https://github.com/electron/electron/issues/41322) following the",
	"[Electron security checklist](https://www.electronjs.org/docs/latest/tutorial/security).",
	"The app is running at http://localhost:3000/settings.",
	"",
	"```bash",
	"curl https://example.com/not-a-citation",
	"```",
	"And `https://example.org/also-code` stays code; https://www.anthropic.com/legal/commercial-terms again.",
].join("\n");

describe("messageWebLinks", () => {
	it("gathers a reply's web links in order, once each, outside code", () => {
		expect(messageWebLinks(REPLY)).toEqual([
			{ url: "https://www.anthropic.com/legal/commercial-terms" },
			{ url: "https://github.com/electron/electron/issues/41322" },
			{ url: "https://www.electronjs.org/docs/latest/tutorial/security", text: "Electron security checklist" },
			{ url: "http://localhost:3000/settings" },
		]);
	});

	it("leaves out files, sessions and mail", () => {
		expect(messageWebLinks("[a](./src/a.ts) [s](ao://sessions/p/s) mailto:x@y.z")).toEqual([]);
	});

	it("names a link by its site, or a local page by host and port", () => {
		expect(linkSiteLabel("https://www.anthropic.com/legal")).toBe("anthropic.com");
		expect(linkSiteLabel("http://localhost:3000/settings")).toBe("localhost:3000");
	});
});

describe("inline links", () => {
	it("draws a bare address as its site's chip and keeps a worded link as text", () => {
		const { container } = render(<ChatMarkdown text={REPLY} />);
		const sites = [...container.querySelectorAll("[data-link-site]")].map((site) => site.textContent);
		// A GitHub issue reads as GitHub writes it, with GitHub's mark.
		expect(sites).toEqual(["anthropic.com", "electron#41322", "localhost:3000", "anthropic.com"]);
		// Its own words stay underlined text.
		expect(screen.getByRole("link", { name: /Electron security checklist/ })).not.toHaveTextContent("electronjs.org");
	});

	it("keeps a pull request's own chip", () => {
		const { container } = render(<ChatMarkdown text="See https://github.com/o/r/pull/12 for it." />);
		expect(container.querySelector("[data-link-site]")).toBeNull();
		expect(screen.getByRole("link")).toHaveTextContent("#12");
	});
});

describe("the links button", () => {
	it("shows for two or more links and opens a list of them", () => {
		render(<TurnActions copyText={REPLY} />);
		const button = screen.getByRole("button", { name: "4 links" });
		expect(button).toHaveAttribute("aria-expanded", "false");
		expect(document.querySelector("[data-message-links-list]")).toBeNull();

		fireEvent.click(button);
		expect(button).toHaveAttribute("aria-expanded", "true");
		const list = document.querySelector<HTMLElement>("[data-message-links-list]")!;
		const rows = within(list).getAllByRole("link");
		expect(rows.map((row) => row.getAttribute("href"))).toEqual([
			"https://www.anthropic.com/legal/commercial-terms",
			"https://github.com/electron/electron/issues/41322",
			"https://www.electronjs.org/docs/latest/tutorial/security",
			"http://localhost:3000/settings",
		]);
		// A worded link is titled by its words until the page's title arrives; a local one opens in the Browser.
		expect(rows[2]).toHaveTextContent("Electron security checklist");
		expect(rows[3]).toHaveTextContent("localhost:3000/settings");
		expect(rows[3]).toHaveTextContent("Browser");
	});

	it("stays away for a single link", () => {
		render(<TurnActions copyText="Only https://example.com here." />);
		expect(screen.queryByRole("button", { name: /links$/ })).toBeNull();
	});
});
