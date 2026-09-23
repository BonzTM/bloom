import { useEffect, useRef, useState, type ReactNode } from "react";
import type { CreatedInvite } from "../api/invites-schemas.js";

type InviteLinkProps = Readonly<{
  created: CreatedInvite;
  origin: string;
  onDismiss: () => void;
}>;

type CopyState = "idle" | "copied" | "failed";

// The one time the invite link is visible. Focus lands on it so a keyboard
// user finds it immediately; dismissing it drops the code from the page.
export function InviteLink({
  created,
  origin,
  onDismiss,
}: InviteLinkProps): ReactNode {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [copy, setCopy] = useState<CopyState>("idle");
  const link = `${origin}${created.accept_path}`;
  useEffect(() => {
    headingRef.current?.focus();
  }, []);

  async function copyLink(): Promise<void> {
    try {
      await navigator.clipboard.writeText(link);
      setCopy("copied");
    } catch {
      setCopy("failed");
    }
  }

  return (
    <section className="invite-link" aria-labelledby="invite-link-heading">
      <h3 id="invite-link-heading" tabIndex={-1} ref={headingRef}>
        Invite {created.invite.label} is ready
      </h3>
      <p>
        Share this link now. It is shown once and cannot be recovered later.
      </p>
      <p>
        <code>{link}</code>
      </p>
      <div className="invite-link-actions">
        <button
          type="button"
          onClick={() => {
            void copyLink();
          }}
        >
          Copy link
        </button>
        <button type="button" onClick={onDismiss}>
          Dismiss
        </button>
      </div>
      <p role="status">{copyMessage(copy)}</p>
    </section>
  );
}

function copyMessage(state: CopyState): string {
  switch (state) {
    case "copied":
      return "Link copied.";
    case "failed":
      return "Copying failed. Select the link and copy it by hand.";
    case "idle":
      return "";
  }
}
