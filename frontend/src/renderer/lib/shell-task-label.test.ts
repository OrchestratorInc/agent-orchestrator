import { describe, expect, it } from "vitest";
import { shellTaskLabel } from "./shell-task-label";

describe("shellTaskLabel", () => {
	it.each([
		["PORT=3000 npm run dev > /tmp/ronish-dev.log 2>&1", { kind: "devServer", port: "3000" }],
		["cd frontend && pnpm dev --port 5173 &", { kind: "devServer", port: "5173" }],
		["nohup npx vite --port=4000 >/tmp/vite.log 2>&1 &", { kind: "devServer", port: "4000" }],
		["yarn start", { kind: "devServer" }],
		["npm run dev:web | tee /tmp/web.log", { kind: "devServer" }],
		["python3 -m http.server 8000", { kind: "devServer", port: "8000" }],
		["next dev -p 3001", { kind: "devServer", port: "3001" }],
		["npx vitest --maxWorkers=2", { kind: "tests" }],
		["go test ./... > /tmp/go.log 2>&1", { kind: "tests" }],
		["npm run build", { kind: "build" }],
		["pnpm lint:fix", { kind: "lint" }],
		["npm ci", { kind: "install" }],
	])("reads %s", (command, label) => {
		expect(shellTaskLabel(command)).toEqual(label);
	});

	it("keeps an unknown program's words but drops the environment and log plumbing", () => {
		expect(shellTaskLabel("DEBUG=1 nohup ./scripts/watch-assets.sh > /tmp/assets.log 2>&1 &"))
			.toEqual({ kind: "command", text: "./scripts/watch-assets.sh" });
	});

	it.each(["Start the renderer dev server", "Watch the composer tests", "sleep 300", "Build and test; then report"])(
		"leaves %s as written",
		(description) => {
			expect(shellTaskLabel(description)).toEqual({ kind: "command", text: description });
		},
	);
});
