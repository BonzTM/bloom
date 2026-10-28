import type { ReactNode } from "react";
import type { MetadataDetails, TitleRatings } from "../api/metadata-schemas.js";
import { formatScore, formatVotes } from "./detail-format.js";

type Score = Readonly<{ label: string; value: string; note: string }>;

type TitleScoresProps = Readonly<{
  details: Pick<MetadataDetails, "vote_average" | "vote_count">;
  // Undefined while the outside scores load, or when they failed.
  ratings: TitleRatings | undefined;
}>;

// The scores a Seerr title page shows above its facts: TMDB's user score,
// then IMDb, Rotten Tomatoes, and Metacritic when a source knows them.
export function TitleScores({ details, ratings }: TitleScoresProps): ReactNode {
  const scores = collectScores(details, ratings);
  if (scores.length === 0) {
    return null;
  }
  return (
    <ul className="title-scores" aria-label="Scores">
      {scores.map((score) => (
        <li key={score.label}>
          <span className="title-score-value">{score.value}</span>{" "}
          <span className="title-score-label">{score.label}</span>
          {score.note === "" ? null : (
            <span className="title-score-note"> {score.note}</span>
          )}
        </li>
      ))}
    </ul>
  );
}

function collectScores(
  details: TitleScoresProps["details"],
  ratings: TitleRatings | undefined,
): Score[] {
  const scores: Score[] = [];
  if (details.vote_count > 0) {
    scores.push({
      label: "TMDB",
      value: formatScore(details.vote_average),
      note: "user score",
    });
  }
  if (ratings?.imdb !== undefined) {
    scores.push({
      label: "IMDb",
      value: ratings.imdb.score.toFixed(1),
      note:
        ratings.imdb.votes > 0
          ? `${formatVotes(ratings.imdb.votes)} votes`
          : "",
    });
  }
  if (ratings?.rotten_tomatoes !== undefined) {
    scores.push({
      label: "Rotten Tomatoes",
      value: `${String(Math.round(ratings.rotten_tomatoes.score))}%`,
      note: "critics",
    });
  }
  if (ratings?.metacritic !== undefined) {
    scores.push({
      label: "Metacritic",
      value: String(Math.round(ratings.metacritic.score)),
      note: "",
    });
  }
  return scores;
}
