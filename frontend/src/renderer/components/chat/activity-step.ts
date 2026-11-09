import type { ConversationActivity } from "../../types/conversation";
import { mcpToolName } from "../../lib/mcp-tool";

/**
 * How a shell command or an agent's built-in tool reads in the chat: what it did,
 * in the words a person would use, instead of one "Ran command" for everything.
 *
 * This only names the step. Which rows group together is decided by
 * `commandCategory` in activity-command.ts, from the same segment a row is named
 * by (`commandIntent`), so a row and the header above it agree.
 */

/** The mark beside a step. The renderer maps each one to a Lucide icon. */
export type StepIcon =
	| "list"
	| "read"
	| "search"
	| "find"
	| "git"
	| "commit"
	| "pr"
	| "issue"
	| "web"
	| "folder-new"
	| "file-new"
	| "write"
	| "move"
	| "copy"
	| "delete"
	| "test"
	| "install"
	| "build"
	| "check"
	| "format"
	| "wait"
	| "tool"
	| "run";

export interface CommandStep {
	/** Past tense, for a finished row: "Read", "Searched for". */
	done: string;
	/** Present tense, for a running row and the live line: "Reading", "Searching for". */
	live: string;
	/** What the step acted on, shown after the verb: a file, a pattern, a PR number. */
	target?: string;
	/** The whole target for the tooltip when `target` is shortened to a basename. */
	targetTitle?: string;
	icon: StepIcon;
}

const GENERIC_STEP: CommandStep = { done: "Ran command", live: "Running command", icon: "run" };

/**
 * The step a command activity shows. Codex reports a web search as a command with
 * no command line, only a summary, so that case is read from the summary.
 */
export function activityCommandStep(activity: ConversationActivity): CommandStep {
	const command = activity.detail?.command?.trim();
	if (command) return describeCommandStep(command);

	const title = activity.summary.trim();
	const summary = title.replace(/^`+|`+$/g, "");
	const web = /^Searched the web(?:\s+for\s+([\s\S]+))?$/i.exec(summary);
	if (web) return webSearchStep(web[1]);
	// Without a command line the summary is the provider's title for the call. Claude
	// Code titles a command with the command itself in backticks. Any other title may
	// be prose that starts like one ("make the build pass", "touch up README"), so it
	// is shown as written rather than read as a command.
	const backticked = title.length > 2 && title.startsWith("`") && title.endsWith("`");
	if (backticked && summary) return describeCommandStep(summary);
	return title && !NAMELESS_TITLE.test(title) ? { done: title, live: title, icon: "run" } : GENERIC_STEP;
}

/** A provider's title that names no particular command, so it says nothing a row can use. */
const NAMELESS_TITLE = /^(?:(?:ran|run|running)(?: a)? command|terminal|bash|shell|sh|exec|execute|command)$/i;

/**
 * The step for one of an agent's built-in tools. Claude Code's Read, Grep,
 * WebSearch and the rest reach AO over ACP as tool calls with the provider's own
 * title, and a web search is titled with nothing but its query. Each is named
 * here like the command that does the same job.
 *
 * A call to an MCP server returns undefined and keeps its own row, and so does a
 * tool this does not know. For those, only the ACP category picks an icon.
 */
export function activityToolStep(activity: ConversationActivity): CommandStep | undefined {
	const detail = activity.detail;
	if (!detail || mcpToolName(activity).server) return undefined;
	const input = (key: string): string | undefined => {
		const value = isRecord(detail.input) ? detail.input[key] : undefined;
		return typeof value === "string" && value.trim() ? value.trim() : undefined;
	};
	switch (detail.providerToolName) {
		case "Read": {
			const file = input("file_path") ?? input("path");
			return readStep(file ? [file] : []);
		}
		case "Grep":
			return searchStep(input("pattern"));
		case "Glob":
			return findStep(input("pattern"));
		case "LS": {
			const folder = input("path");
			const step = listStep(folder);
			return step.target && folder ? { ...step, ...fileTarget(folder) } : step;
		}
		case "WebSearch":
			return webSearchStep(input("query"));
		case "WebFetch":
			return urlStep(input("url"));
		case "ToolSearch":
			return toolSearchStep(input("query"));
	}
	const label = activity.summary.trim();
	const icon = detail.toolKind ? TOOL_KIND_ICONS[detail.toolKind] : undefined;
	return label && icon ? { done: label, live: label, icon } : undefined;
}

/**
 * The gist of a thought, for the live row beside the loader: what the agent is
 * working on, in its own words. Codex heads each part of its reasoning summary
 * with a bold line ("**Designing the cadence strip**"), and the newest one is the
 * part it is on. Claude Code writes prose that ends on what it will do next ("…
 * I'm creating the to-do list next."), so its last whole sentence is used. A
 * sentence still being written is skipped, and with nothing whole yet there is
 * no gist. Code in a fence is not a sentence, and a list marker ("1.", "-") is
 * not part of one, so both go before the text is read as prose.
 */
export function thoughtHeadline(text: string | undefined): string | undefined {
	if (!text?.trim()) return undefined;
	let fenced = false;
	const lines = text.split("\n").filter((line) => {
		if (!/^\s*(?:```|~~~)/.test(line)) return !fenced;
		fenced = !fenced;
		return false;
	});
	const headings = [...lines.join("\n").matchAll(/^[ \t]*\*\*([^*\n]+?)\*\*[ \t]*$/gm)]
		.map((match) => match[1]!.trim())
		.filter(Boolean);
	if (headings.length > 0) return headings.at(-1);
	const plain = lines
		.map((line) => line.replace(/^\s*(?:[-*+]|\d+[.)])\s+/, ""))
		.join("\n")
		.replace(/`([^`\n]*)`/g, "$1")
		.replace(/\*\*|__/g, "")
		.replace(/\s+/g, " ")
		.trim();
	// A sentence ends at . ! or ? followed by a space and a capital, a digit or an
	// opening quote, so "Node.js" and "26.13.0" do not split one.
	const sentences = plain.split(/(?<=[.!?]["')\]”’]?)\s+(?=[A-Z0-9"'“‘(\[])/);
	const whole = sentences.filter((sentence) => /[.!?]["')\]”’]?$/.test(sentence));
	const last = whole.at(-1)?.trim();
	return last ? last.replace(/\.$/, "") : undefined;
}

/**
 * A tool call that only looks at things: a read, a search, a fetch. A run made
 * only of these says "Explored", the way a run of `cat` and `rg` does.
 */
export function isExploratoryTool(activity: ConversationActivity): boolean {
	const detail = activity.detail;
	if (activity.activityKind !== "mcp_tool" || !detail || mcpToolName(activity).server) return false;
	return detail.toolKind === "read" || detail.toolKind === "search" || detail.toolKind === "fetch";
}

const TOOL_KIND_ICONS: Partial<Record<string, StepIcon>> = { read: "read", search: "search", fetch: "web" };

function webSearchStep(query: string | undefined): CommandStep {
	const target = query?.trim() || undefined;
	return target
		? { done: "Searched the web for", live: "Searching the web for", target, icon: "web" }
		: { done: "Searched the web", live: "Searching the web", icon: "web" };
}

function toolSearchStep(query: string | undefined): CommandStep {
	const names = query && /^select:/i.test(query) ? query.slice("select:".length).split(",").map((name) => name.trim()).filter(Boolean) : [];
	if (names.length > 0) return { done: "Loaded tools", live: "Loading tools", target: names.join(", "), icon: "tool" };
	return { done: "Searched tools", live: "Searching tools", target: query || undefined, icon: "tool" };
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Whether a step's past tense only says it was tried ("Ran tests", "Read a.ts"),
 * which stays true when it fails. "Deleted build" does not: beside "failed" it
 * claims something that did not happen.
 */
export function pastTenseHoldsOnFailure(step: Pick<CommandStep, "done">): boolean {
	return /^(?:Ran|Read|Viewed|Checked(?! out)|Listed|Searched|Found|Fetched|Waited|Type-checked|Called)\b/.test(step.done);
}

/** Name one shell command line. Anything unrecognized becomes "Ran <program>". */
export function describeCommandStep(command: string): CommandStep {
	const step = describeKnownCommand(command);
	if (step) return step;
	const binary = mainSegment(command)?.binary;
	if (binary) return { done: "Ran", live: "Running", target: binary, icon: "run" };
	return shellSetupStep(command) ?? GENERIC_STEP;
}

/**
 * A line with no program to name, only shell setup, names that instead: `cd src`,
 * `export CI=1`, `source .env`, `test -f a`.
 */
function shellSetupStep(command: string): CommandStep | undefined {
	for (const line of logicalLines(command)) {
		for (const words of splitSegments(tokenize(line))) {
			const segment = toSegment(words);
			const values = segment ? positionals(segment.args) : [];
			switch (segment?.binary) {
				case "cd":
				case "pushd":
					return { done: "Changed folder", live: "Changing folder", ...(values[0] ? fileTarget(values[0]) : {}), icon: "list" };
				case "popd":
					return { done: "Changed folder", live: "Changing folder", icon: "list" };
				case "export": {
					const names = values.map((value) => value.split("=")[0]!).filter(Boolean);
					return { done: "Set", live: "Setting", target: names.join(", ") || "variables", icon: "tool" };
				}
				case "set":
					return { done: "Set shell options", live: "Setting shell options", icon: "tool" };
				case "source":
				case ".":
					return { done: "Loaded", live: "Loading", ...(values[0] ? fileTarget(values[0]) : { target: "script" }), icon: "read" };
				case "test":
				case "[":
				case "[[":
					return { done: "Checked condition", live: "Checking condition", icon: "check" };
			}
		}
	}
	return undefined;
}

type Segment = { binary: string; binaryPath: string; args: string[]; writeTarget?: string; append?: boolean };

function describeKnownCommand(command: string): CommandStep | undefined {
	const segments = commandSegments(command);
	const segment = workSegment(segments);
	if (!segment) return undefined;
	// `sed -n 1,80p a.ts && sed -n 1,80p b.ts` reads two files, not one.
	if (segments.length > 1 && segments.every(isPlainRead)) {
		return readStep(segments.flatMap((each) => (each.binary === "sed" ? sedFiles(each.args) : readerFiles(each.binary, each.args))));
	}
	return describeSegment(segment, 0);
}

function isPlainRead(segment: Segment): boolean {
	if (segment.writeTarget) return false;
	if (segment.binary === "sed") return !sedInPlace(segment.args);
	return READERS.has(segment.binary);
}

function describeSegment(segment: Segment, depth: number): CommandStep | undefined {
	const { binary, args } = segment;

	// `cat > notes.md`, `echo hi >> log`, `printf … > file`: the command writes a file.
	if (segment.writeTarget && WRITERS.has(binary)) return writeStep(segment.writeTarget, segment.append);
	if (binary === "tee") {
		const file = positionals(args)[0];
		return file ? writeStep(file, args.includes("-a") || args.includes("--append")) : undefined;
	}

	if (isProbe(segment)) {
		const names = positionals(binary === "command" ? args.slice(1) : args);
		return { done: "Checked for", live: "Checking for", target: names.join(", ") || undefined, icon: "check" };
	}

	if (SHELLS.has(binary)) {
		const inline = flagValue(args, ["-c", "-lc", "-ic"]);
		if (inline !== undefined) {
			if (depth > 1) return undefined;
			const inner = mainSegment(inline);
			return inner ? describeSegment(inner, depth + 1) ?? fallbackStep(inner.binary) : undefined;
		}
		const script = positionals(args)[0];
		// `bash -` reads its script from stdin, usually a heredoc: there is no file to name.
		if (script === "-") return fallbackStep(binary);
		return script ? runFileStep(script) : undefined;
	}

	if (binary === "sed") return sedStep(args);
	if (READERS.has(binary)) return readStep(readerFiles(binary, args));
	if (LISTERS.has(binary)) return listStep(positionals(args, LISTER_VALUE_FLAGS[binary])[0]);
	if (SEARCHERS.has(binary)) {
		if (binary === "rg" && args.includes("--files")) return listStep(positionals(args)[0]);
		return searchStep(searchPattern(args));
	}
	if (binary === "find") return findStep(flagValue(args, ["-name", "-iname", "-path", "-ipath", "-regex"]));
	if (binary === "fd" || binary === "fdfind") return findStep(positionals(args)[0]);

	if (binary === "git") return gitStep(args);
	if (binary === "gh") return ghStep(args);

	if (binary === "mkdir") return countedStep(positionals(args), "Created folder", "Creating folder", "folders", "folder-new");
	if (binary === "touch") return countedStep(positionals(args), "Created", "Creating", "files", "file-new");
	if (binary === "rm" || binary === "rmdir" || binary === "unlink" || binary === "trash") {
		return countedStep(positionals(args), "Deleted", "Deleting", "items", "delete");
	}
	if (binary === "mv") return moveStep(positionals(args));
	if (binary === "cp") return copyStep(positionals(args));
	if (binary === "chmod") {
		const file = positionals(args)[1];
		return file
			? { done: "Changed permissions on", live: "Changing permissions on", ...fileTarget(file), icon: "write" }
			: undefined;
	}

	if (FETCHERS.has(binary)) return fetchStep(args);
	if (binary === "sleep") return waitStep(positionals(args)[0]);

	if (PACKAGE_MANAGERS.has(binary)) return packageManagerStep(binary, args);
	if (RUNNERS.has(binary)) {
		const tool = positionals(args)[0];
		if (!tool) return undefined;
		const rest = args.slice(args.indexOf(tool) + 1);
		return toolStep(binaryName(tool), rest) ?? fallbackStep(binaryName(tool));
	}
	if (binary === "go") return goStep(args);
	if (binary === "cargo") return cargoStep(args);
	if (binary === "make" || binary === "just" || binary === "task") {
		const target = taskTarget(binary, args);
		return target ? scriptStep(target, `${binary} ${target}`) : { done: "Ran", live: "Running", target: binary, icon: "run" };
	}
	if (INTERPRETERS.has(binary)) return interpreterStep(binary, args);

	const tool = toolStep(binary, args);
	if (tool) return tool;
	// `./scripts/setup.sh`: a script run by its path names the script.
	if (/^\.{0,2}\//.test(segment.binaryPath) && binary.includes(".")) return runFileStep(segment.binaryPath);
	return undefined;
}

// --- per-command steps -------------------------------------------------------

function readStep(files: string[]): CommandStep {
	if (files.length === 1) return { done: "Read", live: "Reading", ...fileTarget(files[0]!), icon: "read" };
	if (files.length > 1) {
		return {
			done: `Read ${files.length} files`,
			live: `Reading ${files.length} files`,
			target: files.map(fileBasename).join(", "),
			targetTitle: files.join(", "),
			icon: "read",
		};
	}
	return { done: "Read file", live: "Reading file", icon: "read" };
}

function sedInPlace(args: string[]): boolean {
	return args.some((arg) => arg.startsWith("-i") || arg === "--in-place" || arg.startsWith("--in-place="));
}

function sedFiles(args: string[]): string[] {
	// BSD sed takes a bare -i's backup suffix as its own word, `sed -i '' 's/a/b/' a.ts`
	// or `sed -i .bak …`. It is neither the script nor a file. No sed script starts
	// with a dot, so GNU's `sed -i 's/a/b/' a.ts` keeps its script.
	const bareInPlace = args.indexOf("-i");
	const suffix = bareInPlace >= 0 ? args[bareInPlace + 1] : undefined;
	const words = suffix !== undefined && /^(?:\.[\w.-]*)?$/.test(suffix) ? args.filter((_, index) => index !== bareInPlace + 1) : args;
	// Without -e or -f the first positional is the script, not a file.
	const hasScriptFlag = words.some((arg) => arg === "-e" || arg === "-f" || arg === "--expression" || arg === "--file");
	const rest = positionals(words, SED_VALUE_FLAGS);
	return (hasScriptFlag ? rest : rest.slice(1)).filter(looksLikeFile);
}

function sedStep(args: string[]): CommandStep {
	const files = sedFiles(args);
	if (sedInPlace(args)) {
		return files.length === 1
			? { done: "Edited", live: "Editing", ...fileTarget(files[0]!), icon: "write" }
			: { done: files.length ? `Edited ${files.length} files` : "Edited file", live: files.length ? `Editing ${files.length} files` : "Editing file", icon: "write" };
	}
	return readStep(files);
}

function readerFiles(binary: string, args: string[]): string[] {
	let values = positionals(args, READER_VALUE_FLAGS[binary]);
	// jq's first positional is its filter.
	if (binary === "jq") values = values.slice(1);
	return values.filter((value) => looksLikeFile(value) && !/^\d+(?:,\d+)?[a-z]*$/i.test(value));
}

function listStep(path: string | undefined): CommandStep {
	if (!path || path === "." || path === "./") return { done: "Listed files", live: "Listing files", icon: "list" };
	return { done: "Listed", live: "Listing", target: path, icon: "list" };
}

function searchStep(pattern: string | undefined): CommandStep {
	return pattern
		? { done: "Searched for", live: "Searching for", target: pattern, icon: "search" }
		: { done: "Searched files", live: "Searching files", icon: "search" };
}

function findStep(pattern: string | undefined): CommandStep {
	return { done: "Found files", live: "Finding files", target: pattern || undefined, icon: "find" };
}

function writeStep(file: string, append = false): CommandStep {
	return append
		? { done: "Appended to", live: "Appending to", ...fileTarget(file), icon: "write" }
		: { done: "Wrote", live: "Writing", ...fileTarget(file), icon: "write" };
}

function countedStep(
	values: string[],
	done: string,
	live: string,
	plural: string,
	icon: StepIcon,
): CommandStep | undefined {
	if (values.length === 0) return undefined;
	if (values.length === 1) return { done, live, ...fileTarget(values[0]!), icon };
	// "Created folder" → "Created 2 folders"; the verb keeps its first word.
	const verb = (text: string) => text.split(" ")[0]!;
	return {
		done: `${verb(done)} ${values.length} ${plural}`,
		live: `${verb(live)} ${values.length} ${plural}`,
		target: values.map(fileBasename).join(", "),
		targetTitle: values.join(", "),
		icon,
	};
}

function moveStep(values: string[]): CommandStep | undefined {
	if (values.length < 2) return undefined;
	const destination = values.at(-1)!;
	const sources = values.slice(0, -1);
	// Without a trailing slash, `mv notes.md archive` may as well be a move into a
	// folder. Only a file given a new name with an extension, in the same place, is
	// clearly a rename; anything else is a move.
	const renamed =
		sources.length === 1 &&
		dirname(sources[0]!) === dirname(destination) &&
		!destination.endsWith("/") &&
		hasExtension(sources[0]!) &&
		hasExtension(destination);
	if (renamed) {
		return {
			done: "Renamed",
			live: "Renaming",
			target: `${fileBasename(sources[0]!)} → ${fileBasename(destination)}`,
			targetTitle: `${sources[0]} → ${destination}`,
			icon: "move",
		};
	}
	return {
		done: sources.length === 1 ? "Moved" : `Moved ${sources.length} items`,
		live: sources.length === 1 ? "Moving" : `Moving ${sources.length} items`,
		target: sources.length === 1 ? `${fileBasename(sources[0]!)} → ${trimSlash(destination)}` : trimSlash(destination),
		targetTitle: `${sources.join(", ")} → ${destination}`,
		icon: "move",
	};
}

function copyStep(values: string[]): CommandStep | undefined {
	if (values.length < 2) return undefined;
	const destination = values.at(-1)!;
	const sources = values.slice(0, -1);
	return {
		done: sources.length === 1 ? "Copied" : `Copied ${sources.length} items`,
		live: sources.length === 1 ? "Copying" : `Copying ${sources.length} items`,
		target: sources.length === 1 ? `${fileBasename(sources[0]!)} → ${trimSlash(destination)}` : trimSlash(destination),
		targetTitle: `${sources.join(", ")} → ${destination}`,
		icon: "copy",
	};
}

function fetchStep(args: string[]): CommandStep {
	return urlStep(args.find((arg) => /^https?:\/\//i.test(arg)));
}

function urlStep(url: string | undefined): CommandStep {
	let host: string | undefined;
	if (url) {
		try {
			host = new URL(url).host;
		} catch {
			host = undefined;
		}
	}
	return { done: "Fetched", live: "Fetching", target: host ?? "URL", targetTitle: url, icon: "web" };
}

function waitStep(duration: string | undefined): CommandStep {
	if (!duration) return { done: "Waited", live: "Waiting", icon: "wait" };
	const amount = /^\d+(?:\.\d+)?$/.test(duration) ? `${duration}s` : duration;
	return { done: `Waited ${amount}`, live: `Waiting ${amount}`, icon: "wait" };
}

function gitStep(args: string[]): CommandStep {
	const rest = stripGitGlobals(args);
	const sub = rest[0];
	const subArgs = rest.slice(1);
	const target = positionals(subArgs)[0];
	switch (sub) {
		case "status":
			return { done: "Checked git status", live: "Checking git status", icon: "git" };
		case "diff":
			return { done: "Viewed changes", live: "Viewing changes", target: target ? fileBasename(target) : undefined, targetTitle: target, icon: "git" };
		case "show":
			return { done: "Viewed commit", live: "Viewing commit", target, icon: "commit" };
		case "log":
			return { done: "Viewed recent commits", live: "Viewing recent commits", icon: "commit" };
		case "branch":
			if (subArgs.some((arg) => arg === "-d" || arg === "-D" || arg === "--delete")) {
				return { done: "Deleted branch", live: "Deleting branch", target, icon: "git" };
			}
			if (subArgs.includes("--show-current")) {
				return { done: "Checked current branch", live: "Checking current branch", icon: "git" };
			}
			return { done: "Checked branches", live: "Checking branches", icon: "git" };
		case "checkout":
		case "switch": {
			const created = flagValue(subArgs, ["-b", "-B", "-c", "-C", "--create"]);
			if (created) return { done: "Created branch", live: "Creating branch", target: created, icon: "git" };
			// `git checkout .` and `git checkout -- a.ts` put files back; neither switches.
			if (subArgs.includes("--") || target === "." || target?.startsWith("./")) {
				const file = subArgs.includes("--") ? subArgs[subArgs.indexOf("--") + 1] : target;
				if (!file || file === ".") return { done: "Restored files", live: "Restoring files", icon: "git" };
				return { done: "Restored", live: "Restoring", ...fileTarget(file), icon: "git" };
			}
			return { done: "Switched to", live: "Switching to", target, icon: "git" };
		}
		case "add":
			return { done: "Staged changes", live: "Staging changes", icon: "git" };
		case "commit":
			return { done: "Committed changes", live: "Committing changes", icon: "commit" };
		case "push":
			return { done: "Pushed changes", live: "Pushing changes", icon: "git" };
		case "pull":
			return { done: "Pulled changes", live: "Pulling changes", icon: "git" };
		case "fetch":
			return { done: "Fetched updates", live: "Fetching updates", icon: "git" };
		case "merge":
			return resumedStep(subArgs, "merge") ?? { done: "Merged", live: "Merging", target, icon: "git" };
		case "rebase":
			return resumedStep(subArgs, "rebase") ?? { done: "Rebased", live: "Rebasing", target, icon: "git" };
		case "stash":
			return stashStep(subArgs[0]);
		case "restore":
			return { done: "Restored", live: "Restoring", ...(target ? fileTarget(target) : {}), icon: "git" };
		case "reset":
			return { done: "Reset changes", live: "Resetting changes", icon: "git" };
		case "clone":
			return { done: "Cloned", live: "Cloning", target: target ? repoName(target) : undefined, targetTitle: target, icon: "git" };
		case "grep":
			return searchStep(searchPattern(subArgs));
		default:
			// The repository was read in some other way: rev-parse, remote, ls-files…
			return { done: "Checked repository", live: "Checking repository", icon: "git" };
	}
}

/** `git stash` does something different for each action, and only the default stashes. */
function stashStep(action: string | undefined): CommandStep {
	switch (action) {
		case "list":
			return { done: "Listed stashes", live: "Listing stashes", icon: "git" };
		case "show":
			return { done: "Viewed stash", live: "Viewing stash", icon: "git" };
		case "pop":
		case "apply":
			return { done: "Restored stash", live: "Restoring stash", icon: "git" };
		case "drop":
		case "clear":
			return { done: "Dropped stash", live: "Dropping stash", icon: "git" };
		default:
			return { done: "Stashed changes", live: "Stashing changes", icon: "git" };
	}
}

/** `git merge --abort`, `git rebase --continue`: picking up one already under way. */
function resumedStep(args: string[], what: "merge" | "rebase"): CommandStep | undefined {
	if (args.includes("--abort")) return { done: `Aborted ${what}`, live: `Aborting ${what}`, icon: "git" };
	if (args.includes("--continue")) return { done: `Continued ${what}`, live: `Continuing ${what}`, icon: "git" };
	return undefined;
}

function ghStep(args: string[]): CommandStep {
	const values = positionals(args, GH_VALUE_FLAGS);
	const [area, action] = values;
	const number = issueNumber(values[2]);
	const ref = number ? `#${number}` : undefined;
	if (area === "pr") {
		switch (action) {
			case "view":
				return { done: "Read PR", live: "Reading PR", target: ref, icon: "pr" };
			case "diff":
				return { done: "Viewed PR changes", live: "Viewing PR changes", target: ref, icon: "pr" };
			case "list":
			case "status":
				return { done: "Listed pull requests", live: "Listing pull requests", icon: "pr" };
			case "create":
				return { done: "Opened a pull request", live: "Opening a pull request", icon: "pr" };
			case "checks":
				return { done: "Checked CI", live: "Checking CI", target: ref, icon: "check" };
			case "review":
				if (args.includes("--approve") || args.includes("-a")) return { done: "Approved PR", live: "Approving PR", target: ref, icon: "pr" };
				if (args.includes("--request-changes") || args.includes("-r")) {
					return { done: "Requested changes on PR", live: "Requesting changes on PR", target: ref, icon: "pr" };
				}
				return { done: "Commented on PR", live: "Commenting on PR", target: ref, icon: "pr" };
			case "comment":
				return { done: "Commented on PR", live: "Commenting on PR", target: ref, icon: "pr" };
			case "merge":
				return { done: "Merged PR", live: "Merging PR", target: ref, icon: "pr" };
			case "checkout":
				return { done: "Checked out PR", live: "Checking out PR", target: ref, icon: "pr" };
			case "edit":
				return { done: "Edited PR", live: "Editing PR", target: ref, icon: "pr" };
		}
	}
	if (area === "issue") {
		switch (action) {
			case "view":
				return { done: "Read issue", live: "Reading issue", target: ref, icon: "issue" };
			case "list":
				return { done: "Listed issues", live: "Listing issues", icon: "issue" };
			case "create":
				return { done: "Opened an issue", live: "Opening an issue", icon: "issue" };
			case "comment":
				return { done: "Commented on issue", live: "Commenting on issue", target: ref, icon: "issue" };
		}
	}
	if (area === "run") return { done: "Checked CI runs", live: "Checking CI runs", icon: "check" };
	if (area === "api") return { done: "Called the GitHub API", live: "Calling the GitHub API", target: action, icon: "git" };
	if (area === "repo" && action === "view") return { done: "Viewed repository", live: "Viewing repository", icon: "git" };
	return { done: "Ran", live: "Running", target: ["gh", area, action].filter(Boolean).join(" "), icon: "git" };
}

function packageManagerStep(binary: string, args: string[]): CommandStep | undefined {
	const values = positionals(args, PACKAGE_MANAGER_VALUE_FLAGS);
	const sub = values[0];
	if (!sub) {
		// Bare `npm install`-style commands: `pnpm` / `yarn` / `bun` alone installs.
		return binary === "npm" ? undefined : installStep([]);
	}
	if (INSTALL_SUBCOMMANDS.has(sub)) return installStep(sub === "ci" ? [] : values.slice(1));
	if (sub === "test" || sub === "t") return testStep();
	if (sub === "run" || sub === "run-script") {
		const script = values[1];
		return script ? scriptStep(script, script) : undefined;
	}
	if (sub === "exec" || sub === "x" || sub === "dlx") {
		const tool = values[1];
		if (!tool) return undefined;
		const rest = args.slice(args.indexOf(tool) + 1);
		return toolStep(binaryName(tool), rest) ?? fallbackStep(binaryName(tool));
	}
	// `bun file.ts` runs a file; `pnpm build` / `yarn lint` run a script.
	if (binary === "bun" && looksLikeScriptFile(sub)) return runFileStep(sub);
	if (binary !== "npm") return scriptStep(sub, sub);
	return undefined;
}

function installStep(packages: string[]): CommandStep {
	return {
		done: "Installed packages",
		live: "Installing packages",
		target: packages.length ? packages.join(", ") : undefined,
		icon: "install",
	};
}

function testStep(target?: string): CommandStep {
	return { done: "Ran tests", live: "Running tests", target, icon: "test" };
}

/** A named script (`npm run build`, `make lint`): known names get their own words. */
function scriptStep(script: string, label: string): CommandStep {
	const name = script.toLowerCase();
	if (/^(?:test|tests|spec|e2e|vitest|jest)(?:$|[:._-])/.test(name)) return testStep();
	if (/^(?:typecheck|type-check|check-types|types|tsc)(?:$|[:._-])/.test(name)) return typecheckStep();
	if (/^(?:lint|eslint)(?:$|[:._-])/.test(name)) return lintStep();
	if (/^(?:format|fmt|prettier)(?:$|[:._-])/.test(name)) return formatStep();
	if (/^(?:build|compile|bundle)(?:$|[:._-])/.test(name)) return buildStep();
	if (/^(?:dev|start|serve|preview)(?:$|[:._-])/.test(name)) {
		return { done: "Started", live: "Starting", target: label, icon: "run" };
	}
	return { done: "Ran", live: "Running", target: label, icon: "run" };
}

function typecheckStep(): CommandStep {
	return { done: "Type-checked", live: "Type-checking", icon: "check" };
}

function lintStep(): CommandStep {
	return { done: "Ran lint", live: "Running lint", icon: "check" };
}

function formatStep(): CommandStep {
	return { done: "Formatted files", live: "Formatting files", icon: "format" };
}

function buildStep(): CommandStep {
	return { done: "Built project", live: "Building project", icon: "build" };
}

/** The target of `make -C frontend -j 4 test`, past the options and their values. */
function taskTarget(binary: string, args: string[]): string | undefined {
	// make's `-j` and `-l` take a number only when one follows: `make -j 4 test`, `make -j test`.
	const words = binary === "make"
		? args.filter((arg, index) => !(/^\d+$/.test(arg) && (args[index - 1] === "-j" || args[index - 1] === "-l")))
		: args;
	return positionals(words, TASK_RUNNER_VALUE_FLAGS[binary])[0];
}

/** A developer tool run directly or through npx / pnpm dlx. */
function toolStep(tool: string, args: string[]): CommandStep | undefined {
	if (TEST_TOOLS.has(tool)) return testStep();
	if (tool === "playwright") return args[0] === "test" ? testStep() : undefined;
	if (tool === "tsc") return args.some((arg) => arg === "-b" || arg === "--build") ? buildStep() : typecheckStep();
	if (LINT_TOOLS.has(tool)) return lintStep();
	if (tool === "biome") return args[0] === "format" ? formatStep() : lintStep();
	if (tool === "ruff") return args[0] === "format" ? formatStep() : lintStep();
	if (tool === "prettier") return args.includes("--check") || args.includes("-c") ? lintStep() : formatStep();
	if (FORMAT_TOOLS.has(tool)) return formatStep();
	if (BUILD_TOOLS.has(tool)) return buildStep();
	return undefined;
}

function goStep(args: string[]): CommandStep {
	const values = positionals(args);
	switch (values[0]) {
		case "test":
			return testStep();
		case "build":
			return buildStep();
		case "vet":
			return lintStep();
		case "fmt":
			return formatStep();
		case "run": {
			// `go run main.go` runs a file. `go run .` and `go run ./cmd/server` run a
			// package, which only reads as one in full.
			const target = values[1];
			if (target?.endsWith(".go")) return runFileStep(target);
			return { done: "Ran", live: "Running", target: ["go run", target].filter(Boolean).join(" "), icon: "run" };
		}
		case "get":
		case "install":
			return installStep(values.slice(1));
		default:
			return { done: "Ran", live: "Running", target: ["go", values[0]].filter(Boolean).join(" "), icon: "run" };
	}
}

function cargoStep(args: string[]): CommandStep {
	const values = positionals(args);
	switch (values[0]) {
		case "test":
		case "nextest":
			return testStep();
		case "build":
			return buildStep();
		case "check":
			return typecheckStep();
		case "clippy":
			return lintStep();
		case "fmt":
			return formatStep();
		case "add":
		case "install":
			return installStep(values.slice(1));
		default:
			return { done: "Ran", live: "Running", target: ["cargo", values[0]].filter(Boolean).join(" "), icon: "run" };
	}
}

function interpreterStep(binary: string, args: string[]): CommandStep {
	if (binary === "deno") return denoStep(args);
	// `tsx watch src/index.ts` runs the file and reruns it on change.
	if (binary === "tsx" && positionals(args, INTERPRETER_VALUE_FLAGS)[0] === "watch") {
		return interpreterStep(binary, args.filter((arg) => arg !== "watch"));
	}
	const module = flagValue(args, ["-m"]);
	if (module) return TEST_TOOLS.has(module) ? testStep() : { done: "Ran", live: "Running", target: module, icon: "run" };
	// `node -e '…'`, `python -c '…'`: inline code has no file to name.
	if (args.some((arg) => arg === "-e" || arg === "-c" || arg === "--eval" || arg === "-p")) return fallbackStep(binary);
	const script = positionals(args, INTERPRETER_VALUE_FLAGS)[0];
	// `python3 - <<'PY'` reads its code from stdin, like `-c`: no file to name.
	if (!script || script === "-") return fallbackStep(binary);
	if (TEST_TOOLS.has(binaryName(script))) return testStep();
	return runFileStep(script);
}

/** `deno run main.ts`, `deno test`, `deno task dev`: deno takes a subcommand before the script. */
function denoStep(args: string[]): CommandStep {
	const values = positionals(args, INTERPRETER_VALUE_FLAGS);
	const sub = values[0];
	// `deno main.ts` is short for `deno run main.ts`.
	if (sub && looksLikeScriptFile(sub)) return runFileStep(sub);
	switch (sub) {
		case "run":
		case "serve":
			return values[1] ? runFileStep(values[1]) : fallbackStep("deno");
		case "test":
			return testStep();
		case "task":
			return values[1] ? scriptStep(values[1], values[1]) : fallbackStep("deno");
		case "fmt":
			return formatStep();
		case "lint":
			return lintStep();
		case "check":
			return typecheckStep();
		default:
			return { done: "Ran", live: "Running", target: ["deno", sub].filter(Boolean).join(" "), icon: "run" };
	}
}

function runFileStep(file: string): CommandStep {
	return { done: "Ran", live: "Running", ...fileTarget(file), icon: "run" };
}

function fallbackStep(binary: string): CommandStep {
	return { done: "Ran", live: "Running", target: binary, icon: "run" };
}

// --- parsing -----------------------------------------------------------------

/**
 * What grouping reads of a command line: the program its row is named by, past
 * `cd dir &&`, env assignments, wrappers and `command -v` checks, and whether that
 * program writes a file (`cat > a`, `sed -i`), so a row and its header agree.
 */
export function commandIntent(command: string): { program?: string; writes: boolean } {
	const segment = mainSegment(command);
	if (!segment) return { writes: false };
	const writes = segment.writeTarget ? WRITERS.has(segment.binary) : segment.binary === "sed" && sedInPlace(segment.args);
	return { program: segment.binary, writes };
}

/**
 * The segment of a command line that says what it does: past any `cd dir &&`
 * prefix, env assignments and wrappers like `sudo`, and the first stage of a
 * pipeline (`rg foo | head` is a search).
 */
function mainSegment(command: string): Segment | undefined {
	return workSegment(commandSegments(command));
}

/**
 * The segment that names a chain. `git add . && git commit && git push` pushed,
 * and `rm -rf dist && npm run build` built, so the last segment that changes or
 * runs something names it. One that only looks or reports (a trailing `echo done`
 * or `git status`) names it only when nothing else in the chain does anything.
 */
function workSegment(segments: Segment[]): Segment | undefined {
	return segments.findLast((segment) => !onlyObserves(segment)) ?? segments[0];
}

function onlyObserves(segment: Segment): boolean {
	if (segment.writeTarget) return false;
	if (segment.binary === "git") return GIT_OBSERVERS.has(stripGitGlobals(segment.args)[0] ?? "");
	return isPlainRead(segment) || REPORTERS.has(segment.binary) || SEARCH_PROGRAMS.has(segment.binary);
}

/**
 * Every segment that does something, in order, without `cd` and the like. A
 * `command -v gh &&` check in front of the real work is left out too.
 */
function commandSegments(command: string): Segment[] {
	const segments: Segment[] = [];
	for (const line of logicalLines(command)) {
		for (const words of splitSegments(tokenize(line))) {
			const segment = toSegment(words);
			if (segment && !NAVIGATION.has(segment.binary) && !SHELL_SYNTAX.has(segment.binary)) segments.push(segment);
		}
	}
	const work = segments.filter((segment) => !isProbe(segment));
	return work.length > 0 ? work : segments;
}

/** `command -v gh`, `which node`: a check that a program exists. */
function isProbe(segment: Segment): boolean {
	if (segment.binary === "command") return segment.args[0] === "-v" || segment.args[0] === "-V";
	return PROBES.has(segment.binary);
}

const HEREDOC_START = /(?<!<)<<(?!<)-?\s*["']?([\w.-]+)["']?/;

/**
 * The command's lines as the shell reads them. A trailing `\\`, `|` or `&&` carries
 * on to the next line and so does an open quote, comments go, and a heredoc's body
 * is the file's content, not more commands.
 */
function logicalLines(command: string): string[] {
	const lines: string[] = [];
	let line = "";
	let quote: string | undefined;
	let heredoc: string | undefined;
	for (const part of command.trim().split("\n")) {
		if (heredoc !== undefined) {
			if (part.trim() === heredoc) heredoc = undefined;
			continue;
		}
		const scanned = scanLine(part, quote);
		quote = scanned.quote;
		heredoc = HEREDOC_START.exec(scanned.text)?.[1];
		const text = scanned.text.trimEnd();
		if (quote) line += `${scanned.text} `;
		else if (text.endsWith("\\")) line += `${text.slice(0, -1)} `;
		else if (/(?:&&|\|)$/.test(text)) line += `${text} `;
		else {
			line += text;
			if (line.trim()) lines.push(line);
			line = "";
		}
	}
	if (line.trim()) lines.push(line);
	return lines;
}

/** One physical line without its comment, and the quote still open at its end. */
function scanLine(text: string, open: string | undefined): { text: string; quote: string | undefined } {
	let quote = open;
	for (let i = 0; i < text.length; i += 1) {
		const char = text[i]!;
		if (quote === "'") {
			if (char === "'") quote = undefined;
		} else if (char === "\\") {
			i += 1;
		} else if (quote === '"') {
			if (char === '"') quote = undefined;
		} else if (char === '"' || char === "'") {
			quote = char;
		} else if (char === "#" && (i === 0 || /\s/.test(text[i - 1]!))) {
			return { text: text.slice(0, i), quote };
		}
	}
	return { text, quote };
}

type Word = { text: string; operator?: boolean; quoted?: boolean };

const OPERATORS = new Set(["&&", "||", "|", ";", "&", "(", ")"]);

function tokenize(line: string): Word[] {
	const words: Word[] = [];
	// A word runs until whitespace or an operator; quoted text and `$(...)` stay in one
	// word, and `2>&1` and `&>` stay whole so their `&` is not read as "run in the
	// background".
	for (const raw of line.match(/&&|\|\||&>>?|[|;&()]|(?:"(?:\\.|[^"\\])*"|'[^']*'|\$\((?:[^()]|\([^()]*\))*\)|>&\d*|[^\s|;&()"'])+/g) ?? []) {
		if (OPERATORS.has(raw)) words.push({ text: raw, operator: true });
		// A word that opens with a quote is literal text: `">"` is a pattern, not a redirect.
		else words.push({ text: unquote(raw), quoted: /^["']/.test(raw) });
	}
	return words;
}

function splitSegments(words: Word[]): Word[][] {
	const segments: Word[][] = [[]];
	// While in a pipeline: where its first stage is, and the words of the stage after it.
	let pipeline: { first: number; stage: Word[] } | undefined;
	// Later stages of a pipeline only shape the first stage's output, except a `tee`
	// fed by a program that only produces text: `echo hi | tee notes.md` writes the
	// file, and that is the step. `npm test | tee log` still ran the tests.
	const endStage = () => {
		if (!pipeline || toSegment(pipeline.stage)?.binary !== "tee") return;
		const first = toSegment(segments[pipeline.first] ?? []);
		if (first && WRITERS.has(first.binary)) segments[pipeline.first] = pipeline.stage;
	};
	for (const word of words) {
		if (word.operator) {
			if (word.text === "|") {
				endStage();
				pipeline = { first: pipeline?.first ?? segments.length - 1, stage: [] };
			} else if (word.text !== ")") {
				endStage();
				pipeline = undefined;
			}
			segments.push([]);
			continue;
		}
		if (pipeline) pipeline.stage.push(word);
		else segments.at(-1)!.push(word);
	}
	endStage();
	return segments.filter((segment) => segment.length > 0);
}

function toSegment(segmentWords: Word[]): Segment | undefined {
	const words = segmentWords.map((word) => word.text);
	let index = 0;
	while (index < words.length) {
		const word = words[index]!;
		if (/^[A-Za-z_][A-Za-z0-9_]*=/.test(word)) {
			index += 1;
			continue;
		}
		// `if grep -q …; then`, `do cat "$f"`: shell grammar in front of the program.
		if (SHELL_KEYWORDS.has(word)) {
			index += 1;
			continue;
		}
		if (WRAPPERS.has(word)) {
			const valueFlags = WRAPPER_VALUE_FLAGS[word];
			index += 1;
			// `env -i FOO=1 cmd`, `timeout -s KILL 30 cmd`, `sudo -u postgres psql`: skip the
			// wrapper's own flags, the values they take, and a duration.
			while (index < words.length && (words[index]!.startsWith("-") || /^\d+[smh]?$/.test(words[index]!))) {
				index += valueFlags?.has(words[index]!) ? 2 : 1;
			}
			continue;
		}
		break;
	}
	const head = words[index];
	if (!head) return undefined;

	const args: string[] = [];
	let writeTarget: string | undefined;
	let append = false;
	const rest = words.slice(index + 1);
	const quoted = segmentWords.slice(index + 1).map((word) => Boolean(word.quoted));
	for (let i = 0; i < rest.length; i += 1) {
		const word = rest[i]!;
		if (quoted[i]) {
			args.push(word);
			continue;
		}
		// Heredoc markers and input redirects are not arguments.
		if (/^<<-?/.test(word)) {
			if (/^<<-?$/.test(word)) i += 1;
			continue;
		}
		if (word === "<") {
			i += 1;
			continue;
		}
		const redirect = /^(\d*|&)(>>?)(.*)$/.exec(word);
		if (redirect) {
			const target = redirect[3] || rest[i + 1];
			if (!redirect[3]) i += 1;
			// `2>/dev/null` and `2>&1` only route a stream; `> file` writes one.
			if (!redirect[1] && target && !target.startsWith("&") && target !== "/dev/null") {
				writeTarget = target;
				append = redirect[2] === ">>";
			}
			continue;
		}
		args.push(word);
	}
	return { binary: binaryName(head), binaryPath: head, args, writeTarget, append };
}

function unquote(value: string): string {
	return value.replace(/"((?:\\.|[^"\\])*)"|'([^']*)'/g, (_, double: string | undefined, single: string | undefined) =>
		double !== undefined ? double.replace(/\\(.)/g, "$1") : single ?? "",
	);
}

/** Arguments that are not flags, skipping the values of flags known to take one. */
function positionals(args: string[], valueFlags: ReadonlySet<string> = EMPTY): string[] {
	const values: string[] = [];
	for (let i = 0; i < args.length; i += 1) {
		const arg = args[i]!;
		if (arg === "--") {
			values.push(...args.slice(i + 1));
			break;
		}
		if (arg.startsWith("-") && arg.length > 1) {
			if (!arg.includes("=") && valueFlags.has(arg)) i += 1;
			continue;
		}
		values.push(arg);
	}
	return values;
}

/** The value of the first of `flags` present, as `-f value` or `--flag=value`. */
function flagValue(args: string[], flags: string[]): string | undefined {
	for (let i = 0; i < args.length; i += 1) {
		const arg = args[i]!;
		if (flags.includes(arg)) return args[i + 1];
		const eq = arg.indexOf("=");
		if (eq > 0 && flags.includes(arg.slice(0, eq))) return arg.slice(eq + 1);
	}
	return undefined;
}

function searchPattern(args: string[]): string | undefined {
	return flagValue(args, ["-e", "--regexp"]) ?? positionals(args, SEARCH_VALUE_FLAGS)[0];
}

function stripGitGlobals(args: string[]): string[] {
	let i = 0;
	while (i < args.length) {
		const arg = args[i]!;
		if (arg === "-C" || arg === "-c" || arg === "--git-dir" || arg === "--work-tree") {
			i += 2;
			continue;
		}
		if (arg.startsWith("-")) {
			i += 1;
			continue;
		}
		break;
	}
	return args.slice(i);
}

function issueNumber(value: string | undefined): string | undefined {
	if (!value) return undefined;
	return /^#?(\d+)$/.exec(value)?.[1] ?? /\/(?:pull|issues)\/(\d+)/.exec(value)?.[1];
}

function looksLikeFile(value: string): boolean {
	if (!value || /[*?{}]/.test(value) || /^[a-z]+:\/\//i.test(value)) return false;
	return (
		value.startsWith("/") ||
		value.startsWith("./") ||
		value.startsWith("../") ||
		value.startsWith("~/") ||
		value.includes("/") ||
		/^[A-Za-z]:\\/.test(value) ||
		// Any script's letters: `ファイル.md` is as much a file name as `notes.md`.
		/^[\p{L}\p{N}_@.+-]+\.[A-Za-z][\w-]*$/u.test(value) ||
		/^[A-Z][\w-]*$/.test(value)
	);
}

/** `greet.js`, `notes.md`: a name with an extension, which a folder rarely has. */
function hasExtension(path: string): boolean {
	return /[^./]\.[A-Za-z0-9][\w-]*$/.test(fileBasename(path));
}

function looksLikeScriptFile(value: string): boolean {
	return /\.[cm]?[jt]sx?$/.test(value) || value.includes("/");
}

function fileTarget(path: string): { target: string; targetTitle?: string } {
	const cleaned = trimSlash(path);
	const base = fileBasename(cleaned);
	return base === cleaned ? { target: base } : { target: base, targetTitle: cleaned };
}

/**
 * The last part of a path, for a row too narrow for the whole of it; the tooltip
 * carries the rest. Windows paths split on `\` too: `C:\repo\a.ts` → `a.ts`.
 */
export function fileBasename(path: string): string {
	const cleaned = trimSlash(path);
	const slash = lastSeparator(cleaned);
	return slash >= 0 ? cleaned.slice(slash + 1) || cleaned : cleaned;
}

function dirname(path: string): string {
	const cleaned = trimSlash(path);
	const slash = lastSeparator(cleaned);
	return slash >= 0 ? cleaned.slice(0, slash) : "";
}

function lastSeparator(path: string): number {
	return Math.max(path.lastIndexOf("/"), path.lastIndexOf("\\"));
}

function trimSlash(path: string): string {
	return path.length > 1 ? path.replace(/[\\/]+$/, "") : path;
}

function repoName(url: string): string {
	return fileBasename(url.replace(/\.git$/, ""));
}

/** The program's own name, so `/bin/sed` and `sed` read alike. */
export function binaryName(value: string): string {
	const slash = value.lastIndexOf("/");
	return slash >= 0 ? value.slice(slash + 1) : value;
}

const EMPTY: ReadonlySet<string> = new Set();
const NAVIGATION = new Set(["cd", "pushd", "popd", "export", "set", "source", ".", "true", ":"]);
// Shell grammar with no program of its own: a loop's header, a block's end, a test.
const SHELL_SYNTAX = new Set(["for", "case", "select", "function", "fi", "done", "esac", "}", "[", "[[", "test"]);
const SHELL_KEYWORDS = new Set(["if", "then", "else", "elif", "do", "while", "until", "!", "{"]);
const PROBES = new Set(["which", "type", "hash", "whereis"]);
const WRAPPERS = new Set(["sudo", "env", "time", "nohup", "exec", "timeout", "nice"]);
// The wrapper flags that take the next word as their value: `sudo -u postgres`, `timeout -s KILL`.
const WRAPPER_VALUE_FLAGS: Partial<Record<string, ReadonlySet<string>>> = {
	sudo: new Set(["-u", "-g", "-U", "-C", "-D", "-p", "-r", "-t", "-T", "--user", "--group", "--other-user", "--chdir", "--prompt", "--role", "--type"]),
	timeout: new Set(["-s", "-k", "--signal", "--kill-after"]),
	env: new Set(["-u", "-C", "--unset", "--chdir"]),
	nice: new Set(["-n", "--adjustment"]),
	time: new Set(["-f", "-o", "--format", "--output"]),
};
const WRITERS = new Set(["cat", "echo", "printf"]);
// Programs that only report, which never name a chain that does something else.
const REPORTERS = new Set(["echo", "printf", "sleep", "pwd", "date", "clear", "wait"]);
const GIT_OBSERVERS = new Set(["status", "log", "diff", "show"]);
const SHELLS = new Set(["bash", "sh", "zsh", "dash", "fish"]);
const READERS = new Set(["cat", "head", "tail", "nl", "bat", "less", "more", "wc", "jq"]);
const LISTERS = new Set(["ls", "tree", "eza", "exa", "lsd"]);
const SEARCHERS = new Set(["rg", "grep", "egrep", "fgrep", "ag", "ack"]);
const FETCHERS = new Set(["curl", "wget", "http", "https", "xh"]);
const PACKAGE_MANAGERS = new Set(["npm", "pnpm", "yarn", "bun"]);
const RUNNERS = new Set(["npx", "bunx", "pnpx"]);
const INTERPRETERS = new Set(["node", "python", "python3", "deno", "ruby", "perl", "php", "tsx", "ts-node"]);
const INSTALL_SUBCOMMANDS = new Set(["install", "i", "ci", "add"]);
const TEST_TOOLS = new Set(["vitest", "jest", "mocha", "pytest", "ava", "tap", "jasmine", "karma"]);
const LINT_TOOLS = new Set(["eslint", "oxlint", "golangci-lint", "stylelint", "shellcheck", "tslint", "flake8", "pylint", "mypy"]);
const FORMAT_TOOLS = new Set(["gofmt", "goimports", "black", "rustfmt", "dprint"]);
const BUILD_TOOLS = new Set(["vite", "webpack", "esbuild", "rollup", "tsup", "turbo"]);
// Flags that take the next word as their value, per reader: `head -n 40 a.ts`,
// `bat -r 1:40 a.ts`. Elsewhere the same letters are switches: `cat -n`, `wc -c`, `jq -r`.
const HEAD_TAIL_VALUE_FLAGS = new Set(["-n", "-c", "--lines", "--bytes"]);
const READER_VALUE_FLAGS: Partial<Record<string, ReadonlySet<string>>> = {
	head: HEAD_TAIL_VALUE_FLAGS,
	tail: HEAD_TAIL_VALUE_FLAGS,
	bat: new Set(["-r"]),
};
// `tree -L 2 src`, `eza -L 2`: a depth or a pattern, not the folder.
const LISTER_VALUE_FLAGS: Partial<Record<string, ReadonlySet<string>>> = {
	ls: new Set(["-I", "--ignore", "-w", "--width"]),
	tree: new Set(["-L", "-I", "-P", "-o", "--filelimit"]),
	eza: new Set(["-L", "--level", "-I", "--ignore-glob"]),
	exa: new Set(["-L", "--level", "-I", "--ignore-glob"]),
	lsd: new Set(["--depth", "-I", "--ignore-glob"]),
};
// `make -C frontend test`, `just -f ci.just lint`: a folder or a file, not the target.
const TASK_RUNNER_VALUE_FLAGS: Partial<Record<string, ReadonlySet<string>>> = {
	make: new Set(["-C", "-f", "-I", "-o", "-W", "--directory", "--file", "--makefile", "--include-dir"]),
	just: new Set(["-f", "-d", "--justfile", "--working-directory"]),
	task: new Set(["-d", "-t", "--dir", "--taskfile"]),
};
/**
 * What grouping counts as exploring, from the same lists the rows are named by:
 * programs that read files, and those that search for or list them. `glob` is how
 * some agents report a file search.
 */
export const READ_PROGRAMS: ReadonlySet<string> = new Set([...READERS, "sed"]);
export const SEARCH_PROGRAMS: ReadonlySet<string> = new Set([...SEARCHERS, ...LISTERS, "find", "fd", "fdfind", "glob"]);
const SED_VALUE_FLAGS = new Set(["-e", "-f", "--expression", "--file", "-l"]);
const SEARCH_VALUE_FLAGS = new Set([
	"-e", "--regexp", "-g", "--glob", "-t", "--type", "-T", "--type-not", "-A", "-B", "-C",
	"--after-context", "--before-context", "--context", "-m", "--max-count", "-f", "--file",
	"-M", "--max-columns", "-j", "--threads", "--max-depth", "-d", "--include", "--exclude",
	"--exclude-dir", "--type-add", "--sort", "--sortr",
]);
const GH_VALUE_FLAGS = new Set(["-R", "--repo", "--json", "-q", "--jq", "-t", "--template", "-L", "--limit", "-s", "--state", "-A", "--author", "-B", "--base", "-H", "--head", "-b", "--body", "--title", "-l", "--label", "-X", "--method", "-f", "--raw-field", "-F", "--field", "--input"]);
const PACKAGE_MANAGER_VALUE_FLAGS = new Set(["--filter", "-F", "-C", "--dir", "--prefix", "--workspace", "--cwd"]);
const INTERPRETER_VALUE_FLAGS = new Set(["-r", "--require", "--import", "--loader", "-W", "-X"]);
