import { useId, useState, type ReactNode, type SyntheticEvent } from "react";
import { AsyncStatus } from "../../../components/async-status.js";
import type {
  MediaServerExclusions,
  ReplaceExclusionsInput,
} from "../api/media-servers-schemas.js";

export type Choice = Readonly<{ id: string; name: string }>;

type ExclusionsFormProps = Readonly<{
  stored: MediaServerExclusions;
  users: readonly Choice[];
  libraries: readonly Choice[];
  pending: boolean;
  saved: boolean;
  errorSummary: string | undefined;
  onSubmit: (input: ReplaceExclusionsInput) => void;
}>;

const MAX_TOTAL = 500;

// Two checkbox lists, one for people and one for libraries. An id the
// server stored but the lists no longer offer stays checked under its id,
// so saving never silently drops it.
export function ExclusionsForm({
  stored,
  users,
  libraries,
  pending,
  saved,
  errorSummary,
  onSubmit,
}: ExclusionsFormProps): ReactNode {
  const id = useId();
  const [tooMany, setTooMany] = useState(false);
  function handleSubmit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const input = {
      excluded_media_user_ids: data.getAll("user").map(String),
      excluded_library_ids: data.getAll("library").map(String),
    };
    const total =
      input.excluded_media_user_ids.length + input.excluded_library_ids.length;
    setTooMany(total > MAX_TOTAL);
    if (total > MAX_TOTAL) {
      return;
    }
    onSubmit(input);
  }
  return (
    <form
      className="exclusions-form"
      aria-label="Exclusions"
      onSubmit={handleSubmit}
      noValidate
    >
      <ChoiceList
        legend="People left out"
        name="user"
        idPrefix={`${id}-user`}
        choices={withStored(users, stored.excluded_media_user_ids)}
        checked={stored.excluded_media_user_ids}
        emptyText="This server has no known people yet."
      />
      <ChoiceList
        legend="Libraries left out"
        name="library"
        idPrefix={`${id}-library`}
        choices={withStored(libraries, stored.excluded_library_ids)}
        checked={stored.excluded_library_ids}
        emptyText="This server has no known libraries yet."
      />
      <p className="field-hint">
        Excluded people and libraries are left out of collection, imports,
        statistics, and the catalog. Existing rows stay in the database.
      </p>
      <div className="form-actions">
        <button type="submit" className="btn-primary" disabled={pending}>
          {pending ? "Saving…" : "Save exclusions"}
        </button>
      </div>
      <p role="alert" className="field-error">
        {tooMany
          ? `Choose at most ${String(MAX_TOTAL)} exclusions in total.`
          : (errorSummary ?? "")}
      </p>
      <AsyncStatus>{saved ? "Exclusions saved." : ""}</AsyncStatus>
    </form>
  );
}

function withStored(
  choices: readonly Choice[],
  stored: readonly string[],
): readonly Choice[] {
  const known = new Set(choices.map((choice) => choice.id));
  const extra = stored
    .filter((value) => !known.has(value))
    .map((value) => ({ id: value, name: value }));
  return [...choices, ...extra];
}

function ChoiceList({
  legend,
  name,
  idPrefix,
  choices,
  checked,
  emptyText,
}: Readonly<{
  legend: string;
  name: string;
  idPrefix: string;
  choices: readonly Choice[];
  checked: readonly string[];
  emptyText: string;
}>): ReactNode {
  return (
    <fieldset className="field">
      <legend>{legend}</legend>
      {choices.length === 0 ? (
        <p className="field-hint">{emptyText}</p>
      ) : (
        choices.map((choice, index) => (
          <label
            key={choice.id}
            className="choice"
            htmlFor={`${idPrefix}-${String(index)}`}
          >
            <input
              id={`${idPrefix}-${String(index)}`}
              type="checkbox"
              name={name}
              value={choice.id}
              defaultChecked={checked.includes(choice.id)}
            />
            {choice.name}
          </label>
        ))
      )}
    </fieldset>
  );
}
