import type {
  KnownPermission,
  Permission,
} from "../features/auth/api/auth-schemas.js";
import {
  hasAnyPermission,
  permissions,
  REQUESTS_PERMISSIONS,
} from "../features/auth/permissions.js";
import type { IconName } from "./nav-icons.js";

// One page a navigation group leads to. Its label is the page's own heading,
// so a tab and the page it opens always carry the same name.
export type NavPage = Readonly<{
  to: string;
  label: string;
  anyOf: readonly KnownPermission[];
}>;

// A sidebar entry. A group with more than one page shows its pages as tabs
// above whichever of them is open; the sidebar link opens the first page the
// person may see.
export type NavGroup = Readonly<{
  label: string;
  icon: IconName;
  pages: readonly NavPage[];
}>;

// Every signed-in area of the app, in sidebar order. Home and About are not
// listed: Home is the root and About is open to everyone.
export const NAV_GROUPS: readonly NavGroup[] = [
  {
    label: "Discover",
    icon: "compass",
    pages: [
      { to: "/requests", label: "Discover", anyOf: REQUESTS_PERMISSIONS },
    ],
  },
  {
    label: "Requests",
    icon: "inbox",
    pages: [
      {
        to: "/admin/requests",
        label: "Requests",
        anyOf: [permissions.requestsApprove],
      },
    ],
  },
  {
    label: "Playback",
    icon: "play",
    pages: [
      {
        to: "/admin/playback",
        label: "Playback",
        anyOf: [permissions.statsReadAll],
      },
      {
        to: "/admin/activity",
        label: "Activity",
        anyOf: [permissions.statsReadAll],
      },
    ],
  },
  {
    label: "Statistics",
    icon: "chart",
    pages: [
      {
        to: "/admin/statistics",
        label: "Statistics",
        anyOf: [permissions.statsReadAll],
      },
      {
        to: "/statistics",
        label: "My statistics",
        anyOf: [permissions.statsReadOwn],
      },
    ],
  },
  {
    label: "Libraries",
    icon: "library",
    pages: [
      {
        to: "/admin/libraries",
        label: "Libraries",
        anyOf: [permissions.statsReadAll],
      },
    ],
  },
  {
    label: "Users",
    icon: "users",
    pages: [
      {
        to: "/admin/accounts",
        label: "Accounts",
        anyOf: [permissions.usersManage],
      },
      {
        to: "/admin/invites",
        label: "Invites",
        anyOf: [permissions.usersInvite],
      },
      { to: "/admin/roles", label: "Roles", anyOf: [permissions.adminRoles] },
    ],
  },
  {
    label: "Settings",
    icon: "sliders",
    pages: [
      {
        to: "/admin/media-servers",
        label: "Media servers",
        anyOf: [permissions.adminSettings],
      },
      {
        to: "/admin/download-managers",
        label: "Download managers",
        anyOf: [permissions.adminSettings],
      },
      {
        to: "/admin/request-profiles",
        label: "Request settings",
        anyOf: [permissions.adminSettings],
      },
      {
        to: "/admin/notifications",
        label: "Notifications",
        anyOf: [permissions.adminSettings],
      },
      {
        to: "/admin/imports",
        label: "Imports",
        anyOf: [permissions.adminSettings],
      },
    ],
  },
];

// The pages of a group the person may open, in order.
export function visiblePages(
  group: NavGroup,
  granted: readonly Permission[],
): readonly NavPage[] {
  return group.pages.filter((page) => hasAnyPermission(granted, page.anyOf));
}

// True when the path is the page or one of the pages beneath it, such as a
// title under Discover or a watch under Playback.
export function isWithin(pathname: string, page: NavPage): boolean {
  return pathname === page.to || pathname.startsWith(`${page.to}/`);
}

// The page of the group the path belongs to, or undefined. The longest
// match wins, so /admin/statistics never claims /statistics or the reverse.
export function pageFor(
  group: NavGroup,
  pathname: string,
): NavPage | undefined {
  let found: NavPage | undefined;
  for (const page of group.pages) {
    if (isWithin(pathname, page) && page.to.length > (found?.to.length ?? 0)) {
      found = page;
    }
  }
  return found;
}

// The group the path belongs to, or undefined outside every group.
export function groupFor(pathname: string): NavGroup | undefined {
  return NAV_GROUPS.find((group) => pageFor(group, pathname) !== undefined);
}

// Where /admin leads: the first administration page the person may open,
// in sidebar order, or undefined when there is none.
export function firstAdminPage(
  granted: readonly Permission[],
): NavPage | undefined {
  for (const group of NAV_GROUPS) {
    const page = visiblePages(group, granted).find((candidate) =>
      candidate.to.startsWith("/admin/"),
    );
    if (page !== undefined) {
      return page;
    }
  }
  return undefined;
}
