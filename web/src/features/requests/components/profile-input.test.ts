import { describe, expect, it } from "@jest/globals";
import { firstInvalidProfileField, readProfileInput } from "./profile-input.js";

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
  kinds: ["movie", "series"],
  download_manager_kind: "radarr",
  download_manager_instance: "radarr-main",
  quality_profile: "HD-1080p",
  root_folder: "/data/movies",
  tags: " bloom, family ,,",
};

describe("readProfileInput", () => {
  it("trims text and splits comma-separated tags", () => {
    expect(readProfileInput(form(complete))).toEqual({
      input: {
        name: "Movies HD",
        kinds: ["movie", "series"],
        download_manager_kind: "radarr",
        download_manager_instance: "radarr-main",
        quality_profile: "HD-1080p",
        root_folder: "/data/movies",
        tags: ["bloom", "family"],
      },
    });
  });

  it("allows no tags at all", () => {
    const result = readProfileInput(form({ ...complete, tags: "" }));
    expect(result.input?.tags).toEqual([]);
  });

  it("names every field that needs attention", () => {
    const result = readProfileInput(
      form({ name: "  ", kinds: [], tags: "a, a" }),
    );
    expect(result.errors).toEqual({
      name: "Enter the profile name.",
      kinds: "Choose at least one kind of media this profile accepts.",
      download_manager_kind: "Enter the download manager kind.",
      download_manager_instance: "Enter the download manager instance.",
      quality_profile: "Enter the quality profile.",
      root_folder: "Enter the root folder.",
      tags: "Each tag may appear only once.",
    });
    expect(firstInvalidProfileField(result.errors ?? {})).toBe("name");
  });

  it("bounds a tag by bytes", () => {
    const result = readProfileInput(
      form({ ...complete, tags: "é".repeat(51) }),
    );
    expect(result.errors?.tags).toBe("Use at most 100 bytes per tag.");
  });
});
