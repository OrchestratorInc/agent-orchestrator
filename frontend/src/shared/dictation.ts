export type DictationEvent =
	| { type: "listening" }
	| { type: "level"; value: number }
	| { type: "text"; text: string }
	| { type: "end"; text: string }
	| { type: "error"; message: string }
	| { type: "exit" };
