import type { ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import type { RequestProfile } from "../api/requests-schemas.js";
import type { useRequestProfiles } from "../hooks/requests-queries.js";
import { ConfirmControls } from "./confirm-controls.js";
import {
  ActionFailed,
  LoadFailed,
  Pager,
  RefreshFailed,
} from "./list-states.js";
import { describeProfileError } from "./request-errors.js";
import { kindLabel } from "./request-format.js";

type ProfilesQuery = ReturnType<typeof useRequestProfiles>;

type RequestProfilesTableProps = Readonly<{
  query: ProfilesQuery;
  // The id of the profile whose removal is in flight, if any.
  removing: string | undefined;
  removeError: unknown;
  onEdit: (profile: RequestProfile) => void;
  onRemove: (id: string) => void;
}>;

// The route owns the query and decides what a 401 or 403 means; this renders
// every other state: loading, empty, rows, a failed refresh that keeps the
// rows, a failed later page that keeps the rows, and a failed first load.
export function RequestProfilesTable({
  query,
  removing,
  removeError,
  onEdit,
  onRemove,
}: RequestProfilesTableProps): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading profiles…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed noun="profiles" onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  return (
    <>
      {refreshFailed ? (
        <RefreshFailed
          noun="profiles"
          retrying={query.isRefetching}
          onRetry={query.refetch}
        />
      ) : null}
      <ActionFailed summary={describeProfileError(removeError)} />
      {items.length === 0 ? (
        <p>No request profile exists yet. Requests need at least one.</p>
      ) : (
        <Table
          profiles={items}
          removing={removing}
          onEdit={onEdit}
          onRemove={onRemove}
        />
      )}
      <Pager
        noun="profiles"
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}

type TableProps = Readonly<{
  profiles: readonly RequestProfile[];
  removing: string | undefined;
  onEdit: (profile: RequestProfile) => void;
  onRemove: (id: string) => void;
}>;

function Table({
  profiles,
  removing,
  onEdit,
  onRemove,
}: TableProps): ReactNode {
  return (
    <div
      className="table-scroll"
      role="region"
      aria-label="Request profiles table"
      tabIndex={0}
    >
      <table>
        <caption>Request profiles, ordered by name</caption>
        <thead>
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Kinds</th>
            <th scope="col">Download manager</th>
            <th scope="col">Quality profile</th>
            <th scope="col">Root folder</th>
            <th scope="col">Tags</th>
            <th scope="col">Actions</th>
          </tr>
        </thead>
        <tbody>
          {profiles.map((profile) => (
            <ProfileRow
              key={profile.id}
              profile={profile}
              removing={removing === profile.id}
              onEdit={onEdit}
              onRemove={onRemove}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}

type ProfileRowProps = Readonly<{
  profile: RequestProfile;
  removing: boolean;
  onEdit: (profile: RequestProfile) => void;
  onRemove: (id: string) => void;
}>;

function ProfileRow({
  profile,
  removing,
  onEdit,
  onRemove,
}: ProfileRowProps): ReactNode {
  return (
    <tr>
      <th scope="row">{profile.name}</th>
      <td>{profile.kinds.map(kindLabel).join(", ")}</td>
      <td>
        {profile.download_manager_kind} · {profile.download_manager_instance}
      </td>
      <td>{profile.quality_profile}</td>
      <td>
        <code>{profile.root_folder}</code>
      </td>
      <td>{profile.tags.length === 0 ? "—" : profile.tags.join(", ")}</td>
      <td>
        <span className="row-actions">
          <button
            type="button"
            onClick={() => {
              onEdit(profile);
            }}
          >
            Edit {profile.name}
          </button>
          <ConfirmControls
            action="Remove"
            confirming="removing"
            subject={profile.name}
            busy={removing}
            busyLabel="Removing…"
            onConfirm={() => {
              onRemove(profile.id);
            }}
          />
        </span>
      </td>
    </tr>
  );
}
