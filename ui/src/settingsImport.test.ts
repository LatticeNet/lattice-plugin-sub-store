import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const settings = readFileSync(new URL("./screens/SettingsScreen.vue", import.meta.url), "utf8");
const empty = readFileSync(new URL("./screens/SubscriptionsScreen.vue", import.meta.url), "utf8");

describe("import from a running Sub-Store uses migrate, not a second path", () => {
  it("unwraps the official ?api= address and confirms before writing", () => {
    expect(settings).toContain("resolveSubStoreBase");
    expect(settings).toContain("describeSubStoreBase");
    expect(settings).toContain("Import from a running Sub-Store");
    expect(settings).toContain("requestMigrate()");
    expect(settings).toContain('verb="Import"');
    expect(settings).not.toContain("ops.migrate(migrateUrl)");
    expect(empty).toContain("resolveSubStoreBase");
    expect(empty).toContain("confirmMigrate()");
  });

  it("masks the secret path and never prints the backend URL into the confirm", () => {
    expect(settings).toContain("migrateParsed.value.origin");
    expect(settings).toContain("<MaskedUrlInput");
    expect(settings).not.toContain("migrateParsed.masked");
    expect(empty).toContain("migrateConfirmNames");
    expect(empty).toContain("migrateParsed.value.origin");
  });

  it("does not put an http URL in the settings template, which would fail verify:build", () => {
    expect(settings).not.toMatch(/https?:\/\//);
  });

  it("keeps backup restore as the envelope path, separate from migrate", () => {
    expect(settings).toContain("Backup envelope");
    expect(settings).toContain("importBackup");
    expect(settings).toContain("exportBackup");
  });
});
