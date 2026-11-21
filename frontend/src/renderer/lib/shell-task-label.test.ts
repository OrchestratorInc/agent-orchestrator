import { describe, expect, it } from "vitest";
import { shellTaskLabel } from "./shell-task-label";

describe("shellTaskLabel", () => {
	it.each([
		["PORT=3000 npm run dev > /tmp/dev.log 2>&1", { kind: "devServer", port: "3000" }],
		["cd frontend && pnpm dev --port 5173 &", { kind: "devServer", port: "5173", dir: "frontend" }],
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
		// Windows: cmd and PowerShell set the port in a step of their own.
		["set PORT=3000&& npm run dev", { kind: "devServer", port: "3000" }],
		['set "PORT=3000" && npm run dev', { kind: "devServer", port: "3000" }],
		["$env:PORT=3000; npm run dev", { kind: "devServer", port: "3000" }],
		["set PORT=%PORT%&& npm run dev", { kind: "devServer" }],
		["export PORT=3000 && npm run dev", { kind: "devServer", port: "3000" }],
		// Only a number is a port.
		["PORT=$PORT npm run dev", { kind: "devServer" }],
		["PORT=abc npm run dev", { kind: "devServer" }],
		// The subcommand decides: building is not serving.
		["npx vite build --watch", { kind: "build" }],
		["vite preview", { kind: "devServer" }],
		["hugo", { kind: "build" }],
		["hugo server -D", { kind: "devServer" }],
		["jekyll build", { kind: "build" }],
		["jekyll serve", { kind: "devServer" }],
		// A wrapper's own options are not the program.
		["npx --yes vite build --watch", { kind: "build" }],
		["npx -y vitest", { kind: "tests" }],
		["npx --yes http-server -p 8089", { kind: "devServer", port: "8089" }],
		["sudo -u bob npm start", { kind: "devServer" }],
		// Shell interpreters hand over the command to read.
		["sh -c 'cd web && npm run dev'", { kind: "devServer", dir: "web" }],
		['bash -lc "npm run test"', { kind: "tests" }],
		[`"C:\\Windows\\system32\\cmd.exe" /c 'npx --yes http-server -p 8089'`, { kind: "devServer", port: "8089" }],
		[`"C:\\\\Windows\\\\system32\\\\cmd.exe" /c "bash -c 'cd frontend && PORT="'$PORT npm run dev'"'"`, { kind: "devServer", dir: "frontend" }],
		["cmd.exe /d /s /c \"npm run build\"", { kind: "build" }],
		["pwsh -NoProfile -Command npm run lint", { kind: "lint" }],
		// Of a lone "&", the step it backgrounds is the task, not the one-shot after it.
		["npm run dev > /tmp/dev.log 2>&1 & echo $!", { kind: "devServer" }],
	])("reads %s", (command, label) => {
		expect(shellTaskLabel(command)).toEqual(label);
	});

	it.each([
		["DEBUG=1 nohup ./scripts/watch-assets.sh > /tmp/assets.log 2>&1 &", "./scripts/watch-assets.sh"],
		["ping -n 600 127.0.0.1 > NUL & echo started", "ping -n 600 127.0.0.1"],
		["FOO='a b' node x.js", "node x.js"],
		['PORT=1 node -e "a; b"', 'node -e "a; b"'],
		["npm run start:prod > /tmp/prod.log", "npm run start:prod"],
	])("keeps the words of %s that name the program", (command, text) => {
		expect(shellTaskLabel(command)).toEqual({ kind: "command", text });
	});

	it.each([
		"Start the renderer dev server",
		"Watch the composer tests",
		"Serve the docs site",
		"Vite dev server for the renderer",
		"Jest watch for composer",
		"sleep 300",
		"Build and test; then report",
		'curl "http://x/a?b=1 & c=2" -o out',
		"curl 'http://x/a?b=1 & c=2'",
		// Two long-running steps: no one name fits both.
		"npm run dev & npm run test:watch",
		// A quote that never closes is not something to take apart.
		"sh -c 'cd web && npm run dev",
	])("leaves %s as written", (description) => {
		expect(shellTaskLabel(description)).toEqual({ kind: "command", text: description });
	});

	it("reads a description padded with spaces", () => {
		expect(shellTaskLabel("  npm run build  ")).toEqual({ kind: "build" });
	});
});
