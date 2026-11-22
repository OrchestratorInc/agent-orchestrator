// Error text can contain arbitrary application data. Rebuild known error
// formats from fixed text and bounded arguments before sending or grouping.
export function scrubSentryMessage(value: unknown): string {
	if (typeof value !== "string" || value.length > 4096 || /[\r\n]/.test(value)) return "";
	const message = value.trim().replace(/^(?:Uncaught(?: \(in promise\))? )?(?:TypeError|ReferenceError|SyntaxError|RangeError|Error): /, "");
	const property = /^Cannot (read|set) properties of (undefined|null) \((reading|setting) '[^']*'\)$/.exec(message);
	if (property) {
		const key = /'(id|length|name|map|value)'\)$/.exec(message)?.[1];
		return `Cannot ${property[1]} properties of ${property[2]} (${property[3]} '${key ?? "[redacted]"}')`;
	}
	const system = /^(ENOENT: no such file or directory|EACCES: permission denied|EPERM: operation not permitted|EISDIR: illegal operation on a directory|ENOTDIR: not a directory), (open|stat|lstat|scandir|readlink|unlink|mkdir|rename)(?: .+)?$/.exec(message);
	if (system) return `${system[1]}, ${system[2]} [redacted]`;
	const network = /^(Failed to fetch(?! dynamically imported module)|Network request failed|Load failed|Connection refused)(?:[: .].*)?$/.exec(message);
	if (network) return network[1];
	const http = /^(?:HTTP (?:error )?|Request failed with status code )([45]\d\d)(?:\b.*)?$/.exec(message);
	if (http) return `HTTP error ${http[1]}`;
	const chunk = /^(Failed to fetch dynamically imported module|Importing a module script failed|Loading chunk [\w-]+ failed)(?:[: .].*)?$/.exec(message);
	if (chunk) return chunk[1].startsWith("Loading chunk ") ? "Loading chunk failed" : chunk[1];
	if (/^.{1,256} is not a (?:function|constructor)$/.test(message)) {
		return message.endsWith("function") ? "Value is not a function" : "Value is not a constructor";
	}
	if (/^.{1,256} is not defined$/.test(message)) return "Reference is not defined";
	const syntax = /^Unexpected (token|identifier)\b/.exec(message);
	if (syntax) return `Unexpected ${syntax[1]}`;
	if (/^(?:JSON Parse error: )?Unexpected end of (?:JSON )?input$/.test(message)) return message;
	return /^(?:Maximum call stack size exceeded|Invalid array length|Invalid time value|Cannot convert undefined or null to object|Renderer script load failed)$/.test(message) ? message : "";
}

export function sentryMessageFingerprint(message: string): string[] {
	if (!message) return [];
	return [message.toLowerCase()
		.replace(/'[^']*'/g, "'?'")
		.replace(/\b(?:[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}|(?:0x)?[a-f0-9]{8,})\b/gi, "?")
		.replace(/\d+/g, "?")];
}

export function sentryExceptionValue(message: string, title: string, location: string, label: string): string {
	if (!message) return title.slice(0, 200);
	const suffix = ` (in ${location}, ${label})`.slice(0, 199);
	return message.slice(0, 200 - suffix.length) + suffix;
}

const SAFE_EXCEPTION_TYPES = new Set([
	"Error", "EvalError", "RangeError", "ReferenceError", "SyntaxError", "TypeError", "URIError", "AggregateError", "DOMException",
	"ApiError", "ArtifactTextTooLargeError", "ArtifactTooLargeError", "AuthStoreUnreadableError",
	"BrowserProfileStoreError", "CloudAuthError", "CloudCpAuthError", "CloudCpError", "CloudError",
	"CloudShareError", "EndpointRefreshError", "FolderPushError", "GitHubDaemonError",
	"IncompatibleHostVersionError", "IncompatibleRemoteVersionError", "LocalAuthError",
	"OrchestratorSpawnError", "TaskCreateError", "WorkspaceRequestError",
]);

export function safeSentryExceptionType(value: unknown): string {
	return typeof value === "string" && SAFE_EXCEPTION_TYPES.has(value) ? value : "Error";
}
