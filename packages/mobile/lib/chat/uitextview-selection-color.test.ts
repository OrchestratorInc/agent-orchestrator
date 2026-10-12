import { createRequire } from "node:module";
import { expect, it } from "vitest";

const require = createRequire(import.meta.url);
const { transformFileSync } = require("@babel/core") as {
	transformFileSync: (path: string, options: object) => { code?: string } | null;
};

it("passes selectionColor through the UITextView JavaScript entry used by Metro", () => {
	const entry = require.resolve("@bsky.app/react-native-uitextview/lib/commonjs/RNUITextViewNativeComponent.ts");
	const viewConfig = transformFileSync(entry, {
		babelrc: false,
		configFile: false,
		plugins: ["@react-native/babel-plugin-codegen"],
		presets: ["@babel/preset-typescript"],
	})?.code;

	expect(viewConfig).toMatch(/selectionColor:\s*require\('react-native\/Libraries\/Components\/View\/ReactNativeStyleAttributes'\)\.colorAttribute/);
});
