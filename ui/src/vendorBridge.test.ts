import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

/**
 * The vendored chassis is the build it says it is.
 *
 * Until the bridge publishes the chassis, this page runs on a tarball packed
 * from a bridge branch. A tarball is not a version: nothing in package.json
 * says which commit it came from, so it could sit frozen while the other
 * three plugin pages picked up a changed radius or row height, and the drift
 * the chassis exists to end would be back with no visible cause. The record
 * beside the tarball names the commit and the sha256 of the pack, and this
 * holds the three together: the dependency points at the recorded file, the
 * file hashes to the recorded sha, and the pack inside is the recorded
 * version. Replace the tarball with scripts/vendor-bridge.mjs, which rewrites
 * the record; replace it by hand and this fails.
 */
describe("the vendored plugin-bridge build", () => {
  const ui = new URL("../", import.meta.url);
  const pkg = JSON.parse(readFileSync(new URL("./package.json", ui), "utf8"));
  const lock = JSON.parse(readFileSync(new URL("./vendor/plugin-bridge.lock.json", ui), "utf8"));
  const tarball = new URL(`./vendor/${lock.file}`, ui);

  it("is the file the dependency points at", () => {
    expect(pkg.dependencies["@latticenet/plugin-bridge"]).toBe(`file:vendor/${lock.file}`);
  });

  it("hashes to the sha256 recorded for its source commit", () => {
    expect(lock.source.commit).toMatch(/^[0-9a-f]{40}$/);
    const sha256 = createHash("sha256").update(readFileSync(tarball)).digest("hex");
    expect(sha256).toBe(lock.sha256);
  });

  it("carries the recorded package and version", () => {
    const inside = JSON.parse(execFileSync("tar", ["-xzOf", tarball.pathname, "package/package.json"], { encoding: "utf8" }));
    expect(inside.name).toBe(lock.package);
    expect(inside.version).toBe(lock.version);
  });
});
