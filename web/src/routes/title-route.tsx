import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import {
  PROVIDER_ID_PATTERN,
  type MetadataSeason,
  type MetadataTitle,
} from "../features/requests/api/metadata-schemas.js";
import type {
  MediaKind,
  MediaRequest,
} from "../features/requests/api/requests-schemas.js";
import { Poster } from "../features/requests/components/poster.js";
import { describeSearchError } from "../features/requests/components/request-errors.js";
import { RequestForm } from "../features/requests/components/request-form.js";
import {
  kindLabel,
  statusLabel,
  titleWithYear,
} from "../features/requests/components/request-format.js";
import {
  useCreateRequest,
  useMovie,
  useRequestProfiles,
  useSeries,
} from "../features/requests/hooks/requests-queries.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { NotFoundRoute } from "./not-found-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

const KINDS: Readonly<Record<string, MediaKind>> = {
  movies: "movie",
  series: "series",
};

// One title from the metadata provider, with the form to request it.
export default function TitleRoute(): ReactNode {
  const { kind: segment = "", id = "" } = useParams();
  const session = useSession();
  const accountId = session.data?.account.id;
  const kind = KINDS[segment];
  if (kind === undefined || !PROVIDER_ID_PATTERN.test(id)) {
    return <NotFoundRoute />;
  }
  if (accountId === undefined) {
    return null;
  }
  return (
    <TitlePage
      key={`${accountId}/${kind}/${id}`}
      accountId={accountId}
      kind={kind}
      providerId={id}
    />
  );
}

type TitlePageProps = Readonly<{
  accountId: string;
  kind: MediaKind;
  providerId: string;
}>;

function TitlePage({ accountId, kind, providerId }: TitlePageProps): ReactNode {
  const movie = useMovie(accountId, providerId, kind === "movie");
  const series = useSeries(accountId, providerId, kind === "series");
  const title = kind === "movie" ? movie : series;
  const profiles = useRequestProfiles(accountId);
  const create = useCreateRequest(accountId);
  usePageTitle(pageTitle(title.data?.title ?? kindLabel(kind)));
  const denial =
    accessDenial(title.error) ??
    accessDenial(profiles.error) ??
    accessDenial(create.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(title.errorUpdatedAt, profiles.errorUpdatedAt, create.submittedAt),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  if (title.status === "pending") {
    return <AsyncStatus>Loading the title…</AsyncStatus>;
  }
  if (title.status === "error") {
    return <TitleFailed error={title.error} onRetry={title.refetch} />;
  }
  const seasons: readonly MetadataSeason[] | undefined =
    kind === "series" ? series.data?.seasons : undefined;
  return (
    <article className="title-page">
      <div className="title-poster">
        <Poster
          posterPath={title.data.poster_path}
          title={title.data.title}
          size="w500"
        />
      </div>
      <div className="title-body">
        <p>
          <Link to="/requests">Back to requests</Link>
        </p>
        <h1>{titleWithYear(title.data)}</h1>
        <p className="badge badge-neutral">{kindLabel(kind)}</p>
        <Overview title={title.data} />
        <RequestSection
          title={title.data}
          seasons={seasons}
          profiles={profiles}
          create={create}
        />
      </div>
    </article>
  );
}

function Overview({ title }: Readonly<{ title: MetadataTitle }>): ReactNode {
  if (title.overview === "") {
    return <p className="row-detail">No overview is available.</p>;
  }
  return <p className="title-overview">{title.overview}</p>;
}

type RequestSectionProps = Readonly<{
  title: MetadataTitle;
  seasons: readonly MetadataSeason[] | undefined;
  profiles: ReturnType<typeof useRequestProfiles>;
  create: ReturnType<typeof useCreateRequest>;
}>;

function RequestSection({
  title,
  seasons,
  profiles,
  create,
}: RequestSectionProps): ReactNode {
  const [created, setCreated] = useState<MediaRequest | null>(null);
  const doneRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (created !== null) {
      doneRef.current?.focus();
    }
  }, [created]);
  if (created !== null) {
    return (
      <section aria-labelledby="requested-heading" className="card">
        <h2 id="requested-heading" ref={doneRef} tabIndex={-1}>
          Requested {titleWithYear(created)}
        </h2>
        <p>
          Status: {statusLabel(created.status)}.{" "}
          <Link to="/requests">See your requests.</Link>
        </p>
      </section>
    );
  }
  if (profiles.status === "pending") {
    return <AsyncStatus>Loading request profiles…</AsyncStatus>;
  }
  if (profiles.status === "error" && profiles.data === undefined) {
    return (
      <>
        <AsyncStatus kind="alert">
          The request profiles could not be loaded, so nothing can be requested
          yet.
        </AsyncStatus>
        <button
          type="button"
          onClick={() => {
            void profiles.refetch();
          }}
        >
          Retry
        </button>
      </>
    );
  }
  return (
    <section aria-labelledby="request-heading" className="card">
      <h2 id="request-heading">
        Request this {kindLabel(title.kind).toLowerCase()}
      </h2>
      <RequestForm
        title={title}
        seasons={seasons}
        profiles={profiles.data.pages.flatMap((page) => page.items)}
        pending={create.isPending}
        serverError={create.error}
        onSubmit={(input) => {
          create.mutate(input, {
            onSuccess: (request) => {
              setCreated(request);
            },
          });
        }}
      />
    </section>
  );
}

function TitleFailed({
  error,
  onRetry,
}: Readonly<{ error: unknown; onRetry: () => Promise<unknown> }>): ReactNode {
  return (
    <>
      <h1>Title unavailable</h1>
      <AsyncStatus kind="alert">{describeSearchError(error)}</AsyncStatus>
      <p>
        <button
          type="button"
          onClick={() => {
            void onRetry();
          }}
        >
          Retry
        </button>{" "}
        <Link to="/requests">Back to requests</Link>
      </p>
    </>
  );
}
