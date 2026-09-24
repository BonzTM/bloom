import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { AsyncStatus } from "../components/async-status.js";
import {
  useSession,
  useSessionRecheck,
} from "../features/auth/hooks/auth-queries.js";
import type { NotificationChannel } from "../features/notifications/api/notification-schemas.js";
import { ChannelForm } from "../features/notifications/components/channel-form.js";
import { ChannelsTable } from "../features/notifications/components/channels-table.js";
import { DeliveriesPanel } from "../features/notifications/components/deliveries-panel.js";
import {
  useChannels,
  useRemoveChannel,
  useSaveChannel,
  useTestChannel,
} from "../features/notifications/hooks/notifications-queries.js";
import { SignInNotConfirmed } from "../features/requests/components/list-states.js";
import { accessDenial } from "../lib/api/errors.js";
import { AccessDeniedRoute } from "./access-denied-route.js";
import { pageTitle, usePageTitle } from "./use-page-title.js";

export default function NotificationChannelsRoute(): ReactNode {
  usePageTitle(pageTitle("Notifications"));
  const session = useSession();
  const accountId = session.data?.account.id;
  // The route guard only renders this page for a signed-in account. Keying
  // the page on the account remounts it when the principal changes.
  if (accountId === undefined) {
    return null;
  }
  return <ChannelsPage key={accountId} accountId={accountId} />;
}

function ChannelsPage({
  accountId,
}: Readonly<{ accountId: string }>): ReactNode {
  const channels = useChannels(accountId);
  const save = useSaveChannel(accountId);
  const remove = useRemoveChannel(accountId);
  const test = useTestChannel(accountId);
  const editing = useEditing();
  const [shown, setShown] = useState<string | undefined>(undefined);
  const denial =
    accessDenial(channels.error) ??
    accessDenial(save.error) ??
    accessDenial(remove.error) ??
    accessDenial(test.error);
  useSessionRecheck(
    denial !== undefined,
    Math.max(
      channels.errorUpdatedAt,
      save.submittedAt,
      remove.submittedAt,
      test.submittedAt,
    ),
  );
  if (denial === "forbidden") {
    return <AccessDeniedRoute />;
  }
  const items = channels.data?.pages.flatMap((page) => page.items) ?? [];
  const shownChannel = items.find((channel) => channel.id === shown);
  return (
    <>
      <h1>Notifications</h1>
      <p className="page-intro">
        A channel is a place Bloom tells about requests: a webhook, a Discord
        server, or an email address. Each channel picks the events it wants;
        deliveries are queued and retried, so nothing is lost when a channel is
        briefly down.
      </p>
      <FormSection save={save} editing={editing} />
      <section aria-labelledby="channels-heading" className="card">
        <h2 id="channels-heading">Channels</h2>
        {denial === "unauthenticated" ? (
          <SignInNotConfirmed noun="channels" onRetry={channels.refetch} />
        ) : (
          <>
            <TestOutcome test={test} channels={items} />
            <ChannelsTable
              query={channels}
              removing={remove.isPending ? remove.variables : undefined}
              testing={test.isPending ? test.variables : undefined}
              actionError={remove.error ?? test.error}
              shown={shown}
              onEdit={editing.start}
              onTest={(id) => {
                test.mutate(id);
              }}
              onShowDeliveries={setShown}
              onRemove={(id) => {
                if (shown === id) {
                  setShown(undefined);
                }
                remove.mutate(id);
              }}
            />
          </>
        )}
      </section>
      {shownChannel === undefined ? null : (
        <section aria-labelledby="deliveries-heading" className="card">
          <h2 id="deliveries-heading">Deliveries of {shownChannel.name}</h2>
          <DeliveriesPanel
            accountId={accountId}
            channelId={shownChannel.id}
            channelName={shownChannel.name}
          />
        </section>
      )}
    </>
  );
}

function TestOutcome({
  test,
  channels,
}: Readonly<{
  test: ReturnType<typeof useTestChannel>;
  channels: readonly NotificationChannel[];
}>): ReactNode {
  if (!test.isSuccess) {
    return null;
  }
  const name = channels.find((channel) => channel.id === test.variables)?.name;
  return (
    <AsyncStatus>
      Test message sent{name === undefined ? "" : ` through ${name}`}.
    </AsyncStatus>
  );
}

type Editing = Readonly<{
  channel: NotificationChannel | undefined;
  formKey: number;
  start: (channel: NotificationChannel) => void;
  stop: () => void;
}>;

function useEditing(): Editing {
  const [channel, setChannel] = useState<NotificationChannel | undefined>(
    undefined,
  );
  const [formKey, setFormKey] = useState(0);
  const start = useCallback((next: NotificationChannel) => {
    setChannel(next);
    setFormKey((current) => current + 1);
  }, []);
  const stop = useCallback(() => {
    setChannel(undefined);
    setFormKey((current) => current + 1);
  }, []);
  return { channel, formKey, start, stop };
}

function FormSection({
  save,
  editing,
}: Readonly<{
  save: ReturnType<typeof useSaveChannel>;
  editing: Editing;
}>): ReactNode {
  const [saved, setSaved] = useState<string | undefined>(undefined);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const subject = editing.channel;
  useEffect(() => {
    if (subject !== undefined) {
      headingRef.current?.focus();
    }
  }, [subject]);
  return (
    <section aria-labelledby="channel-form-heading" className="card">
      <h2 id="channel-form-heading" ref={headingRef} tabIndex={-1}>
        {subject === undefined ? "Register a channel" : `Edit ${subject.name}`}
      </h2>
      <ChannelForm
        key={editing.formKey}
        channel={subject}
        pending={save.isPending}
        serverError={save.error}
        onSubmit={(input) => {
          setSaved(undefined);
          save.save(
            { id: subject?.id, input },
            {
              onSuccess: (channel) => {
                setSaved(channel.name);
                editing.stop();
              },
            },
          );
        }}
        onCancel={subject === undefined ? undefined : editing.stop}
      />
      <p role="status">{saved === undefined ? "" : `Saved ${saved}.`}</p>
    </section>
  );
}
