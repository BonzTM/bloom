import type { FilteredDiscoverList } from "../api/metadata-schemas.js";

// A studio or network tile: TMDB's identifier, its name, and its logo on
// TMDB's image host, filtered to white so it reads on a dark tile.
export type Brand = Readonly<{ id: number; name: string; logo: string }>;

const LOGO_ORIGIN =
  "https://image.tmdb.org/t/p/w780_filter(duotone,ffffff,bababa)";

function brand(id: number, name: string, file: string): Brand {
  return { id, name, logo: `${LOGO_ORIGIN}/${file}` };
}

// The studios and networks Seerr offers on its discover page, with the same
// TMDB identifiers and logo files (github.com/seerr-team/seerr,
// src/components/Discover/StudioSlider and NetworkSlider, MIT licensed).
export const STUDIOS: readonly Brand[] = [
  brand(2, "Disney", "wdrCwmRnLFJhEoH8GSfymY85KHT.png"),
  brand(127928, "20th Century Studios", "h0rjX5vjW5r8yEnUBStFarjcLT4.png"),
  brand(34, "Sony Pictures", "GagSvqWlyPdkFHMfQ3pNq6ix9P.png"),
  brand(174, "Warner Bros. Pictures", "ky0xOc5OrhzkZ1N6KyUxacfQsCk.png"),
  brand(33, "Universal", "8lvHyhjr8oUKOOy2dKXoALWKdp0.png"),
  brand(4, "Paramount", "fycMZt242LVjagMByZOLUGbCvv3.png"),
  brand(3, "Pixar", "1TjvGVDMYsj6JBxOAkUHpPEwLf7.png"),
  brand(521, "DreamWorks", "kP7t6RwGz2AvvTkvnI1uteEwHet.png"),
  brand(420, "Marvel Studios", "hUzeosd33nzE5MCNsZxCGEKTXaQ.png"),
  brand(9993, "DC", "2Tc1P3Ac8M479naPp1kYT3izLS5.png"),
  brand(41077, "A24", "1ZXsGaFPgrgS6ZZGS37AqD5uU12.png"),
];

export const NETWORKS: readonly Brand[] = [
  brand(213, "Netflix", "wwemzKWzjKYJFfCeiB57q3r4Bcm.png"),
  brand(2739, "Disney+", "gJ8VX6JSu3ciXHuC2dDGAo2lvwM.png"),
  brand(1024, "Prime Video", "ifhbNuuVnlwYy5oXA5VIb2YR8AZ.png"),
  brand(2552, "Apple TV+", "4KAy34EHvRM25Ih8wb82AuGU7zJ.png"),
  brand(453, "Hulu", "pqUTCleNUiTLAVlelGxUgWn1ELh.png"),
  brand(49, "HBO", "tuomPhY2UtuPTqqFnKMVHvSb724.png"),
  brand(4353, "Discovery+", "1D1bS3Dyw4ScYnFWTlBOvJXC3nb.png"),
  brand(2, "ABC", "ndAvF4JLsliGreX87jAc9GdjmJY.png"),
  brand(19, "FOX", "1DSpHrWyOORkL9N2QHX7Adt31mQ.png"),
  brand(359, "Cinemax", "6mSHSquNpfLgDdv6VnOOvC5Uz2h.png"),
  brand(174, "AMC", "pmvRmATOCaDykE6JrVoeYxlFHw3.png"),
  brand(67, "Showtime", "Allse9kbjiP6ExaQrnSpIhkurEi.png"),
  brand(318, "Starz", "8GJjw3HHsAJYwIWKIPBPfqMxlEa.png"),
  brand(71, "The CW", "ge9hzeaU7nMtQ4PjkFlc68dGAJ9.png"),
  brand(6, "NBC", "o3OedEP0f9mfZr33jz2BfXOUK5.png"),
  brand(16, "CBS", "nm8d7P7MJNiBLdgIzUK0gkuEA4r.png"),
  brand(4330, "Paramount+", "fi83B1oztoS47xxcemFdPMhIzK.png"),
  brand(4, "BBC One", "mVn7xESaTNmjBUyUtGNvDQd3CT1.png"),
  brand(56, "Cartoon Network", "c5OC6oVCg6QP4eqzW6XIq17CQjI.png"),
  brand(80, "Adult Swim", "9AKyspxVzywuaMuZ1Bvilu8sXly.png"),
  brand(13, "Nickelodeon", "ikZXxg6GnwpzqiZbRPhJGaZapqB.png"),
  brand(3353, "Peacock", "gIAcGTjKKr0KOHL5s4O36roJ8p7.png"),
];

// Which list a brand's tile opens: a studio's movies or a network's series.
export const BRAND_LISTS = {
  studios: "movies/studios",
  networks: "series/networks",
} as const satisfies Readonly<Record<string, FilteredDiscoverList>>;

export function brandName(
  list: FilteredDiscoverList,
  id: number,
): string | undefined {
  const brands = list === "movies/studios" ? STUDIOS : NETWORKS;
  return brands.find((candidate) => candidate.id === id)?.name;
}
