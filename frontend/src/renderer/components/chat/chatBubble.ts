/**
 * The user's message bubble shape (the chat's own radius and leading tokens).
 * A tagged message's dashed box and a mention's focused steer draw with it
 * too, so they read as the same shape as an ordinary message. `chat-bubble`
 * gives them the bubble's text size, line height and (Interface v2) padding in
 * the timeline, which styles.css sets for the bubble (`.cursor-chat-human-message`).
 */
export const CHAT_BUBBLE_SHAPE = "chat-bubble rounded-lg px-3 py-2.5 text-sm leading-body-md";
