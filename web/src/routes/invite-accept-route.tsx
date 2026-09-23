import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, type ReactNode } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import { INVITE_CODE_PATTERN } from "../features/invites/api/invites-schemas.js";
import { AcceptInviteForm } from "../features/invites/components/accept-invite-form.js";
import { describePreviewError } from "../features/invites/components/invite-errors.js";
import {
  forgetInvitePreview,
  useAcceptInvite,
  useInvitePreview,
} from "../features/invites/hooks/invites-queries.js";
import { ApiError } from "../lib/api/errors.js";
import { INVITE_ACCEPTED_PATH } from "./invite-accepted-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

const INVALID =
  "This invite link is not valid, has expired, or has already been used.";

// The public landing page for an invite link. No sign-in is involved: the
// person is creating an account on the media server, not on Bloom.
export default function InviteAcceptRoute(): ReactNode {
  usePageTitle(pageTitle("Accept invite"));
  const { code = "" } = useParams();
  if (!INVITE_CODE_PATTERN.test(code)) {
    return <Invalid />;
  }
  return <InvitePage key={code} code={code} />;
}

function InvitePage({ code }: Readonly<{ code: string }>): ReactNode {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const preview = useInvitePreview(code);
  const accept = useAcceptInvite();
  // An invite that became unavailable while the form was open, and one that
  // never was, look the same: the code is forgotten and the form is gone.
  if (isNotFound(accept.error)) {
    forgetInvitePreview(queryClient, code);
    return <Invalid />;
  }
  if (preview.status === "pending") {
    return <AsyncStatus>Checking your invite…</AsyncStatus>;
  }
  if (preview.status === "error") {
    return isNotFound(preview.error) ? (
      <Invalid />
    ) : (
      <PreviewFailed error={preview.error} onRetry={preview.refetch} />
    );
  }
  return (
    <>
      <h1>You are invited to {preview.data.media_server_name}</h1>
      <p>
        Choose the username and password you will use to sign in to{" "}
        {preview.data.media_server_name}. Bloom creates the account there and
        does not keep the password.
      </p>
      <AcceptInviteForm
        invite={preview.data}
        pending={accept.isPending}
        serverError={accept.error}
        onSubmit={(request) => {
          accept.accept(
            { code, request },
            {
              // The code has done its job: drop it from the cache and from
              // the address bar before showing the confirmation.
              onAccepted: (accepted) => {
                forgetInvitePreview(queryClient, code);
                void navigate(INVITE_ACCEPTED_PATH, {
                  replace: true,
                  state: { accepted },
                });
              },
            },
          );
        }}
      />
    </>
  );
}

function isNotFound(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}

// This page can replace a focused form mid-flow, so it takes focus itself.
function Invalid(): ReactNode {
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  return (
    <>
      <h1 tabIndex={-1} ref={heading}>
        Invite not available
      </h1>
      <p role="alert">{INVALID}</p>
      <p>Ask the person who invited you for a new link.</p>
    </>
  );
}

function PreviewFailed({
  error,
  onRetry,
}: Readonly<{ error: unknown; onRetry: () => Promise<unknown> }>): ReactNode {
  return (
    <>
      <h1>Invite</h1>
      <AsyncStatus kind="alert">{describePreviewError(error)}</AsyncStatus>
      <button
        type="button"
        onClick={() => {
          void onRetry();
        }}
      >
        Retry
      </button>
    </>
  );
}
