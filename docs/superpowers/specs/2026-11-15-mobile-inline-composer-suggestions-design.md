# Mobile: inline `/` and `@` suggestions above the composer

## Intent

Typing `/` in a mobile chat session opened a route sheet (`ComposerPickerSheet`)
that covered the composer and had its own search field. The user loses the
keyboard context and types the query twice. Show the matches inline, directly
above the composer, the way chat apps (and the AO desktop composer's
`ComposerSuggestMenu`) do: the keyboard stays up and the text after the sigil is
the filter.

## Goals

- `/` at the start of a message and `@` after whitespace show an inline panel
  docked above the composer, inside the keyboard-riding dock.
- Typing keeps filtering live; tapping a row inserts it and closes the panel;
  a space, deleting the sigil, or moving the caret off the token closes it.
- Rows: skill label + one-line description + source badge; files: basename +
  parent path.
- Loading and empty states are explicit.

## Non-goals

- A "Frequently used" section or category tabs (skills have no categories).
- Keyboard (hardware) arrow navigation.

## Anti-goals

- Stealing focus from the TextInput or dismissing the keyboard.
- Layout shift of the timeline beyond the dock growing upward.

## Approaches

1. **Inline panel inside the composer dock (chosen).** Rides the keyboard with
   the composer for free, reuses the existing ranking (`rankComposerCatalog`).
2. Keep the sheet but at a small detent. Still takes focus from the input.
3. Absolute-positioned overlay over the timeline. Needs manual keyboard math
   the dock already does.

## Design

- New `ComposerSuggestionPanel` (presentation only) renders ranked rows in one
  elevated surface with hairline dividers, max ~3.5 rows tall, scrollable,
  `keyboardShouldPersistTaps="handled"`.
- `ChatComposer` derives the active trigger from `text` + `cursor`, loads the
  catalog once per trigger kind (via `onLoadSkills` / `onLoadFiles`), and renders
  the panel above the composer row. Selecting calls `replaceComposerSuggestion`.
- The `composer-picker` sheet route and `ComposerPickerSheet` are removed (no
  other entry point opens them).

## Left to the builder

Exact row height, panel max height, and empty/loading copy.
