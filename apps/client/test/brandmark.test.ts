import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";

import { BRAND_WORDMARK_SVG } from "../src/main/brandmark";

// ⛔ THE ONE THING THAT MAKES A DUPLICATED ASSET ACCEPTABLE.
//
// The first-run screen is a data: URL built before any bundle exists, so it cannot import from
// apps/web/src/assets. The mark is therefore copied into brandmark.ts — and a copied asset that
// nothing compares is a second logo that will differ from the first on the day someone re-exports
// one of them, in a screen nobody looks at twice.
test("the embedded mark is byte-identical to the asset it was copied from", () => {
  const asset = readFileSync(
    join(__dirname, "..", "..", "web", "src", "assets", "tunnex-wordmark.svg"),
    "utf8",
  ).trim();
  assert.equal(
    BRAND_WORDMARK_SVG,
    asset,
    "apps/client/src/main/brandmark.ts has drifted from apps/web/src/assets/tunnex-wordmark.svg — " +
      "re-copy it rather than editing one of the two.",
  );
});
