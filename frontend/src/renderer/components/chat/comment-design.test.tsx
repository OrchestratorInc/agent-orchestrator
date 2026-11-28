import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CHAT_FLASH_MS, clearChatFlash, flashChatMessage } from "../../lib/chat-flash";
import { TeamTile } from "../teams/TeamVisuals";
import { asideBoxClass } from "./AsideMarker";
import { CHAT_BUBBLE_SHAPE } from "./chatBubble";
import { humanBubbleClass } from "./ChatTimelineItems";

// DESIGN.md rules for the comments and mentions surfaces: tokens, not one-off
// hex, radius or leading; rows through Pressable; one shared tint flash.

const source = (path: string) => readFileSync(resolve(__dirname, path), "utf8");
const tokensCss = source("../../../styles/tokens.css");
const stylesCss = source("../../styles.css");

describe("comment and mention surfaces follow DESIGN.md", () => {
	const files = ["AsideMarker.tsx", "AsidePane.tsx", "ComposerSuggestMenu.tsx", "MentionChip.tsx", "../ThreadsView.tsx"];

	it.each(files)("%s uses no one-off hex, radius, leading or type size, and no raw <button>", (file) => {
		const text = source(file);
		expect(text).not.toMatch(/#facc15/i);
		expect(text).not.toMatch(/rounded-\[\d/);
		expect(text).not.toMatch(/leading-\[\d/);
		expect(text).not.toMatch(/text-\[\d+(\.\d+)?px\]/);
		expect(text).not.toMatch(/<button\b/);
	});

	it("paints comment highlights and quote edges from tokens defined for dark and light", () => {
		expect(stylesCss).not.toMatch(/#facc15/i);
		expect(stylesCss).toMatch(/::highlight\(ao-comment\)\s*\{\s*background-color: var\(--color-comment-highlight\);/);
		expect(stylesCss).toMatch(/::highlight\(ao-comment-active\)\s*\{\s*background-color: var\(--color-comment-highlight-strong\);/);
		const light = tokensCss.match(/:root\[data-theme="light"\]\s*\{([\s\S]*?)\n\}/)?.[1] ?? "";
		const dark = tokensCss.slice(0, tokensCss.indexOf(':root[data-theme="light"] {'));
		for (const token of ["--color-comment-highlight", "--color-comment-highlight-strong", "--color-comment-quote-edge"]) {
			const value = (block: string) => block.match(new RegExp(`${token}:\\s*([^;]+);`))?.[1];
			expect(value(dark), token).toBeDefined();
			expect(value(light), token).toBeDefined();
			// Light deepens the yellow: the dark theme's pale mix vanishes on a white canvas.
			expect(value(light), token).not.toBe(value(dark));
		}
	});

	it("flashes a target with a background tint, never a ring or glow, and keeps an outline under reduced motion", () => {
		const rule = stylesCss.match(/\[data-chat-flash\]\s*\{([^}]*)\}/)?.[1] ?? "";
		expect(rule).toContain("animation: chat-target-flash");
		const keyframes = stylesCss.match(/@keyframes chat-target-flash\s*\{([\s\S]*?)\n\}/)?.[1] ?? "";
		expect(keyframes).toContain("background-color");
		expect(keyframes).not.toContain("box-shadow");
		expect(stylesCss).not.toMatch(/data-mention-focus|data-comment-flash/);
		const reduced = stylesCss.match(/@media \(prefers-reduced-motion: reduce\) \{\s*\[data-chat-flash\] \{([^}]*)\}/)?.[1] ?? "";
		expect(reduced).toContain("animation: none");
		expect(reduced).toContain("outline: 1px solid");
	});

	it("draws a tagged message in the user's bubble shape, with only its edge changed", () => {
		for (const shape of CHAT_BUBBLE_SHAPE.split(" ")) {
			expect(humanBubbleClass().split(" ")).toContain(shape);
			expect(asideBoxClass().split(" ")).toContain(shape);
		}
		expect(asideBoxClass()).toContain("border-dashed");
	});

	it("gives a tagged message the bubble's own text size, line height and padding in the timeline", () => {
		expect(asideBoxClass().split(" ")).toContain("chat-bubble");
		const css = stylesCss.replace(/\/\*[\s\S]*?\*\//g, "");
		const rules = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(([, selectors]) => selectors!.split(",").map((selector) => selector.trim()));
		const bubbleRules = rules.filter((selectors) => selectors.some((selector) => selector === ".cursor-chat-timeline .cursor-chat-human-message" || selector === "html[data-new-desktop-ui] .cursor-chat-human-message"));
		// The line height (1.58), the 14px text and Interface v2's padding.
		expect(bubbleRules.length).toBeGreaterThanOrEqual(3);
		for (const selectors of bubbleRules) {
			const scope = selectors.find((selector) => selector.endsWith(" .cursor-chat-human-message"))!.replace(/ \.cursor-chat-human-message$/, "");
			expect(selectors, selectors.join(", ")).toContain(`${scope} .chat-bubble`);
		}
	});

	it("gives an inline team tile its own xs size", () => {
		render(<TeamTile name="Platform" size="xs" />);
		const tile = document.querySelector("[data-team-tile]")!;
		expect(tile.className).toContain("size-4");
		expect(tile.className).toContain("rounded-xs");
		expect(tile.className).not.toMatch(/text-\[\d/);
	});
});

describe("the shared message flash", () => {
	afterEach(() => {
		clearChatFlash();
		vi.useRealTimers();
		document.body.innerHTML = "";
	});

	it("tints a message's bubble when it has one, and the message itself otherwise", () => {
		document.body.innerHTML = '<div id="human"><div class="cursor-chat-human-message">hi</div></div><div id="agent"><p>answer</p></div>';
		const human = document.getElementById("human")!;
		flashChatMessage(human);
		expect(human.querySelector(".cursor-chat-human-message")).toHaveAttribute("data-chat-flash");
		expect(human).not.toHaveAttribute("data-chat-flash");
		const agent = document.getElementById("agent")!;
		flashChatMessage(agent);
		expect(agent).toHaveAttribute("data-chat-flash");
		// One message at a time: the newer flash ended the first.
		expect(human.querySelector("[data-chat-flash]")).toBeNull();
	});

	it("ends after its time", () => {
		vi.useFakeTimers();
		document.body.innerHTML = '<div id="agent"><p>answer</p></div>';
		const agent = document.getElementById("agent")!;
		flashChatMessage(agent);
		vi.advanceTimersByTime(CHAT_FLASH_MS - 1);
		expect(agent).toHaveAttribute("data-chat-flash");
		vi.advanceTimersByTime(1);
		expect(agent).not.toHaveAttribute("data-chat-flash");
	});
});
