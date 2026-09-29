import type { ReactNode } from "react";

// Small inline icons for the navigation. Decorative only: every link keeps
// its text, so the icons are hidden from assistive technology.
type IconProps = Readonly<{ name: IconName }>;

export type IconName =
  | "home"
  | "info"
  | "compass"
  | "chart"
  | "shield"
  | "inbox"
  | "play"
  | "server"
  | "download"
  | "upload"
  | "library"
  | "sliders"
  | "bell"
  | "ticket"
  | "users"
  | "search"
  | "clock"
  | "menu";

const PATHS: Readonly<Record<IconName, string>> = {
  home: "M3 11.5 12 4l9 7.5M5 10v10h5v-6h4v6h5V10",
  info: "M12 8h.01M11 12h1v4h1M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Z",
  compass: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Zm3.5-12.5-2 5-5 2 2-5 5-2Z",
  chart: "M4 20V10m6 10V4m6 16v-7m4 7H2",
  shield: "M12 3 4 6v6c0 5 3.5 8 8 9 4.5-1 8-4 8-9V6l-8-3Z",
  inbox: "M3 13h5l2 3h4l2-3h5M5 6h14l2 7v6H3v-6l2-7Z",
  play: "M8 5v14l11-7L8 5Z",
  server: "M4 5h16v5H4zM4 14h16v5H4zM7 7.5h.01M7 16.5h.01",
  download: "M12 4v11m0 0-4-4m4 4 4-4M4 19h16",
  upload: "M12 15V4m0 0L8 8m4-4 4 4M4 19h16",
  library: "M5 4v16M9 4v16M13 5l4 15M3 20h18",
  sliders: "M4 7h10m4 0h2M4 17h4m4 0h8M14 5v4M8 15v4",
  bell: "M6 16V11a6 6 0 1 1 12 0v5l2 2H4l2-2Zm4 3a2 2 0 0 0 4 0",
  ticket:
    "M4 8a2 2 0 0 0 2-2h12a2 2 0 0 0 2 2v3a2 2 0 0 0 0 4v3a2 2 0 0 0-2 2H6a2 2 0 0 0-2-2v-3a2 2 0 0 0 0-4V8Zm6-2v12",
  users:
    "M16 19v-1a4 4 0 0 0-8 0v1M12 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6Zm6 1a3 3 0 1 0 0-6M20 19v-1a3 3 0 0 0-2-2.8",
  search: "M10.5 18a7.5 7.5 0 1 0 0-15 7.5 7.5 0 0 0 0 15Zm10.5 3-5-5",
  clock: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18Zm0-13v5l3 2",
  menu: "M4 7h16M4 12h16M4 17h16",
};

export function NavIcon({ name }: IconProps): ReactNode {
  return (
    <svg
      className="nav-icon"
      viewBox="0 0 24 24"
      width="20"
      height="20"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d={PATHS[name]} />
    </svg>
  );
}
