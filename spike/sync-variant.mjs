// Regenerates spike/extension-omnibox/ from spike/extension/.
// The variant is a full copy whose manifest differs in two ways: its name, and
// an added omnibox keyword "go/" (check 15 of the Chrome spike checklist).
//
// Usage: node spike/sync-variant.mjs   (works from any directory)

import { cpSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const spikeDir = import.meta.dirname;
const source = join(spikeDir, "extension");
const target = join(spikeDir, "extension-omnibox");

// Start from nothing so files deleted from the source do not linger.
rmSync(target, { recursive: true, force: true });
cpSync(source, target, { recursive: true });

const manifestPath = join(target, "manifest.json");
const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
manifest.name = "godir spike (omnibox)";
manifest.omnibox = { keyword: "go/" };
writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + "\n");

console.log(`wrote ${target}`);
for (const name of readdirSync(target).sort()) {
  console.log(`  ${name}`);
}
