import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import {
  requestDecisionSchema,
  type MediaRequest,
  type RequestDecision,
} from "../api/requests-schemas.js";
import type { Decision, useRequests } from "../hooks/requests-queries.js";
import {
  ActionFailed,
  LoadFailed,
  Pager,
  RefreshFailed,
} from "./list-states.js";
import { describeDecisionError } from "./request-errors.js";
import { RequestProgress } from "./request-progress.js";
import {
  kindLabel,
  seasonsLabel,
  statusBadgeClass,
  statusLabel,
  titleWithYear,
} from "./request-format.js";

type RequestsQuery = ReturnType<typeof useRequests>;

type RequestsTableProps = Readonly<{
  query: RequestsQuery;
  accountId: string;
  profileNames: ReadonlyMap<string, string>;
  // The id of the request whose decision is in flight, if any.
  deciding: string | undefined;
  decideError: unknown;
  onDecide: (decision: Decision) => void;
}>;

// The route owns the query and the status filter; this renders every other
// state: loading, empty, rows, a failed refresh that keeps the rows, a
// failed later page that keeps the rows, and a failed first load.
export function RequestsTable({
  query,
  accountId,
  profileNames,
  deciding,
  decideError,
  onDecide,
}: RequestsTableProps): ReactNode {
  if (query.status === "pending") {
    return <AsyncStatus>Loading requests…</AsyncStatus>;
  }
  if (query.status === "error" && query.data === undefined) {
    return <LoadFailed noun="requests" onRetry={query.refetch} />;
  }
  const items = query.data.pages.flatMap((page) => page.items);
  const refreshFailed = query.isError && !query.isFetchNextPageError;
  return (
    <>
      {refreshFailed ? (
        <RefreshFailed
          noun="requests"
          retrying={query.isRefetching}
          onRetry={query.refetch}
        />
      ) : null}
      <ActionFailed summary={describeDecisionError(decideError)} />
      {items.length === 0 ? (
        <p>No requests match.</p>
      ) : (
        <Table
          requests={items}
          accountId={accountId}
          profileNames={profileNames}
          deciding={deciding}
          onDecide={onDecide}
        />
      )}
      <Pager
        noun="requests"
        hasMore={query.hasNextPage}
        loading={query.isFetchingNextPage}
        failed={query.isFetchNextPageError}
        onMore={query.fetchNextPage}
      />
    </>
  );
}

type TableProps = Readonly<{
  requests: readonly MediaRequest[];
  accountId: string;
  profileNames: ReadonlyMap<string, string>;
  deciding: string | undefined;
  onDecide: (decision: Decision) => void;
}>;

function Table({
  requests,
  accountId,
  profileNames,
  deciding,
  onDecide,
}: TableProps): ReactNode {
  return (
    <div
      className="table-scroll"
      role="region"
      aria-label="Requests table"
      tabIndex={0}
    >
      <table>
        <caption>Requests, newest first</caption>
        <thead>
          <tr>
            <th scope="col">Title</th>
            <th scope="col">Kind</th>
            <th scope="col">Profile</th>
            <th scope="col">Requester</th>
            <th scope="col">Status</th>
            <th scope="col">Requested</th>
            <th scope="col">Decision</th>
            <th scope="col">Actions</th>
          </tr>
        </thead>
        <tbody>
          {requests.map((request) => (
            <RequestRow
              key={request.id}
              request={request}
              accountId={accountId}
              profileName={profileNames.get(request.profile_id)}
              deciding={deciding === request.id}
              onDecide={onDecide}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}

type RequestRowProps = Readonly<{
  request: MediaRequest;
  accountId: string;
  profileName: string | undefined;
  deciding: boolean;
  onDecide: (decision: Decision) => void;
}>;

function RequestRow({
  request,
  accountId,
  profileName,
  deciding,
  onDecide,
}: RequestRowProps): ReactNode {
  const title = titleWithYear(request);
  const seasons = seasonsLabel(request);
  return (
    <tr>
      <th scope="row">
        {title}
        {seasons === "" ? null : <span className="row-detail">{seasons}</span>}
      </th>
      <td>{kindLabel(request.kind)}</td>
      <td>{profileName ?? "Unknown profile"}</td>
      <td>
        {request.requester_username === "" ? (
          <code title={request.requester_account_id}>
            {shortId(request.requester_account_id)}
          </code>
        ) : (
          <span title={request.requester_account_id}>
            {request.requester_username}
          </span>
        )}
      </td>
      <td>
        <span className={statusBadgeClass(request.status)}>
          {statusLabel(request.status)}
        </span>
      </td>
      <td>
        <time dateTime={request.created_at}>
          {request.created_at.slice(0, 10)}
        </time>
      </td>
      <td>{decisionSummary(request)}</td>
      <td>
        <RowActions
          request={request}
          accountId={accountId}
          title={title}
          deciding={deciding}
          onDecide={onDecide}
        />
      </td>
    </tr>
  );
}

// Pending requests are decided; failed ones may be approved again, which
// sends them back to the download manager; processing ones show progress.
function RowActions({
  request,
  accountId,
  title,
  deciding,
  onDecide,
}: Readonly<{
  request: MediaRequest;
  accountId: string;
  title: string;
  deciding: boolean;
  onDecide: (decision: Decision) => void;
}>): ReactNode {
  if (request.status === "pending") {
    return (
      <DecisionControls
        id={request.id}
        title={title}
        deciding={deciding}
        onDecide={onDecide}
      />
    );
  }
  if (request.status === "failed") {
    return deciding ? (
      <span role="status">Saving the decision…</span>
    ) : (
      <button
        type="button"
        onClick={() => {
          onDecide({ id: request.id, verb: "approve", decision: {} });
        }}
      >
        Approve again <span className="visually-hidden">{title}</span>
      </button>
    );
  }
  if (request.status === "processing") {
    return (
      <RequestProgress
        accountId={accountId}
        requestId={request.id}
        title={title}
      />
    );
  }
  return <span aria-hidden="true">—</span>;
}

function shortId(id: string): string {
  return id.slice(0, 8);
}

function decisionSummary(request: MediaRequest): ReactNode {
  if (request.decided_at === undefined && request.failure_reason === "") {
    return "—";
  }
  return (
    <>
      {request.decided_at === undefined ? null : (
        <time dateTime={request.decided_at}>
          {request.decided_at.slice(0, 10)}
        </time>
      )}
      {request.decision_reason === "" ? null : (
        <span className="row-detail">{request.decision_reason}</span>
      )}
      {request.failure_reason === "" ? null : (
        <span className="row-detail failure-reason">
          {request.failure_reason}
        </span>
      )}
    </>
  );
}

type Verb = Decision["verb"];

type DecisionControlsProps = Readonly<{
  id: string;
  title: string;
  deciding: boolean;
  onDecide: (decision: Decision) => void;
}>;

// A decision is two steps: choose approve or decline, then confirm with an
// optional reason the requester will see. Focus moves to the reason field
// and comes back to the chosen button on cancel.
function DecisionControls({
  id,
  title,
  deciding,
  onDecide,
}: DecisionControlsProps): ReactNode {
  const [verb, setVerb] = useState<Verb | undefined>(undefined);
  const approveRef = useRef<HTMLButtonElement>(null);
  const declineRef = useRef<HTMLButtonElement>(null);
  const returnTo = useRef<Verb | undefined>(undefined);
  useEffect(() => {
    if (verb !== undefined || returnTo.current === undefined) {
      return;
    }
    const target = returnTo.current === "approve" ? approveRef : declineRef;
    returnTo.current = undefined;
    target.current?.focus();
  }, [verb]);
  if (deciding) {
    return <span role="status">Saving the decision…</span>;
  }
  if (verb === undefined) {
    return (
      <span className="row-actions">
        <button
          ref={approveRef}
          type="button"
          className="btn-primary"
          onClick={() => {
            setVerb("approve");
          }}
        >
          Approve <span className="visually-hidden">{title}</span>
        </button>
        <button
          ref={declineRef}
          type="button"
          onClick={() => {
            setVerb("decline");
          }}
        >
          Decline <span className="visually-hidden">{title}</span>
        </button>
      </span>
    );
  }
  return (
    <DecisionForm
      verb={verb}
      title={title}
      onConfirm={(decision) => {
        setVerb(undefined);
        onDecide({ id, verb, decision });
      }}
      onCancel={() => {
        returnTo.current = verb;
        setVerb(undefined);
      }}
    />
  );
}

const VERB_LABELS: Readonly<Record<Verb, { doing: string; done: string }>> = {
  approve: { doing: "approving", done: "Approve" },
  decline: { doing: "declining", done: "Decline" },
};

function DecisionForm({
  verb,
  title,
  onConfirm,
  onCancel,
}: Readonly<{
  verb: Verb;
  title: string;
  onConfirm: (decision: RequestDecision) => void;
  onCancel: () => void;
}>): ReactNode {
  const formId = useId();
  const reasonRef = useRef<HTMLTextAreaElement>(null);
  const [error, setError] = useState<string | undefined>(undefined);
  useEffect(() => {
    reasonRef.current?.focus();
  }, []);
  const labels = VERB_LABELS[verb];
  const reasonId = `${formId}-reason`;
  const errorId = `${formId}-reason-error`;
  return (
    <form
      className="decision-form"
      aria-label={`Confirm ${labels.doing} ${title}`}
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        const raw = new FormData(event.currentTarget).get("reason");
        const reason = typeof raw === "string" ? raw.trim() : "";
        const parsed = requestDecisionSchema.safeParse(
          reason === "" ? {} : { reason },
        );
        if (!parsed.success) {
          setError("Use at most 1000 bytes for the reason.");
          reasonRef.current?.focus();
          return;
        }
        onConfirm(parsed.data);
      }}
    >
      <label htmlFor={reasonId}>Reason (optional)</label>
      <textarea
        ref={reasonRef}
        id={reasonId}
        name="reason"
        rows={2}
        aria-invalid={error !== undefined}
        aria-describedby={errorId}
      />
      <p id={errorId} className="field-error">
        {error}
      </p>
      <span className="confirm-remove">
        <button
          type="submit"
          className={verb === "approve" ? "btn-primary" : "btn-danger"}
        >
          Confirm {labels.doing}{" "}
          <span className="visually-hidden">{title}</span>
        </button>
        <button type="button" className="btn-ghost" onClick={onCancel}>
          Cancel {labels.doing} <span className="visually-hidden">{title}</span>
        </button>
      </span>
    </form>
  );
}
