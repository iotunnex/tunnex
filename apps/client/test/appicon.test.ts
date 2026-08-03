import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const ROOT = join(__dirname, "..", "..", "..");
const ICON = join(__dirname, "..", "build", "icon.png");

// ⛔ THE APP SHIPPED WITH ELECTRON'S OWN ICON, AND THE FIX HAD A SECOND HALF THAT NEARLY GOT LOST.
//
// Generating the icon was the easy part. `.gitignore` carries an UNANCHORED `build/`, which matches
// `apps/client/build/` — the electron-builder buildResources directory, full of AUTHORED files. The
// four already in git are there only because somebody ran `git add -f`. The icon would have been
// the fifth: correct on the machine that generated it, absent on every fresh clone, and the
// packaged app would have gone back to Electron's atom with nothing failing.
//
// > **THIS REPO HAS ALREADY PAID FOR THIS EXACT PATTERN** — an unanchored `secrets/` kept
// > `apps/api/internal/secrets` SOURCE out of git, building locally and breaking every clone.

test("the app icon exists and is a square PNG big enough for every target", () => {
  assert.ok(existsSync(ICON), "apps/client/build/icon.png is missing — run `pnpm --filter @tunnex/client icon`");
  const buf = readFileSync(ICON);
  // PNG signature, then IHDR: width and height are big-endian u32 at offsets 16 and 20.
  assert.equal(buf.subarray(1, 4).toString("ascii"), "PNG");
  const w = buf.readUInt32BE(16);
  const h = buf.readUInt32BE(20);
  assert.equal(w, h, `the icon is ${w}x${h} — a non-square icon is stretched by every OS that shows it`);
  // electron-builder derives .icns and .ico from this one file; below 512 they come out blurred.
  assert.ok(w >= 512, `the icon is ${w}px — too small to generate .icns/.ico cleanly`);
});

test("⛔ the icon is not swallowed by the unanchored `build/` ignore rule", () => {
  // ⚠ `git check-ignore` IS THE WRONG INSTRUMENT AND IT ANSWERED "IGNORED" FOR A FILE THAT IS NOT.
  // Its exit status is 0 when ANY pattern matches — including a NEGATION — so an un-ignored file
  // reports the same status as an ignored one. The question is not "does a rule match" but "would
  // git add this", and only `ls-files --others --exclude-standard` answers that.
  const tracked = execFileSync("git", ["ls-files", "apps/client/build/icon.png"], {
    cwd: ROOT,
    encoding: "utf8",
  }).trim();
  const addable = execFileSync(
    "git",
    ["ls-files", "--others", "--exclude-standard", "apps/client/build/icon.png"],
    { cwd: ROOT, encoding: "utf8" },
  ).trim();
  assert.ok(
    tracked !== "" || addable !== "",
    "apps/client/build/icon.png is neither tracked nor addable — .gitignore is swallowing it, and " +
      "the packaged app will fall back to Electron's icon on a fresh clone",
  );
});

test("the generated helper binary stays OUT of git", () => {
  // The same directory holds a real build artefact. Un-ignoring the directory must not drag it in.
  const addable = execFileSync(
    "git",
    ["ls-files", "--others", "--exclude-standard", "apps/client/build/helper/"],
    { cwd: ROOT, encoding: "utf8" },
  ).trim();
  assert.equal(addable, "", "the staged helper binary became addable — it is a build output");
});
