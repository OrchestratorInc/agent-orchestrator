import { describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ StyleSheet: { create: (styles: object) => styles } }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }) }));
vi.mock("lucide-react-native/icons/star", () => ({ default: "Star" }));
vi.mock("./AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./haptics", () => ({ haptics: { select: () => {} } }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({}), useThemeState: () => ({ scheme: "light" }), useThemedStyles: () => ({}) }));

import type { WheelAgent } from "./chat/useChatModelWheel";
import { firstPickable, matchModels } from "./model-search";

const agents: WheelAgent[] = [
	{ id: "claude-code", label: "Claude", models: [{ key: "opus", label: "Opus 4.7", starred: true }, { key: "sonnet", label: "Sonnet 4.6" }] },
	{ id: "codex", label: "Codex", models: [{ key: "gpt-5.5", label: "GPT-5.5" }, { key: "gpt-5.5-mini", label: "GPT-5.5 mini" }] },
];

const keys = (query: string, locked = false) => matchModels(agents, query, "claude-code", locked).map((section) => [section.agent.id, section.data.map((model) => model.key)]);

describe("model search", () => {
	it("lists every agent's models when nothing is typed", () => {
		expect(keys("")).toEqual([["claude-code", ["opus", "sonnet"]], ["codex", ["gpt-5.5", "gpt-5.5-mini"]]]);
	});

	it("matches any case, by name or id, every word typed", () => {
		expect(keys("SONNET")).toEqual([["claude-code", ["sonnet"]]]);
		expect(keys("gpt mini")).toEqual([["codex", ["gpt-5.5-mini"]]]);
		// The agent's name finds its models.
		expect(keys("codex")).toEqual([["codex", ["gpt-5.5", "gpt-5.5-mini"]]]);
		expect(keys("nothing like it")).toEqual([]);
	});

	it("goes to the first match an agent can switch to", () => {
		expect(firstPickable(matchModels(agents, "5", "claude-code", false))).toEqual({ agent: "codex", model: "gpt-5.5" });
		// While the agent works, the others' models cannot be picked.
		expect(firstPickable(matchModels(agents, "gpt", "claude-code", true))).toBeUndefined();
		expect(firstPickable(matchModels(agents, "", "codex", true))).toEqual({ agent: "codex", model: "gpt-5.5" });
	});
});
