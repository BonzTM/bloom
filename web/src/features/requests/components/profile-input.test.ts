import { describe, expect, it } from "@jest/globals";
import type { DownloadManager } from "../api/download-manager-schemas.js";
import { firstInvalidProfileField, readProfileInput } from "./profile-input.js";

const radarr: DownloadManager = {
  id: "7d8e9f0a-0000-4000-8000-000000000001",
  kind: "radarr",
  name: "radarr-main",
  base_url: "https://radarr.example",
  allow_insecure: false,
  created_at: "2026-09-14T10:00:00Z",
  updated_at: "2026-09-14T10:00:00Z",
};

function form(
  fields: Readonly<Record<string, string | readonly string[]>>,
): FormData {
  const data = new FormData();
  for (const [name, value] of Object.entries(fields)) {
    for (const item of typeof value === "string" ? [value] : value) {
      data.append(name, item);
    }
  }
  return data;
}

const complete = {
  name: " Movies HD ",
  download_manager: radarr.id,
  quality_profile: "HD-1080p",
  root_folder: "/data/movies",
  tags: ["bloom", "4k"],
};

describe("readProfileInput", () => {
  it("derives the kind and instance from the chosen manager", () => {
    expect(readProfileInput(form(complete), [radarr])).toEqual({
      input: {
        name: "Movies HD",
        kinds: ["movie"],
        download_manager_kind: "radarr",
        download_manager_instance: "radarr-main",
        quality_profile: "HD-1080p",
        root_folder: "/data/movies",
        tags: ["bloom", "4k"],
      },
    });
  });

  it("allows no tags at all", () => {
    const result = readProfileInput(form({ ...complete, tags: [] }), [radarr]);
    expect(result.input?.tags).toEqual([]);
  });

  it("names every field that needs attention", () => {
    const result = readProfileInput(form({ name: "  ", tags: ["a", "a"] }), [
      radarr,
    ]);
    expect(result.errors).toEqual({
      name: "Enter the profile name.",
      download_manager: "Choose a download manager instance.",
      quality_profile: "Choose the quality profile.",
      root_folder: "Choose the root folder.",
      tags: "Each tag may appear only once.",
    });
    expect(firstInvalidProfileField(result.errors ?? {})).toBe("name");
  });

  it("refuses an instance that is not registered", () => {
    const result = readProfileInput(
      form({
        ...complete,
        download_manager: "7d8e9f0a-0000-4000-8000-0000000000ff",
      }),
      [radarr],
    );
    expect(result.errors).toEqual({
      download_manager: "Choose a registered download manager.",
    });
  });

  it("bounds a tag by bytes", () => {
    const result = readProfileInput(
      form({ ...complete, tags: ["é".repeat(51)] }),
      [radarr],
    );
    expect(result.errors?.tags).toBe("Use at most 100 bytes per tag.");
  });
});
