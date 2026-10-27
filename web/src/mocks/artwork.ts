import { http, HttpResponse } from "msw";

// Development-only posters. The mock titles name TMDB image paths that
// exist nowhere, so the browser worker answers them with a generated
// gradient instead of a broken image. Tests never load images, so only the
// browser worker uses this handler.

const PALETTES: readonly (readonly [string, string])[] = [
  ["#4338ca", "#0f172a"],
  ["#7c3aed", "#1e1b4b"],
  ["#0e7490", "#082f49"],
  ["#b45309", "#1c1917"],
  ["#be123c", "#1f0a14"],
  ["#15803d", "#052e16"],
];

function paletteFor(seed: string): readonly [string, string] {
  let hash = 0;
  for (let index = 0; index < seed.length && index < 64; index += 1) {
    hash = (hash * 31 + seed.charCodeAt(index)) % 9973;
  }
  return PALETTES[hash % PALETTES.length] ?? ["#4338ca", "#0f172a"];
}

function artwork(seed: string, wide: boolean): HttpResponse<string> {
  const [from, to] = paletteFor(seed);
  const [width, height] = wide ? [1280, 720] : [400, 600];
  const label = seed
    .replace(/[^a-z0-9]+/gi, " ")
    .trim()
    .slice(0, 18);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${String(width)}" height="${String(height)}" viewBox="0 0 ${String(width)} ${String(height)}"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="${from}"/><stop offset="1" stop-color="${to}"/></linearGradient></defs><rect width="100%" height="100%" fill="url(#g)"/><text x="50%" y="88%" fill="rgba(255,255,255,0.55)" font-family="sans-serif" font-size="${wide ? "56" : "34"}" font-weight="700" text-anchor="middle">${label}</text></svg>`;
  return new HttpResponse(svg, {
    headers: { "Content-Type": "image/svg+xml" },
  });
}

// Studio and network logos are white wordmarks on a clear background, the
// way TMDB's duotone filter returns them.
function logo(seed: string): HttpResponse<string> {
  const label = seed
    .replace(/\.png$/i, "")
    .replace(/[^a-z0-9]+/gi, "")
    .slice(0, 6)
    .toUpperCase();
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="500" height="200" viewBox="0 0 500 200"><text x="50%" y="62%" fill="#ffffff" font-family="sans-serif" font-size="88" font-weight="800" text-anchor="middle">${label}</text></svg>`;
  return new HttpResponse(svg, {
    headers: { "Content-Type": "image/svg+xml" },
  });
}

export const artworkHandlers = [
  http.get("https://image.tmdb.org/t/p/:size/:file", ({ params }) =>
    String(params.size).includes("_filter(")
      ? logo(String(params.file))
      : artwork(String(params.file), params.size === "w1280"),
  ),
];
