# ao theme

Restyle the Orchestrator desktop app when the user asks for it ("make the
sidebar Slack purple", "use this theme file", "tone down the borders",
"switch to Nord, dark"). You make or import a **custom theme**: it appears in
Settings > General > Appearance next to the built-in themes, and every open
window can switch to it at once. No reload is needed.

```bash
ao theme                                   # custom and built-in themes
ao theme set sumi --name "Sumi" --dark canvas=#101012 --dark sidebar=#3f0e40 --use
ao theme set sumi --dark accent=#e0a84f    # change part, keep the rest
ao theme set sumi --dark sidebar=          # an empty value removes the role
ao theme show sumi > "$TMPDIR/sumi.json"   # the theme's file, to edit or share
ao theme import "$TMPDIR/sumi.json" --use  # add or replace from a file
ao theme use nord --mode dark              # switch to any theme (and appearance)
ao theme mode light                        # light, dark or system
ao theme delete sumi
```

## The theme file

A theme is one JSON file. People share these; `ao theme import` takes one as
it is.

```json
{
  "version": 1,
  "name": "Sumi",
  "appearance": "dark",
  "colors": { "canvas": "#101012", "sidebar": "#3f0e40", "accent": "#e0a84f" },
  "variants": { "light": { "canvas": "#fafafa", "sidebar": "#f4edf4" } },
  "css": "[data-workspace-area=\"composer\"] { border-radius: 4px; }"
}
```

- **`appearance`** is the one `colors` paints: `dark` or `light`.
- **`variants`** holds the other appearance's colors. Without it the theme has
  one appearance: picking it switches the app to that appearance, and the
  other keeps Orchestrator's default look.
- **`id`** is optional; it comes from the name ("One Pro Night" gives
  `one-pro-night`). Importing a file whose id exists replaces that theme.
- A color is any literal CSS color: `#101012`, `rgb(16 16 18)`,
  `oklch(0.2 0.01 250)`. No `var()` or `color-mix()`.
- `colors` is not inherited by `variants`: set each appearance's roles.

`ao theme set <id>` edits the same file for you: `--dark role=color`,
`--light role=color`, `--set role=color` (both), `--name`, `--css <file>`,
`--clear-css`. It makes the theme if the id is new. Prefer it for follow-ups
("a bit darker"), so earlier changes survive. Add `--use` to switch every
window to the theme, and `--mode dark|light|system` to switch the appearance.

## Colors

Every role is optional. Set the few the request needs: a role you leave out
follows the ones you set (the sidebar and cards follow `canvas`, borders follow
`text`), and otherwise keeps Orchestrator's default look.

| Role | Paints |
|---|---|
| `canvas` | The center pane: chat, board, settings |
| `text`, `textMuted` | Primary and secondary text (keep `textMuted` readable on `canvas`) |
| `sidebar` | The window frame: the left sidebar, the top band with tabs and window controls, and the gap around the center pane |
| `sidebarForeground`, `sidebarMutedForeground` | Text on the frame |
| `sidebarRowHover`, `sidebarRowSelected` | The hovered and the selected sidebar row |
| `sidebarControlSurface`, `sidebarBorder` | Buttons, the search field and lines on the frame |
| `surface` | Cards and grouped content |
| `surfaceRaised` | Raised fills inside cards |
| `surfaceOverlay` | Menus, popovers and hover cards |
| `secondary`, `muted` | Hover, selected and input fills |
| `border`, `input` | Hairlines; input outlines and the center pane's edge |
| `accent`, `accentForeground` | Primary button fill and its label |
| `link` | Links, inline code and the brand mark (defaults to a readable `accent`) |
| `focus` | Focus ring |
| `messageSurface`, `messageForeground` | Your chat bubble |
| `codeBackground`, `codeForeground` | Code blocks in chat |
| `placeholder` | Placeholder text in inputs |
| `error`, `warning` | Destructive actions; needs-attention status |
| `red` `orange` `yellow` `green` `blue` `purple` `cyan` | Status colors and the terminal's ANSI colors |
| `terminalBackground`, `terminalForeground`, `terminalCursor`, `terminalSelection` | The terminal, when it should differ from `canvas` and `text` |

Text on the frame takes care of itself: with a dark `sidebar` in a light theme
(or the reverse), frame text switches to a readable color unless you set
`sidebarForeground`. So a Slack-style look is one role per appearance:

```bash
ao theme set slack --name "Slack" --set sidebar=#3f0e40 --set sidebarRowSelected=#1164a3 --use
```

A shared file may set other roles (`chrome`, `toolbar*`, `update*`,
`searchMatch*`, `messageAction*` and a few more). They are accepted and kept
in the file, but paint nothing here; `chrome` stands in for `sidebar` when a
file sets no `sidebar`. `ao theme` says so when a file has such roles, and
leaves out any name that is not a role, with a warning. Read those notes: they
are why a color did not change.

## CSS

`css` applies while the theme shows, in both appearances. Use it for what
colors cannot do: fonts, radius, spacing, a background image. Scope a rule to
one appearance with `:root[data-theme="dark"]` or `:root[data-theme="light"]`.
`ao theme set --css <file>` replaces the theme's whole CSS, so start from the
`css` in `ao theme show <id>`.

Select stable hooks, not generated class names (they change with app updates):

| Selector | Part |
|---|---|
| `[data-workspace-area="sidebar"]`, `[data-slot="sidebar-inner"]` | Sidebar |
| `[data-slot="sidebar-menu-button"]` | Sidebar row |
| `[data-workspace-area="conversation"]` | Chat area, behind the messages and the composer |
| `.messages-timeline-scroll` | Chat scroller |
| `[data-message-role="user"]`, `[data-message-role="assistant"]` | A message row |
| `[data-user-message-bubble]` | The user's message bubble |
| `.chat-markdown` | Assistant text |
| `.chat-markdown-codeblock` | Code block |
| `[data-chat-composer-form]` | The strip that holds the composer |
| `[data-slot="composer-host"]`, `[data-workspace-area="composer"]` | Composer card |
| `[data-workspace-area="inspector"]` | Right inspector |
| `[data-slot="card"]`, `[data-slot="input"]`, `[data-slot="switch"]`, `[data-slot="dialog-content"]`, `[data-slot="tooltip-content"]`, `[data-slot="hover-card-content"]`, `[data-slot="command"]`, `[data-slot="select-content"]`, `[data-slot="table"]` | UI primitives |

In a theme's CSS, `.dark` and `:root:not(.dark)` are read as this app's dark
and light, so a shared file that scopes its rules that way applies as written.

State: `[data-state="open"]`, `[data-active="true"]`. Always give the value:
inactive items carry `data-active="false"`, so a bare `[data-active]` matches
every item.

Some app rules are more specific than a plain selector; add `!important` to a
declaration when one does not take.

Tokens to set on `:root`:

| Token | Controls | Default |
|---|---|---|
| `--radius` | Base corner radius; buttons, cards, menus, the message bubble and the composer all follow it | `0.625rem` |
| `--font-family-base`, `--font-family-mono` | UI and code fonts | Geist, Geist Mono |
| `--size-center-panel-inset-mac` | Gap around the center pane on macOS | `6px` |
| `--radius-center-panel` | Center pane corners | `12px` |
| `--radius-chat-bubble` | The user's message bubble corners, when they should differ | `--radius` |
| `--radius-chat-composer` | Composer corners, when they should differ | twice `--radius` |

```css
:root { --font-family-base: "Avenir Next", sans-serif; --radius: 0.25rem; }
```

## Fonts, images and backgrounds

A theme never loads a file from the network or disk: `@import`, `image-set()`
and any `url()` that is not a `data:` URL are refused, so a shared theme
cannot send what is on screen to another host.

- A font installed on the user's computer needs no file: name it in
  `--font-family-base`.
- Otherwise embed the file as a `data:` URL in the CSS. Build it with a
  command, not by hand:

  ```bash
  printf '[data-workspace-area="conversation"] { background: url(data:image/webp;base64,%s) center / cover no-repeat !important; }\n' \
    "$(base64 < wall.webp | tr -d '\n')" > "$TMPDIR/sumi.css"
  ao theme set sumi --css "$TMPDIR/sumi.css"
  ```

**A background behind the chat** goes on `[data-workspace-area="conversation"]`.
It can be a drawn, animated scene with no image file: an inline SVG whose
`<style>` holds `@keyframes`.

```css
[data-workspace-area="conversation"] {
  background: linear-gradient(rgba(9,6,15,.5), rgba(9,6,15,.6)),
    url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 320 180' preserveAspectRatio='xMidYMid slice'><style>.s{animation:t 2s infinite}@keyframes t{50%25{opacity:.1}}</style><rect width='320' height='180' fill='%23040208'/><rect class='s' x='40' y='30' width='1' height='1' fill='%23fff'/></svg>")
    center bottom / cover no-repeat !important;
}
[data-user-message-bubble], [data-slot="composer-host"] { background: rgba(20,14,34,.82) !important; backdrop-filter: blur(6px); }
.chat-markdown { text-shadow: 0 1px 2px rgba(0,0,0,.9); }
```

Inside the SVG: use single quotes, write `#` as `%23` and `%` as `%25`, and
write a paint reference `url(#id)` as `ur%6C(%23id)` (a plain `url(` there is
refused as a file load). Keep a dark veil (the gradient above) over the scene
and give bubbles and the composer a translucent fill so text stays readable.
Add `@media (prefers-reduced-motion:reduce){*{animation-play-state:paused!important}}`
to the SVG's style. An animated scene repaints the chat area all the time;
keep it small and slow.

**Artwork the user asks for by name** (a game, a film, a band). A theme is a
personal look for the user's own app on their own computer, like a desktop
wallpaper; Orchestrator does not publish it. So when the user asks for such
artwork, help them get it on screen:

- If they give you an image (a path, or a file dropped into the chat), embed
  it. That is their copy to use.
- Otherwise ask for one, or point to where the publisher offers wallpapers,
  and embed the file once they have it. Resize it first if it is large
  (`sips -Z 1600 in.jpg --out out.jpg` on macOS).
- Note the source in a CSS comment. Mention once that sharing the theme file
  shares the image inside it too.

A theme's CSS is capped at 2 MiB; prefer one variable font and small or
compressed images.

With window translucency on (macOS), the frame is a tint of `sidebar` over the
blurred desktop and the center pane stays solid. Terminal colors reach
terminals opened afterwards.

## Rules

- Change only what the user asked for. Make small changes and ask how it
  looks: you cannot see the result.
- Set both appearances when the request does not name one, or the theme will
  only show in the one you set.
- In your reply, name the theme and what changed, with old and new values
  where you know them ("sumi: dark sidebar #111212 → #3f0e40").
- To undo, switch back (`ao theme use orchestrate`) or delete the theme. The
  user can also pick another theme, export or delete this one in Settings >
  General > Appearance. If a theme's CSS hid or broke the interface, the menu
  bar's View > Use Default Theme (macOS) still works, and so does
  `ao theme use orchestrate` from any terminal.
