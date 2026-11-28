// ESLint for the frontend. Scope is deliberately narrow: TypeScript parsing
// plus the React hooks rules, which the typechecker cannot enforce. Add rules
// in their own PRs so each one lands with its violations fixed.
import reactHooks from "eslint-plugin-react-hooks";
import tseslint from "typescript-eslint";

export default tseslint.config(
	{
		ignores: ["src/docs/**", "src/api/schema.ts", "src/renderer/routeTree.gen.ts"],
	},
	{
		files: ["src/**/*.{ts,tsx}"],
		extends: [tseslint.configs.base],
		// Existing disable comments name rules that are not enabled yet; keep
		// them as documentation rather than churning them in this PR.
		linterOptions: { reportUnusedDisableDirectives: "off" },
	},
	{
		// React only runs in the renderer; the Electron main process has
		// functions named use* that are not hooks.
		files: ["src/renderer/**/*.{ts,tsx}"],
		plugins: { "react-hooks": reactHooks },
		rules: {
			"react-hooks/rules-of-hooks": "error",
			"react-hooks/exhaustive-deps": "warn",
		},
	},
);
