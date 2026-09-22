import { z } from "zod/v4";

const LOGIN_PATH = "/login";
const MAX_PATH_LENGTH = 2048;

// Router state is attacker-influenced input (anyone can push history state),
// so it crosses a Zod boundary with a size bound before any string logic runs.
const returnStateSchema = z.object({
  from: z.string().max(MAX_PATH_LENGTH),
});

// Turns router state into a same-origin path to return to after sign-in.
// Anything that is not a plain in-app path, or that would loop back to the
// sign-in page, falls back to home: a deny-by-default parse.
export function safeDestination(state: unknown, origin: string): string {
  const parsed = returnStateSchema.safeParse(state);
  if (!parsed.success) {
    return "/";
  }
  const from = parsed.data.from;
  if (!from.startsWith("/") || hasUnsafeCharacters(from)) {
    return "/";
  }
  const url = new URL(from, origin);
  if (url.origin !== origin || url.pathname === LOGIN_PATH) {
    return "/";
  }
  return `${url.pathname}${url.search}${url.hash}`;
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
