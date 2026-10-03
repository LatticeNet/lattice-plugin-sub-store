/**
 * The plugin-bridge release this page builds on is pinned to its bytes.
 *
 * @latticenet resolves to GitHub Packages (ui/.npmrc). A lock entry that
 * carries only a version installs whatever that registry serves under the
 * version, with nothing to check it against, where the vendored tarball it
 * replaced was held by an integrity hash. The adopting branch has to write
 * the entry that way while the release is unpublished, because the
 * registry's tarball URL and the hash only exist once it is out. This test
 * is the merge gate for that window: it fails until someone runs
 * `npm install --package-lock-only` in ui/ against the published release and
 * commits the lock, so no branch merges with an unpinned dependency.
 */
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const NAME = "@latticenet/plugin-bridge";
const ui = fileURLToPath(new URL("..", import.meta.url));
const read = (file: string) => readFileSync(`${ui}${file}`, "utf8");

describe("the plugin-bridge dependency", () => {
  const pkg = JSON.parse(read("package.json")) as { dependencies: Record<string, string> };
  const lock = JSON.parse(read("package-lock.json")) as {
    packages: Record<string, { version?: string; resolved?: string; integrity?: string; dependencies?: Record<string, string> }>;
  };
  const version = pkg.dependencies[NAME] ?? "";
  const entry = lock.packages[`node_modules/${NAME}`] ?? {};

  it("is one exact version in package.json and the lock", () => {
    expect(version).toMatch(/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?$/);
    expect(lock.packages[""]?.dependencies?.[NAME]).toBe(version);
    expect(entry.version).toBe(version);
  });

  it("is locked to the registry's tarball and its integrity (after the release is published, run npm install --package-lock-only in ui/ and commit the lock)", () => {
    const registry = /^@latticenet:registry=(\S+?)\/?$/m.exec(read(".npmrc"))?.[1] ?? "";
    expect(registry).toMatch(/^https:\/\//);
    expect(entry.resolved ?? "", "lock entry has no resolved URL").toMatch(new RegExp(`^${registry.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}/`));
    expect(entry.integrity ?? "", "lock entry has no integrity").toMatch(/^sha512-[A-Za-z0-9+/]+={0,2}$/);
  });
});
