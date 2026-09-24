import { useId, type ReactNode } from "react";

export const WINDOW_CHOICES = [7, 30, 90, 365] as const;

export type WindowDays = (typeof WINDOW_CHOICES)[number];

export function isWindowDays(value: number): value is WindowDays {
  return (WINDOW_CHOICES as readonly number[]).includes(value);
}

// The viewer's own zone, as the browser reports it; UTC when it cannot.
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

export type LibraryChoice = Readonly<{
  serverId: string;
  id: string;
  name: string;
}>;

export type LibraryFilter = Readonly<{ serverId: string; id: string }>;

type StatsControlsProps = Readonly<{
  days: WindowDays;
  onDaysChange: (days: WindowDays) => void;
  servers: readonly { id: string; name: string }[];
  serverId: string | undefined;
  onServerChange: (id: string | undefined) => void;
  // Libraries with recorded watches; choosing one also selects its server.
  libraries: readonly LibraryChoice[];
  library: LibraryFilter | undefined;
  onLibraryChange: (library: LibraryFilter | undefined) => void;
  timeZone: string;
}>;

const LIBRARY_SEPARATOR = "|";

function libraryValue(library: LibraryFilter): string {
  return `${library.serverId}${LIBRARY_SEPARATOR}${library.id}`;
}

// The window and server a dashboard is computed for, and the zone its
// days and hours are in.
export function StatsControls({
  days,
  onDaysChange,
  servers,
  serverId,
  onServerChange,
  libraries,
  library,
  onLibraryChange,
  timeZone,
}: StatsControlsProps): ReactNode {
  const id = useId();
  return (
    <div className="filter stats-controls">
      <label htmlFor={`${id}-days`}>Window</label>
      <select
        id={`${id}-days`}
        value={String(days)}
        onChange={(event) => {
          const next = Number(event.target.value);
          if (isWindowDays(next)) {
            onDaysChange(next);
          }
        }}
      >
        {WINDOW_CHOICES.map((choice) => (
          <option key={choice} value={String(choice)}>
            Last {String(choice)} days
          </option>
        ))}
      </select>
      {servers.length > 1 || serverId !== undefined ? (
        <>
          <label htmlFor={`${id}-server`}>Server</label>
          <select
            id={`${id}-server`}
            value={serverId ?? ""}
            onChange={(event) => {
              onServerChange(
                event.target.value === "" ? undefined : event.target.value,
              );
            }}
          >
            <option value="">All servers</option>
            {servers.map((server) => (
              <option key={server.id} value={server.id}>
                {server.name}
              </option>
            ))}
          </select>
        </>
      ) : null}
      {libraries.length > 0 || library !== undefined ? (
        <>
          <label htmlFor={`${id}-library`}>Library</label>
          <select
            id={`${id}-library`}
            value={library === undefined ? "" : libraryValue(library)}
            onChange={(event) => {
              const chosen = libraries.find(
                (choice) =>
                  libraryValue({ serverId: choice.serverId, id: choice.id }) ===
                  event.target.value,
              );
              onLibraryChange(
                chosen === undefined
                  ? undefined
                  : { serverId: chosen.serverId, id: chosen.id },
              );
            }}
          >
            <option value="">All libraries</option>
            {libraries.map((choice) => (
              <option
                key={libraryValue({ serverId: choice.serverId, id: choice.id })}
                value={libraryValue({
                  serverId: choice.serverId,
                  id: choice.id,
                })}
              >
                {choice.name}
              </option>
            ))}
          </select>
        </>
      ) : null}
      <span className="row-detail">Days and hours in {timeZone}.</span>
    </div>
  );
}
