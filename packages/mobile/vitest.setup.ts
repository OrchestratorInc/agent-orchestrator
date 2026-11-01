import { vi } from "vitest";

// expo-symbols cannot load under node: its entry reaches expo-modules-core
// through expo-font. `AppSymbol` is meant to sit in widely shared chrome, so it
// is mocked once here rather than in every test that happens to render one.
// The host element keeps every prop, so a test can still read which symbol,
// tint and accessibility a glyph was given.
vi.mock("expo-symbols", () => ({
	SymbolView: "SymbolView",
	unstable_getMaterialSymbolSourceAsync: async () => null,
}));
