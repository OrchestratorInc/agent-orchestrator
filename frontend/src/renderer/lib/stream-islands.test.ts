import { describe, expect, it } from "vitest";
import { CODE_ISLAND, IMAGE_ISLAND, LINK_ISLAND, codeIsland, headingIslands, imageIsland, islandHref, linkIsland, proseIslands } from "./stream-islands";

const hosts = (html: string) =>
	[...new DOMParser().parseFromString(html, "text/html").querySelectorAll("[data-words-island]")]
		.filter((host) => !host.parentElement?.closest("[data-words-island]"))
		.map((host) => [host.getAttribute("data-words-island"), JSON.parse(host.getAttribute("data-words-island-data")!)]);

describe("stream islands", () => {
	it("hosts a finished link with its decoded href and label, and leaves an unfinished one", () => {
		const href = islandHref("ao://sessions/p/s?x=1&y=\"2\"").replaceAll("&", "&amp;");
		expect(hosts(linkIsland(`<a data-markdown="a" href="${href}">the <strong data-markdown="strong">docs</strong></a>`))).toEqual([
			[LINK_ISLAND, { id: LINK_ISLAND, href: 'ao://sessions/p/s?x=1&y="2"', label: 'the <strong data-markdown="strong">docs</strong>' }],
		]);
		const unfinished = '<a data-markdown="incomplete-link">the docs</a>';
		expect(linkIsland(unfinished)).toBe(unfinished);
		const unsafe = '<a data-markdown="a" href="">x</a>';
		expect(linkIsland(unsafe)).toBe(unsafe);
	});

	it("hosts inline code with its text", () => {
		expect(hosts(codeIsland('<code data-markdown="code">a &lt;b&gt; &amp; c</code>'))).toEqual([[CODE_ISLAND, { id: CODE_ISLAND, text: "a <b> & c" }]]);
	});

	it("hosts a finished image with its decoded src and alt", () => {
		const src = islandHref("https://x.test/a b.png");
		expect(hosts(imageIsland(`<img data-markdown="img" src="${src}" alt="a &quot;q&quot; &amp; b" loading="lazy">`))).toEqual([
			[IMAGE_ISLAND, { id: IMAGE_ISLAND, src: "https://x.test/a b.png", alt: 'a "q" & b' }],
		]);
	});

	it("links quoted session links without their quotes", () => {
		const html = proseIslands('<p data-markdown="p">See &quot;ao://sessions/p/s&quot; and &#39;ao://sessions/p/t&#39;.</p>');
		expect(hosts(html).map(([, data]) => data.href)).toEqual(["ao://sessions/p/s", "ao://sessions/p/t"]);
		expect(new DOMParser().parseFromString(html, "text/html").body.textContent).toBe(`See "ao://sessions/p/s" and 'ao://sessions/p/t'.`);
	});

	it("links bare session links in prose only", () => {
		const code = codeIsland('<code data-markdown="code">ao://sessions/p/in-code</code>');
		const html = proseIslands(`<p data-markdown="p">See ao://sessions/p/s, <em>ao://sessions/p/t</em> and ${code}<br>ao://sessions/p/u.</p>`);
		expect(hosts(html).map(([name, data]) => [name, data.href])).toEqual([
			[LINK_ISLAND, "ao://sessions/p/s"],
			[LINK_ISLAND, "ao://sessions/p/t"],
			[CODE_ISLAND, undefined],
			[LINK_ISLAND, "ao://sessions/p/u"],
		]);
	});

	it("compacts emoji in a block's own text, as the settled reply does, and links headings without them", () => {
		const emoji = (html: string) => [...new DOMParser().parseFromString(html, "text/html").querySelectorAll(".chat-md-emoji")].map((span) => span.textContent);
		const prose = proseIslands('<p data-markdown="p">Done ✅ &amp; 🇮🇳 <em>kept 🎉</em> ao://sessions/p/s 👍</p>');
		expect(emoji(prose)).toEqual(["✅", "🇮🇳", "👍"]);
		expect(new DOMParser().parseFromString(prose, "text/html").body.textContent).toBe("Done ✅ & 🇮🇳 kept 🎉 ao://sessions/p/s 👍");
		const heading = headingIslands('<h2 data-markdown="h2">Report ✅ ao://sessions/p/s</h2>');
		expect(emoji(heading)).toEqual([]);
		expect(hosts(heading).map(([, data]) => data.href)).toEqual(["ao://sessions/p/s"]);
	});
});
