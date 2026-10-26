/**
 * KaTeX with what it needs on the page, as one lazily imported chunk: its
 * stylesheet (and fonts), and `copy-tex`, which makes copying a selection
 * yield a formula's TeX rather than its glyphs. Import this module only
 * through `import()`, so none of it loads until a formula renders.
 */

import "katex/dist/katex.min.css";
import "katex/contrib/copy-tex";

export { default } from "katex";
