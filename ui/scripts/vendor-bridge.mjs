// Replace the vendored @latticenet/plugin-bridge build with a pack of a bridge
// checkout, and record which commit it came from.
//
//   node scripts/vendor-bridge.mjs /path/to/lattice-plugin-bridge
//
// The checkout must be clean: a tarball packed from uncommitted work has no
// commit to name. `npm pack` is reproducible for a given tree (fixed mtimes,
// sorted entries), so the sha256 written here identifies the source commit as
// well as the file, and vendorBridge.test.ts holds the two together.
//
// yagni: this exists only until the bridge publishes the chassis; see
// vendor/plugin-bridge.lock.json "retire".
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFileSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const checkout = process.argv[2];
if (!checkout) {
  console.error("usage: node scripts/vendor-bridge.mjs <path to a lattice-plugin-bridge checkout>");
  process.exit(2);
}

const git = (...args) => execFileSync("git", ["-C", checkout, ...args], { encoding: "utf8" }).trim();
if (git("status", "--porcelain")) {
  console.error(`${checkout} has uncommitted changes; commit or discard them first so the tarball names a commit`);
  process.exit(1);
}
const commit = git("rev-parse", "HEAD");
const branch = git("rev-parse", "--abbrev-ref", "HEAD");
const repo = git("remote", "get-url", "origin");

execFileSync("npm", ["run", "build"], { cwd: checkout, stdio: "inherit" });
const packDir = mkdtempSync(join(tmpdir(), "bridge-pack-"));
execFileSync("npm", ["pack", "--pack-destination", packDir], { cwd: checkout, stdio: "inherit" });
const [file] = readdirSync(packDir).filter((name) => name.endsWith(".tgz"));
const packed = readFileSync(join(packDir, file));
const sha256 = createHash("sha256").update(packed).digest("hex");
const { name, version } = JSON.parse(readFileSync(join(checkout, "package.json"), "utf8"));

const ui = resolve(new URL("..", import.meta.url).pathname);
const lockPath = join(ui, "vendor", "plugin-bridge.lock.json");
const lock = JSON.parse(readFileSync(lockPath, "utf8"));
copyFileSync(join(packDir, file), join(ui, "vendor", file));
writeFileSync(
  lockPath,
  JSON.stringify({ ...lock, package: name, version, file, sha256, source: { repo, branch, commit } }, null, 2) + "\n",
);

console.log(`vendored ${name}@${version} from ${branch}@${commit.slice(0, 7)} as vendor/${file} (sha256 ${sha256.slice(0, 12)}...)`);
console.log(`next: npm install ./vendor/${file} --ignore-scripts, so package-lock.json carries the new integrity, then commit ui/vendor, ui/package.json and ui/package-lock.json together.`);
