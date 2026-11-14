export type DictationEvent =
	| { type: "listening" }
	| { type: "levels"; values: number[] }
	| { type: "text"; text: string }
	| { type: "finishing" }
	| { type: "end"; text: string }
	| { type: "error"; message: string }
	| { type: "exit" };
