const LOGIN_PATH = "/login";

// Turns router state into a same-origin path to return to after sign-in.
// Anything that is not a plain in-app path, or that would loop back to the
// sign-in page, falls back to home. The value comes from navigation state,
// which is attacker-influenced input, so this is a deny-by-default parse.
export function safeDestination(state: unknown, origin: string): string {
  const from = readFrom(state);
  if (
    from === undefined ||
    !from.startsWith("/") ||
    hasUnsafeCharacters(from)
  ) {
    return "/";
  }
  const url = new URL(from, origin);
  if (url.origin !== origin || url.pathname === LOGIN_PATH) {
    return "/";
  }
  return `${url.pathname}${url.search}${url.hash}`;
}

function readFrom(state: unknown): string | undefined {
  if (typeof state !== "object" || state === null || !("from" in state)) {
    return undefined;
  }
  const from: unknown = state.from;
  return typeof from === "string" ? from : undefined;
}

// Backslashes and control characters let some parsers read "/\evil" as an
// authority or a header break; none of them belong in an in-app path.
function hasUnsafeCharacters(path: string): boolean {
  for (const char of path) {
    const code = char.codePointAt(0) ?? 0;
    if (char === "\\" || code < 0x20 || code === 0x7f) {
      return true;
    }
  }
  return false;
}
