import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import type { RequestProfile } from "../features/requests/api/requests-schemas.js";
import { AsyncStatus } from "../components/async-status.js";
import { SignInNotConfirmed } from "../features/requests/components/list-states.js";
import { RequestProfileForm } from "../features/requests/components/request-profile-form.js";
import { RequestProfilesTable } from "../features/requests/components/request-profiles-table.js";
import { TmdbKeySection } from "../features/requests/components/tmdb-key-section.js";
import {
  useDownloadManagers,
  useMetadataKeyPresence,
  useRemoveMetadataKey,
  useRemoveProfile,
  useRequestProfiles,
  useSaveProfile,
  useSetMetadataKey,
} from "../features/requests/hooks/requests-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function RequestProfilesRoute(): ReactNode {
  usePageTitle(pageTitle("Request settings"));
  const session = useSession();
  const accountId = session.data?.account.id;
  // The route guard only renders this page for a signed-in account. Keying
  // the page on the account remounts it when the principal changes, so a
  // late answer to one account's action can never show under another's.
  if (accountId === undefined) {
    return null;
  }
  return <RequestProfilesPage key={accountId} accountId={accountId} />;
}

function RequestProfilesPage({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const profiles = useRequestProfiles(accountId);
  const save = useSaveProfile(accountId);
  const remove = useRemoveProfile(accountId);
  const presence = useMetadataKeyPresence(accountId);
  const setKey = useSetMetadataKey(accountId);
  const removeKey = useRemoveMetadataKey(accountId);
  const managers = useDownloadManagers(accountId);
  const editing = useEditing();
  const denial =
    accessDenial(profiles.error) ??
    accessDenial(save.error) ??
    accessDenial(remove.error) ??
    accessDenial(presence.error) ??
    accessDenial(setKey.error) ??
    accessDenial(removeKey.error) ??
    accessDenial(managers.error);
  // The server denied something the cached session says is allowed: the
  // session is gone, or a permission was taken away. Re-reading the session
  // lets the route guard send the person to sign in or off this page.
  useSessionRecheck(
    denial !== undefined,
    Math.max(
      profiles.errorUpdatedAt,
      presence.errorUpdatedAt,
      managers.errorUpdatedAt,
      save.submittedAt,
      remove.submittedAt,
      setKey.submittedAt,
      removeKey.submittedAt,
    ),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  return (
    <>
      <h1>Request settings</h1>
      <p className="page-intro">
        Requests look titles up on The Movie Database and are fulfilled through
        a request profile: the download manager instance, quality profile, and
        root folder an approved request is sent to.{" "}
        <Link to="/admin/download-managers">Manage download managers.</Link>
      </p>
      <TmdbKeySection presence={presence} set={setKey} remove={removeKey} />
      <ProfileSection
        accountId={accountId}
        save={save}
        editing={editing}
        managers={managers}
      />
      <section aria-labelledby="request-profiles-heading" className="card">
        <h2 id="request-profiles-heading">Request profiles</h2>
        {denial === "unauthenticated" ? (
          <SignInNotConfirmed noun="profiles" onRetry={profiles.refetch} />
        ) : (
          <RequestProfilesTable
            query={profiles}
            removing={remove.isPending ? remove.variables : undefined}
            removeError={remove.error}
            onEdit={editing.start}
            onRemove={(id) => {
              remove.mutate(id);
            }}
          />
        )}
      </section>
    </>
  );
}

type Editing = Readonly<{
  profile: RequestProfile | undefined;
  // Bumped whenever the form must start over: a new subject or a success.
  formKey: number;
  start: (profile: RequestProfile) => void;
  stop: () => void;
}>;

// Which profile the form edits, if any. The table sits below the form, so
// choosing "Edit" also moves focus to the form's heading.
function useEditing(): Editing {
  const [profile, setProfile] = useState<RequestProfile | undefined>(undefined);
  const [formKey, setFormKey] = useState(0);
  const start = useCallback((next: RequestProfile) => {
    setProfile(next);
    setFormKey((current) => current + 1);
  }, []);
  const stop = useCallback(() => {
    setProfile(undefined);
    setFormKey((current) => current + 1);
  }, []);
  return { profile, formKey, start, stop };
}

function ProfileSection({
  accountId,
  save,
  editing,
  managers,
}: Readonly<{
  accountId: string;
  save: ReturnType<typeof useSaveProfile>;
  editing: Editing;
  managers: ReturnType<typeof useDownloadManagers>;
}>): ReactNode {
  const [saved, setSaved] = useState<string | undefined>(undefined);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const subject = editing.profile;
  useEffect(() => {
    if (subject !== undefined) {
      headingRef.current?.focus();
    }
  }, [subject]);
  return (
    <section aria-labelledby="profile-form-heading" className="card">
      <h2 id="profile-form-heading" ref={headingRef} tabIndex={-1}>
        {subject === undefined ? "Create a profile" : `Edit ${subject.name}`}
      </h2>
      {managers.status === "pending" ? (
        <AsyncStatus>Loading download managers…</AsyncStatus>
      ) : managers.status === "error" && managers.data === undefined ? (
        <>
          <AsyncStatus kind="alert">
            The download managers could not be loaded, so no profile can be
            filled in yet.
          </AsyncStatus>
          <button
            type="button"
            onClick={() => {
              void managers.refetch();
            }}
          >
            Retry
          </button>
        </>
      ) : (
        <RequestProfileForm
          key={editing.formKey}
          accountId={accountId}
          managers={managers.data.pages.flatMap((page) => page.items)}
          profile={subject}
          pending={save.isPending}
          serverError={save.error}
          onSubmit={(input) => {
            setSaved(undefined);
            save.mutate(
              { id: subject?.id, input },
              {
                onSuccess: (profile) => {
                  setSaved(profile.name);
                  editing.stop();
                },
              },
            );
          }}
          onCancel={subject === undefined ? undefined : editing.stop}
        />
      )}
      <p role="status">{saved === undefined ? "" : `Saved ${saved}.`}</p>
    </section>
  );
}
