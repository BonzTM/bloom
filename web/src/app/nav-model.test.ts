import { describe, expect, it } from "@jest/globals";
import {
  firstAdminPage,
  groupFor,
  NAV_GROUPS,
  pageFor,
  visiblePages,
} from "./nav-model.js";

function group(label: string) {
  const found = NAV_GROUPS.find((candidate) => candidate.label === label);
  if (found === undefined) {
    throw new Error(`no group ${label}`);
  }
  return found;
}

describe("groupFor", () => {
  it.each([
    ["/requests", "Discover"],
    ["/requests/movies/949", "Discover"],
    ["/admin/requests", "Requests"],
    ["/admin/playback/watches/abc", "Playback"],
    ["/admin/activity", "Playback"],
    ["/statistics", "Statistics"],
    ["/admin/statistics/users/s/u", "Statistics"],
    ["/admin/libraries/s/items/i", "Libraries"],
    ["/admin/accounts/a", "Users"],
    ["/admin/media-servers/s/exclusions", "Settings"],
    ["/ADMIN/Playback", "Playback"],
    ["/admin/roles/", "Users"],
  ])("puts %s under %s", (path, label) => {
    expect(groupFor(path)?.label).toBe(label);
  });

  it.each(["/", "/about", "/login", "/admin", "/requestsx", "/admin/rolesx"])(
    "leaves %s outside every area",
    (path) => {
      expect(groupFor(path)).toBeUndefined();
    },
  );
});

describe("pageFor", () => {
  it("never lets one statistics page claim the other", () => {
    const stats = group("Statistics");
    expect(pageFor(stats, "/statistics")?.to).toBe("/statistics");
    expect(pageFor(stats, "/admin/statistics")?.to).toBe("/admin/statistics");
  });
});

describe("visiblePages", () => {
  it("keeps only the pages the permissions open, in order", () => {
    expect(
      visiblePages(group("Users"), ["users.invite", "admin.roles"]).map(
        (page) => page.label,
      ),
    ).toEqual(["Invites", "Roles"]);
    expect(visiblePages(group("Settings"), ["users.invite"])).toEqual([]);
  });
});

describe("firstAdminPage", () => {
  it.each([
    [["requests.approve", "admin.settings"], "/admin/requests"],
    [["stats.read.all"], "/admin/playback"],
    [["users.invite"], "/admin/invites"],
    [["admin.settings"], "/admin/media-servers"],
  ] as const)("sends %j to %s", (granted, to) => {
    expect(firstAdminPage(granted)?.to).toBe(to);
  });

  it("finds nothing for an account with only personal permissions", () => {
    expect(
      firstAdminPage(["stats.read.own", "requests.create"]),
    ).toBeUndefined();
  });
});
