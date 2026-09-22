import { expect, it } from "vitest";
import { terminalEnhanceScript } from "./terminalEnhanceScript";

it("retains the Local terminal's fit reporting and zoom/pan controls", () => {
	expect(terminalEnhanceScript("ios")).toContain("__aoAdjustTerminalZoom");
	expect(terminalEnhanceScript("ios")).toContain("FRESSH_DIMS");
	expect(terminalEnhanceScript("android")).toContain("mode = 'pinch'");
	expect(terminalEnhanceScript("android")).toContain("var IS_ANDROID=true;");
});
