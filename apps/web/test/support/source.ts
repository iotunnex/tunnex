// Reading source in a test: the shared strippers.
//
// ⛔ A CENSUS OVER SOURCE STRIPS COMMENTS FIRST — AS THE STARTING SHAPE, NOT AS A FIX EACH TIME IT
// BITES. Ruled S14.20 after the THIRD instance in one epic of prose satisfying a search for code:
//
//   1. `placeholderglyph` banned an em-dash as a rendered value and caught its own explanatory
//      comment about em-dashes.
//   2. A `@ts-expect-error` written INSIDE a comment to describe one became a live directive.
//   3. The `client.html` entry check passed with the flip REVERTED, because the comment explaining
//      the flip contains "client.html".
//
// Every one was found by hand, after the fact, in a check that had already been reported green.
//
// > **A COMMENT IS THE MOST LIKELY PLACE IN A FILE FOR THE EXACT STRING A CENSUS HUNTS FOR** — the
// > code does the thing once, and the prose ABOUT the thing quotes it, explains it, and names the
// > alternative it rejected. A search over raw source is therefore biased toward finding the
// > explanation rather than the implementation, and the better-documented the file, the more
// > reliably the census lies.
//
// The direction of the lie is what makes it dangerous: it is FALSE GREEN. The census reports the
// thing present when only its description is present, so the failure is silent forever.
//
// ⚠ THE STRIPPER MUST MATCH THE LANGUAGE. There is no universal comment syntax, and using the
// JavaScript one on a YAML file strips nothing while looking like it worked — which is a green
// check that never ran. Hence one function per syntax, chosen at the call site.

/**
 * TS / TSX / JS.
 *
 * ⚠ `//` is stripped only at the START of a line. A mid-line strip would eat `"https://…"` out of
 * every URL string in the tree and turn real code invisible — trading a false green for a false
 * red. Trailing comments after code survive; they are rare and they sit next to the code that
 * would satisfy the search anyway.
 */
export function stripJsComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/^\s*\/\/.*$/gm, " ");
}

/** CSS has ONLY block comments — `//` in a stylesheet is not a comment and must not be stripped. */
export function stripCssComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, " ");
}

/** SVG / HTML / XML. */
export function stripXmlComments(src: string): string {
  return src.replace(/<!--[\s\S]*?-->/g, " ");
}

/**
 * YAML — line-start `#` only, for the same reason as `//` above: a `#` mid-line is a fragment in a
 * URL, an anchor, or a colour far more often than it is a comment we need gone.
 */
export function stripYamlComments(src: string): string {
  return src.replace(/^\s*#.*$/gm, " ");
}
