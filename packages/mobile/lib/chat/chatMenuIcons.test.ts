import { describe, expect, it } from "vitest";
import { materialDrawableNames } from "../toolbarIcon.types";
import type { ChatMenuIcon } from "./chatMenuIcons";
import { chatMenuSymbols } from "./chatMenuIcons.types";

// Android's menu draws an image source from the shared registry; an action
// naming a symbol the registry does not bundle would draw nothing.
describe("chat menu icons", () => {
	it("draws every action's Material Symbol from the shared drawable registry on Android", () => {
		for (const name of Object.keys(chatMenuSymbols) as ChatMenuIcon[]) expect(materialDrawableNames, name).toContain(chatMenuSymbols[name].android);
	});
});
