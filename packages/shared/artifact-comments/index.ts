// Shared contract for comments on shared artifact links: wire types, element
// grouping, the comment-bridge protocol and the Send to agent message.
// types.ts, groups.ts and bridge-protocol.ts import nothing outside this
// folder, so the web viewer (ao-landing) can vendor them as they are.
export * from "./types";
export * from "./bridge-protocol";
export * from "./groups";
export * from "./agent-message";
