# Seerr (Jellyseerr / Overseerr) research

Research date: 2026-09-21. Purpose: inventory what Seerr does so Bloom (Go) can mirror it with pluggable media servers and integrations.

Repo file paths below refer to `seerr-team/seerr` on the `develop` branch, i.e. `https://github.com/seerr-team/seerr/blob/develop/<path>`. Issue numbers prefixed `seerr#` are in `seerr-team/seerr` (the former `fallenbagel/jellyseerr`; that URL now redirects). `overseerr#` are in `sct/overseerr`.

---

## 1. What Jellyseerr, Overseerr and Seerr are

### Identity and merge status

- **Seerr** is the unified successor of Overseerr and Jellyseerr. Announcement (2026-02-10): "the Jellyseerr and Overseerr teams are officially merging into a single team called Seerr." and "For users, this means one shared codebase combining all existing Overseerr functionalities with the latest Jellyseerr features, along with Jellyfin and Emby support". Source: https://docs.seerr.dev/blog/seerr-release/
- The Jellyseerr repo was renamed: `gh api repos/seerr-team/seerr` reports `full_name = seerr-team/seerr`, created 2022-03-09 (the original Jellyseerr creation date); `fallenbagel/jellyseerr` redirects there. Description: "Open-source media request and discovery manager for Jellyfin, Plex, and Emby." Source: https://github.com/seerr-team/seerr
- Seerr v3.0.0 (2026-02-14) is the first unified release; its notes include "Overseerr to Jellyseerr migration (#2019)" and "Rebrand Jellyseerr logos to Seerr (#2406)". Source: https://github.com/seerr-team/seerr/releases/tag/v3.0.0
- Overseerr is **archived** (`isArchived: true`, last push 2026-02-15). Its README now says: "Overseerr is being superseded by Seerr, a unified project merging Overseerr and Jellyseerr. This repository will no longer receive new features or major updates." Final release v1.35.0 (2026-02-15) contains "prepare seerr migration (#4339)". Sources: https://github.com/sct/overseerr , https://github.com/sct/overseerr/releases/tag/v1.35.0
- Before the merge, Overseerr's maintainer described it as "partially paused": "Active development is very, very slow ... So, no, Overseerr is not dead. It's just partially paused" (2024-07-15). Source: https://github.com/sct/overseerr/issues/3898
- Migration is automatic: "Whether you come from Overseerr or Jellyseerr, you don't need to perform any manual migration steps, your instance will automatically be migrated to Seerr." Docker image is `ghcr.io/seerr-team/seerr` (also `seerr/seerr` on Docker Hub); container "can now be run as a non-root user (`node` user)" and needs `init: true`. Source: https://docs.seerr.dev/migration-guide/
- Post-merge: v3.1.0 (2026-02-28) fixed CVE-2026-27707 ("Unauthenticated Account Registration via Jellyfin Endpoint", affecting Plex-configured instances), CVE-2026-27793 (profile endpoint exposing other users' "webhook URLs, Telegram tokens, and similar sensitive configuration") and CVE-2026-27792 ("Missing Authentication on Push Subscription Endpoints"), and announced "the end of our post-merger feature freeze". Source: https://docs.seerr.dev/blog/seerr-3-1-0-security-release/

### Stack

| Item | Value | Source |
|---|---|---|
| Language | TypeScript (frontend + backend) | https://github.com/seerr-team/seerr (primaryLanguage) |
| Frontend | Next.js 16.2.6, React 19.2.6, Tailwind | `package.json` https://github.com/seerr-team/seerr/blob/develop/package.json |
| Backend | Express 5.2.1, TypeORM 0.3.31, node-schedule 2.1.1, nodemailer 8.0.5, web-push 3.6.7 | `package.json` |
| Runtime | Node `^22.19.0`, pnpm `^10` | `package.json` `engines` |
| Database | SQLite (`sqlite3`) or PostgreSQL (`pg`); README: "Support for **PostgreSQL** and **SQLite** databases." | README; `server/migration/sqlite` (55 files), `server/migration/postgres` (20 files) |
| Settings | JSON file `config/settings.json` (`SETTINGS_PATH` in `server/lib/settings/index.ts`), with its own settings-migration chain in `server/lib/settings/migrations` | https://github.com/seerr-team/seerr/blob/develop/server/lib/settings/index.ts |
| Sessions | express-session persisted via `connect-typeorm` (`TypeormStore`) | `server/index.ts` lines ~220-230 |
| API | OpenAPI spec `seerr-api.yml`, 168 paths, served at `/api-docs`; docs site publishes 212 endpoint pages | https://github.com/seerr-team/seerr/blob/develop/seerr-api.yml ; https://docs.seerr.dev/sitemap.xml |
| Tests | Cypress e2e (`cypress/e2e`), server tests via `node server/test/index.mts` | `package.json` scripts |
| i18n | 41 locale JSON files in `src/i18n/locale`; translations via Weblate ("We use Weblate for our translations") | `CONTRIBUTING.md` line 195 |
| Packaging | Docker, Helm chart (`charts/`), Nix, AUR/Synology/TrueNAS/Unraid third-party | https://docs.seerr.dev/getting-started/ |
| License | MIT (both repos) | GitHub `licenseInfo` |

### Activity (2026-09-21)

- seerr-team/seerr: 12,652 stars, 1,038 forks, last push 2026-09-22; releases v3.0.0 (Feb) → v3.4.1 (2026-07-30); milestones v3.5.0 (open, 5 open / 64 closed) and v3.6.0 open. Sources: `gh repo view`, https://github.com/seerr-team/seerr/releases , https://github.com/seerr-team/seerr/milestones
- Issues: 1,556 total / 250 open (seerr); 1,604 total / 108 open (overseerr, archived). Source: GitHub search API `repo:... is:issue`.
- Discussions: 366 in seerr-team/seerr. Source: GraphQL `discussions.totalCount`.
- Roadmap issue: "Here's the Milestones page for Seerr, where each milestone corresponds to a planned release. No ETAs are provided we're all volunteers". Source: https://github.com/seerr-team/seerr/issues/926

### Supported media servers

- README: "It integrates with the media server of your choice: Jellyfin, Plex, and Emby." Only one at a time: "Added support for Jellyfin and Emby as alternatives to Plex. Only one integration can be used at a time." Sources: https://github.com/seerr-team/seerr#readme ; https://docs.seerr.dev/blog/seerr-release/
- Code: `MediaServerType { PLEX=1, JELLYFIN, EMBY, NOT_CONFIGURED }` in `server/constants/server.ts`; Emby is handled by the Jellyfin client (`server/api/jellyfin.ts`) with `ServerType.EMBY`. Users carry `UserType { PLEX=1, LOCAL=2, JELLYFIN=3, EMBY=4 }` (`server/constants/user.ts`).
- An in-app Plex ⇄ Jellyfin/Emby switch is in preview (PR #2539, open): "adds an in-app way to switch a single Seerr instance between media servers (Plex <=> Jellyfin/Emby) without losing users, permissions, requests, or general settings." Source: https://github.com/seerr-team/seerr/discussions/2792

---

## 2. Feature inventory

### Discovery / browsing

- Home page is built from ordered "discover sliders" that admins can enable, reorder and extend. Built-in types: `RECENTLY_ADDED, RECENT_REQUESTS, PLEX_WATCHLIST, TRENDING, POPULAR_MOVIES, MOVIE_GENRES, UPCOMING_MOVIES, STUDIOS, POPULAR_TV, TV_GENRES, UPCOMING_TV, NETWORKS`; custom slider types `TMDB_MOVIE_KEYWORD, TMDB_MOVIE_GENRE, TMDB_TV_KEYWORD, TMDB_TV_GENRE, TMDB_SEARCH, TMDB_STUDIO, TMDB_NETWORK, TMDB_MOVIE_STREAMING_SERVICES, TMDB_TV_STREAMING_SERVICES`. Source: https://github.com/seerr-team/seerr/blob/develop/server/constants/discover.ts ; entity `server/entity/DiscoverSlider.ts`; API "Add a new slider", "Batch update all sliders" https://docs.seerr.dev/api/add-a-new-slider/
- Discover endpoints: movies, TV, by genre, by studio, by network, by original language, upcoming TV, trending, keyword, genre slider data, recommended/similar. Source: docs API pages e.g. https://docs.seerr.dev/api/discover-movies-by-studio/ , https://docs.seerr.dev/api/discover-tv-shows-by-network/ , https://docs.seerr.dev/api/get-recommended-movies/
- Trending filters (v3.2/3.3): "Trending page filters for movies vs. TV shows (daily/weekly options)". Source: https://docs.seerr.dev/blog/seerr-3-2-0-and-3-3-0-release-notes/
- Collections and people: "Get collection details", "Get person details", "Get combined credits"; person pages got "Direct IMDb and TMDB links". Sources: https://docs.seerr.dev/api/get-collection-details/ , https://docs.seerr.dev/api/get-person-details/ , 3.2/3.3 blog.
- Ratings: Rotten Tomatoes (scraped via Algolia index) and IMDb (via Radarr's proxy `api.radarr.video/v1`): "Get RT and IMDB movie ratings combined". Source: `server/api/rating/rottentomatoes.ts`, `server/api/rating/imdbRadarrProxy.ts`, https://docs.seerr.dev/api/get-rt-and-imdb-movie-ratings-combined/
- Region/language filtering: "Discover Region, Discover Language & Streaming Region ... filter content shown on the 'Discover' home page based on regional availability and original language ... Users can override these global settings". Source: https://docs.seerr.dev/using-seerr/settings/general/
- Hide available / hide requested / hide blocklisted toggles ("Hide Available Media", "Hide Requested Media", "Hide Blocklisted Items"). Source: same page.
- Recently added slider and "View Recently Added" permission. Source: `PermissionEdit` messages `viewrecent`.

### Search

- TMDB search for movies, TV, people; special prefixes parsed in `server/lib/search.ts`: `tmdb:<id>`, `imdb:<tt|nm id>`, `tvdb:<id>`, `year:<yyyy>` (regex patterns at lines 46, 94, 132, 170). Source: https://github.com/seerr-team/seerr/blob/develop/server/lib/search.ts
- "Available media will still appear in search results" even when hidden from Discover. Source: https://docs.seerr.dev/using-seerr/settings/general/

### Requests

- Movies and TV; TV at **season** granularity: "Customizable request system, which allows users to request individual seasons or movies". Source: README.
- Partial-season toggle: "Allow Partial Series Requests ... If disabled, users will only be able to submit requests for all unavailable seasons." Source: https://docs.seerr.dev/using-seerr/settings/general/
- Specials: setting "Disable special seasons: Adds a setting to prevent special seasons from being shown or requested." (`enableSpecialEpisodes` in `MainSettings`). Source: https://docs.seerr.dev/blog/seerr-release/
- **No episode-level requests** (open ask, seerr#264, overseerr#342). Source: https://github.com/seerr-team/seerr/issues/264
- 4K is a separate parallel flow: `Media.status` and `Media.status4k`, `MediaRequest.is4k`, separate default 4K Radarr/Sonarr servers ("If you have separate 4K Radarr/Sonarr servers, you need to designate default 4K servers in addition to default non-4K servers."). Sources: `server/entity/Media.ts`, https://docs.seerr.dev/using-seerr/settings/services/
- Request statuses `PENDING, APPROVED, DECLINED, FAILED, COMPLETED`; media statuses `UNKNOWN, PENDING, PROCESSING, PARTIALLY_AVAILABLE, AVAILABLE, BLOCKLISTED, DELETED`. Source: https://github.com/seerr-team/seerr/blob/develop/server/constants/media.ts
- Advanced request options per request: `serverId, profileId, rootFolder, languageProfileId, tags` on `MediaRequest` (needs "Advanced Requests" permission: "Grant permission to modify advanced media request options."). Source: `server/entity/MediaRequest.ts`; `src/components/PermissionEdit/index.tsx`
- Auto-approve via permissions (see below); "Manage Requests" also auto-approves: "All requests made by a user with this permission will be automatically approved." Source: `PermissionEdit`.
- Request on behalf of other users (Manage Users): "that permission also grants the ability to submit requests on behalf of other users." Source: https://docs.seerr.dev/using-seerr/settings/users/
- Request API: list, create, count (with "completed count" added in 3.0.0), get, update, delete, approve/decline (`requestRoutes.post` at lines 640/677 of `server/routes/request.ts`). Source: https://docs.seerr.dev/api/create-new-request/
- Download progress from Radarr/Sonarr queues is tracked and shown (`server/lib/downloadtracker.ts` polls `radarr.getQueue()` / `sonarr.getQueue()`; jobs `download-sync`, `download-sync-reset`).
- Availability sync job removes/downgrades media deleted from the server or *arr (`server/lib/availabilitySync.ts`, job `availability-sync`).
- Retry failed requests: `POST /request/{requestId}/retry` and approve/decline via `POST /request/{requestId}/{status}` (`server/routes/request.ts` lines 640-683). Source: https://docs.seerr.dev/api/retry-failed-request/

### Request quotas

- Global "Global Movie Request Limit & Global Series Request Limit ... Unless an override is configured, users are granted these global request limits. Note that users with the Manage Users permission are exempt". Per-user override with "Enable Override". Sources: https://docs.seerr.dev/using-seerr/settings/users/ , https://docs.seerr.dev/using-seerr/users/editing-users/
- Model: `movieQuotaLimit/movieQuotaDays/tvQuotaLimit/tvQuotaDays` on `User`; TV quota counts **seasons** ("Count tv season requests made during quota period"); declined requests are excluded; `MediaRequest.ignoreQuota` flag. Source: https://github.com/seerr-team/seerr/blob/develop/server/entity/User.ts (lines ~283-373)
- 3.2/3.3: "Unlimited time quota reset capability for global per-user requests". Source: 3.2/3.3 blog.

### Permissions (bitmask, `server/lib/permissions.ts`)

Values and UI descriptions (from `src/components/PermissionEdit/index.tsx`):

| Permission | Bit | Grants |
|---|---|---|
| ADMIN | 2 | "Full administrator access. Bypasses all other permission checks." |
| MANAGE_SETTINGS | 4 | Route guard for settings pages (`useRouteGuard(Permission.MANAGE_SETTINGS)` in `src/pages/settings/jellyfin.tsx`); not exposed in the PermissionEdit UI list |
| MANAGE_USERS | 8 | "Grant permission to manage users. Users with this permission cannot modify users with or grant the Admin privilege." |
| MANAGE_REQUESTS | 16 | "Grant permission to manage media requests. All requests made by a user with this permission will be automatically approved." |
| REQUEST | 32 | "Grant permission to submit requests for non-4K media." |
| VOTE | 64 | In enum only; no other reference in `server/` or `src/` (unused) |
| AUTO_APPROVE | 128 | "Grant automatic approval for all non-4K media requests." |
| AUTO_APPROVE_MOVIE / _TV | 256 / 512 | per-type auto-approve |
| REQUEST_4K | 1024 | "Grant permission to submit requests for 4K media." |
| REQUEST_4K_MOVIE / _TV | 2048 / 4096 | per-type 4K request |
| REQUEST_ADVANCED | 8192 | "Grant permission to modify advanced media request options." |
| REQUEST_VIEW | 16384 | "Grant permission to view media requests submitted by other users." |
| AUTO_APPROVE_4K (+MOVIE/TV) | 32768 / 65536 / 131072 | 4K auto-approve |
| REQUEST_MOVIE / REQUEST_TV | 262144 / 524288 | per-type request |
| MANAGE_ISSUES | 1048576 | "Grant permission to manage media issues." |
| VIEW_ISSUES | 2097152 | "Grant permission to view media issues reported by other users." |
| CREATE_ISSUES | 4194304 | "Grant permission to report media issues." |
| AUTO_REQUEST (+MOVIE/TV) | 8388608 / 16777216 / 33554432 | "Grant permission to automatically submit requests for non-4K media via Plex Watchlist." |
| RECENT_VIEW | 67108864 | "Grant permission to view the list of recently added media." |
| WATCHLIST_VIEW | 134217728 | "View {mediaServerName} Watchlists" |
| MANAGE_BLOCKLIST | 268435456 | "Grant permission to manage blocklisted media." |
| VIEW_BLOCKLIST | 1073741824 | "Grant permission to view blocklisted media." |

Source: https://github.com/seerr-team/seerr/blob/develop/server/lib/permissions.ts ; https://github.com/seerr-team/seerr/blob/develop/src/components/PermissionEdit/index.tsx. Default permissions for new users are configurable ("Default Permissions"), bulk edit exists ("Bulk Edit"). Sources: settings/users docs, editing-users docs.

### User management

- Import from media server: "Clicking the **Import Jellyfin Users** button ... will fetch the list of users with access to the Jellyfin server and add them" (same for Emby, Plex). Routes `/import-from-plex`, `/import-from-jellyfin` in `server/routes/user/index.ts`. Source: https://docs.seerr.dev/using-seerr/users/adding-users/
- Auto-provisioning on first login: "Enable New Jellyfin/Emby/Plex Sign-In ... users with access to your media server will be able to sign in to Seerr even if they have not yet been imported." Source: https://docs.seerr.dev/using-seerr/settings/users/
- Local users: "Create Local User" with email + password (min 8 chars) or auto-generated password emailed. Source: adding-users docs.
- Per-user settings: display name, email, language, discover region/language, quotas, notification settings, avatar. Source: editing-users docs; `server/entity/UserSettings.ts`.
- User list sorting (3.2/3.3): "User list sorting by name, email, role, requests, and other criteria". Source: 3.2/3.3 blog.

### Authentication methods

- **Plex**: PIN-based plex.tv OAuth from the browser (`https://plex.tv/api/v2/pins?strong=true` in `src/utils/plex.ts`), token posted to `POST /auth/plex`, verified with `PlexTvAPI.getUser()` and `checkUserAccess`. Source: `server/routes/auth.ts` line 58; `server/api/plextv.ts`.
- **Jellyfin/Emby**: username/password `POST /auth/jellyfin`; **Jellyfin Quick Connect** (`/auth/jellyfin/quickconnect/initiate`, `/check`), shipped (seerr#1595 closed). Source: `server/routes/auth.ts` lines 235-704; https://docs.seerr.dev/api/authenticate-with-quick-connect/
- **Local**: `POST /auth/local` email+password; password reset by email (`/auth/reset-password`). Setting "Enable Local Sign-In". Source: `server/routes/auth.ts`; settings/users docs.
- **API key**: "Sign-in is also possible by passing an `X-Api-Key` header along with a valid API Key generated by Seerr." Single admin-level key; settable via `API_KEY` env. Sources: `seerr-api.yml` header; https://docs.seerr.dev/using-seerr/settings/general/
- **Cookie sessions** stored in DB; optional CSRF ("Enable CSRF Protection ... HTTPS is required"). Source: https://docs.seerr.dev/using-seerr/settings/network/
- **OIDC/SSO: not shipped.** The top-voted issue (seerr#183, 276 reactions) is open; PR #2715 "feat: initial support for OpenID Connect authentication" is open (updated 2026-09-14) with a preview image (`preview-new-oidc`): "This feature is still experimental." Configuration "Must be configured in `settings.json`" in that PR. Sources: https://github.com/seerr-team/seerr/pull/2715 , https://github.com/seerr-team/seerr/discussions/2721
- Forward-auth (seerr#1221) and LDAP are open asks. Source: https://github.com/seerr-team/seerr/issues/1221
- Only one media-server auth source at a time (seerr#100 open). Source: https://github.com/seerr-team/seerr/issues/100

### Notifications

Agents (`NotificationAgentKey` in `server/lib/settings/index.ts`; docs at https://docs.seerr.dev/using-seerr/notifications/):

| Agent | Config fields (from `server/lib/settings/index.ts`) |
|---|---|
| Discord | `webhookUrl, botUsername, botAvatarUrl, webhookRoleId, webhookThreadId, enableMentions, locale, useUserLocale` |
| Email | SMTP `smtpHost, smtpPort, secure, ignoreTls, requireTls, authUser, authPass, allowSelfSigned, emailFrom, senderName, usePublicLogo, pgpPrivateKey, pgpPassword, userEmailRequired` (per-user PGP key in `UserSettings.pgpKey`) |
| Gotify | `url, token, priority, locale` |
| ntfy | `url, topic, tags, username/password or token, priority, locale` |
| Pushbullet | `accessToken, channelTag` (per-user token) |
| Pushover | `accessToken, userToken, sound` (per-user token/key/sound) |
| Slack | `webhookUrl, locale` |
| Telegram | `botAPI, chatId, messageThreadId, sendSilently` (per-user chat id/thread) |
| Webhook | `webhookUrl, jsonPayload (template), authHeader, customHeaders[], supportVariables` |
| Web Push | VAPID keys in settings; per-user `UserPushSubscription` |

- Shared option `embedPoster` ("images in notifications are now optional"). Source: https://docs.seerr.dev/blog/seerr-release/
- Webhook template variables `{{notification_type}}, {{event}}, {{subject}}, {{message}}, {{image}}, {{notifyuser_*}}` plus request/media/issue vars; "Custom Headers"; "dynamic placeholder support in webhook URLs" (experimental). Sources: https://docs.seerr.dev/using-seerr/notifications/webhook/ , release blog.
- Server-side i18n for agents (3.2/3.3): "Server-side internationalization (i18n) for all notification agents". Source: 3.2/3.3 blog.
- Web Push: "require a secure connection"; "For Web Push notifications to work on mobile you need to add Seerr to your home screen as progressive web app (PWA)." Source: https://docs.seerr.dev/using-seerr/notifications/webpush/
- **LunaSea** existed in Overseerr (`server/lib/notifications/agents/lunasea.ts` in sct/overseerr) and was removed in Seerr by settings migration `0006_remove_lunasea.ts`. Sources: https://github.com/sct/overseerr/tree/develop/server/lib/notifications/agents , https://github.com/seerr-team/seerr/blob/develop/server/lib/settings/migrations/0006_remove_lunasea.ts
- Not supported (open asks): Apprise (seerr#2270), Signal (seerr#1830), multiple instances of one agent (seerr#804).

Event types (`Notification` enum, `server/lib/notifications/index.ts`): `MEDIA_PENDING(2), MEDIA_APPROVED(4), MEDIA_AVAILABLE(8), MEDIA_FAILED(16), TEST_NOTIFICATION(32), MEDIA_DECLINED(64), MEDIA_AUTO_APPROVED(128), ISSUE_CREATED(256), ISSUE_COMMENT(512), ISSUE_RESOLVED(1024), ISSUE_REOPENED(2048), MEDIA_AUTO_REQUESTED(4096)`. Users pick per-agent type bitmasks (`UserSettings.notificationTypes`). Source: https://github.com/seerr-team/seerr/blob/develop/server/lib/notifications/index.ts

### Issues (media problem reports)

- Types `VIDEO, AUDIO, SUBTITLES, OTHER`; status `OPEN, RESOLVED`; optional `problemSeason`/`problemEpisode`; threaded comments. Source: https://github.com/seerr-team/seerr/blob/develop/server/constants/issue.ts , `server/entity/Issue.ts`, `server/entity/IssueComment.ts`
- API: list, create, count, get, comment, set status, delete (`server/routes/issue.ts`). "Add issue description preview (#1881)" in 3.0.0.

### Watchlists

- Local per-user watchlist (`server/entity/Watchlist.ts`; `POST /watchlist`, `DELETE /watchlist/{tmdbId}`). Source: https://docs.seerr.dev/api/add-media-to-watchlist/
- Plex Watchlist sync and auto-request: "allows Seerr to automatically create requests for media items you add to your Plex Watchlist"; "only available for Plex users"; "Auto-request only works for standard quality content". Implemented in `server/lib/watchlistsync.ts` via `PlexTvAPI.getWatchlist`. Source: https://docs.seerr.dev/using-seerr/plex/watchlist-auto-request/
- Fetching other users' Plex watchlists requires their token (open ask seerr#1378).

### Blocklist ("blacklist")

- "Blocklist for movies, series, and tags: Allows permitted users to hide movies, series, or tags from regular users." Entity `Blocklist` (`mediaType, tmdbId, blocklistedTags`); collections can be blocklisted ("Add collection to blocklist" API); tag-based auto-blocklisting job "Process Blocklisted Tags" with "Blocklist Region"/"Blocklist Language". Sources: release blog; https://docs.seerr.dev/using-seerr/settings/general/ ; https://docs.seerr.dev/api/add-collection-to-blocklist/

### Tagging

- Radarr/Sonarr tags per server (`DVRSettings.tags`), "Tag Requests" (`tagRequests`: tag items with requesting user), anime-specific `animeTags`, per-request `tags`. Sources: `server/lib/settings/index.ts`; `SonarrModal`/`RadarrModal` UI strings "Tag Requests".

### Override rules

- "Override rules: Adjust default request settings based on conditions such as user, tag, or other criteria." Entity `OverrideRule { radarrServiceId, sonarrServiceId, users, genre, language, keywords, profileId, rootFolder, tags }`. UI: "Specifies conditions before applying parameter changes. Each field must be validated for the rules to be applied (AND operation). A field is considered verified if any of its properties match (OR operation)." Sources: release blog; https://github.com/seerr-team/seerr/blob/develop/server/entity/OverrideRule.ts ; `src/components/Settings/OverrideRule/OverrideRuleModal.tsx`; API https://docs.seerr.dev/api/create-override-rule/
- Rules cannot pick the *instance* (open ask seerr#1560 "Override Rules: Add instance support").

### Mobile / PWA

- "Mobile-friendly design" (README); `public/site.webmanifest` with `display: standalone` and shortcuts Discover/Requests/Profile/Settings; pull-to-refresh e2e test (`cypress/e2e/pull-to-refresh.cy.ts`). No first-party native app (seerr#509 "Mobile App" closed).

### i18n

- 41 locales in `src/i18n/locale`; Weblate at translate.seerr.dev. Source: `CONTRIBUTING.md`.

### API

- REST under `/api/v1`, OpenAPI file `seerr-api.yml` (168 paths), cookie or `X-Api-Key` auth; local Swagger UI at `/api-docs`. Source: README "API Documentation"; `seerr-api.yml`.
- Community Terraform/OpenTofu provider exists ("maintained by the community, not by the Seerr team"). Source: https://docs.seerr.dev/extending-seerr/terraform-provider/
- No per-user API keys (open, seerr#2582 "Personal API keys per user").

### Other

- Jobs (`server/job/schedule.ts`): Plex recently-added scan, Plex full scan, Plex refresh token, Plex watchlist sync, Jellyfin recently-added scan, Jellyfin full scan, Radarr scan, Sonarr scan, Media Availability Sync, Download Sync, Download Sync Reset, Image Cache Cleanup, Process Blocklisted Tags. Cron editable in UI ("Jobs & Cache"). Source: https://docs.seerr.dev/using-seerr/settings/jobs&cache/
- Image proxy/cache ("Enable Image Caching ... proxy and cache images from pre-configured sources (such as TMDB)"). Source: general settings docs.
- Network: DNS cache, force IPv4, HTTP(S) proxy, trust proxy, CSRF, API request timeout (default 10 s). Source: https://docs.seerr.dev/using-seerr/settings/network/
- Version check against GitHub releases (`server/api/github.ts`), disable-able (seerr#1873 closed/implemented).

---

## 3. Integrations

| Service | Used for | API / auth | Source |
|---|---|---|---|
| **TMDB** | All movie/TV/person/collection metadata, discover, search, keywords, watch providers, genres, certifications | `https://api.themoviedb.org/3` with a **hard-coded application API key** (`api_key: '431a8708161bcd1f1fbe7536137e61ed'` in `server/api/themoviedb/index.ts` line 188). Anime detected by TMDB keyword `ANIME_KEYWORD_ID = 210024`. | https://github.com/seerr-team/seerr/blob/develop/server/api/themoviedb/index.ts ; `server/api/themoviedb/constants.ts` |
| **TheTVDB** (experimental) | Optional metadata provider for series and anime "as in Sonarr" to fix season/episode numbering mismatches; enriches TMDB show with TVDB seasons | `https://api4.thetvdb.com/v4`, login with a hard-coded project `apiKey` (`server/api/tvdb/index.ts` line 114), token refresh. Settings `MetadataSettings { tv, anime }` each `tmdb|tvdb`; UI "Metadata Providers" with test button. | release blog; `server/api/tvdb/index.ts`; `src/components/Settings/SettingsMetadata.tsx` |
| **AniDB / anime-lists** | Map AniDB ids from Jellyfin libraries to TVDB/TMDB ("fall back on AniDB") | Downloads `https://raw.githubusercontent.com/Anime-Lists/anime-lists/master/anime-list.xml` | `server/api/animelist.ts`; `server/lib/scanners/jellyfin/index.ts` |
| **Sonarr** (v3/v4) | Add series, monitor seasons, search, queue (download progress), library scan for availability, tags | REST with API key; v3 and v4 only ("Only v3 & V4 Radarr/Sonarr servers are supported!"). Per-instance settings: default / default-4K, hostname/port/SSL/base URL, `activeProfileId`, `activeDirectory` (root folder), `activeLanguageProfileId` (fetched from `/languageprofile`), `tags`, `enableSeasonFolders`, `seriesType` (`standard|daily|anime`), `monitorNewItems` (`all|none`, "Monitor New Seasons"), anime overrides `animeSeriesType, activeAnimeProfileId, activeAnimeDirectory, activeAnimeLanguageProfileId, animeTags`, `syncEnabled` (scan), `preventSearch`, `tagRequests`, `externalUrl`, `overrideRule[]`. `AddSeriesOptions { tvdbid, profileId, languageProfileId, seasons[], seasonFolder, rootFolderPath, tags, seriesType, monitored, monitorNewItems, searchNow }`. Methods: `getSeries, getSeriesByTvdbId, addSeries, getLanguageProfiles, searchSeries, getEpisodes, monitorEpisodes, removeSeries`, base `getProfiles, getRootFolders, getQueue, getTags, createTag, renameTag, getSystemStatus`. | https://docs.seerr.dev/using-seerr/settings/services/ ; `server/lib/settings/index.ts` (`SonarrSettings`); `server/api/servarr/sonarr.ts`; `server/api/servarr/base.ts` |
| **Radarr** | Add movie, search, queue, library scan, remove | Same base; `RadarrSettings.minimumAvailability` ("Minimum Availability" required); `RadarrMovieOptions { qualityProfileId, minimumAvailability, tags, rootFolderPath, tmdbId, monitored, searchNow }`; methods `getMovies, getMovieByTmdbId, addMovie, searchMovie, removeMovie`. Separate 4K instances flagged `is4k`. | `server/api/servarr/radarr.ts`; services docs |
| **Lidarr / Readarr / music / books** | **Not integrated.** README: "Currently, Seerr supports Sonarr and Radarr. More to come!" Music PR #2132 "feat: music support" was closed unmerged (2026-06-10) after a preview image (`preview-music-support`); no Lidarr code in `server/`. Overseerr closed Lidarr (#352) and eBook (#605) as `wontimplement`. | README; https://github.com/seerr-team/seerr/pull/2132 ; https://github.com/seerr-team/seerr/discussions/2160 ; https://github.com/sct/overseerr/issues/352 ; https://github.com/sct/overseerr/issues/605 |
| **Plex** | Auth (plex.tv PIN OAuth), user import (`plex.tv` friends/home users via `PlexTvAPI.getUsers`), `checkUserAccess`, library list/sync, recently added and full scans for availability (`ratingKey` stored on `Media`), Plex Watchlist sync, "Play on Plex" deep links (`mediaUrl`, `iOSPlexUrl`), token refresh job | Admin's Plex token (from login) + `X-Plex-*` headers; `PlexSettings { name, machineId, ip, port, useSsl, libraries[], webAppUrl }` | `server/api/plextv.ts`; `server/api/plexapi.ts`; `server/lib/scanners/plex` |
| **Jellyfin / Emby** | Auth (username/password, Quick Connect), user import (`getUsers`), library list, recently-added and full scans (`jellyfinMediaId` on `Media`), seasons/episodes for partial availability, "Play on Jellyfin" links, avatar proxy, forgot-password URL | Admin login yields an API token (`createApiToken`); `JellyfinSettings { ip, port, useSsl, urlBase, externalHostname, jellyfinForgotPasswordUrl, libraries[], serverId, apiKey }`. Emby uses the same client with `ServerType.EMBY`. | `server/api/jellyfin.ts`; https://docs.seerr.dev/using-seerr/settings/mediaserver/ |
| **Tautulli** (Plex only) | Watch statistics on media pages and user pages: `get_item_watch_time_stats`, `get_item_user_stats`, `get_user_watch_time_stats`, `get_history`; `tautulliUrl` links | Tautulli API key; `TautulliSettings { hostname, port, useSsl, urlBase, apiKey, externalUrl }` | `server/api/tautulli.ts`; `server/lib/settings/index.ts` |
| **Prowlarr / indexers / download clients** | **Not integrated** (no code in `server/api`). Seerr only talks to the *arr apps. | `server/api` directory listing |
| Rotten Tomatoes / IMDb | Ratings display | RT via public Algolia index; IMDb via `https://api.radarr.video/v1` | `server/api/rating/*` |
| GitHub | Version/update check | `https://api.github.com` releases/commits | `server/api/github.ts` |
| Pushover API | Sound list for user settings ("Get Pushover sounds") | Pushover app token | `server/api/pushover.ts` |

Notes for pluggability: media-server code is not behind a single interface today — `availabilitySync.ts` imports `JellyfinAPI`, `PlexAPI`, `RadarrAPI`, `SonarrAPI` directly and branches on `MediaServerType`; scanners share a `baseScanner.ts`. Source: https://github.com/seerr-team/seerr/blob/develop/server/lib/availabilitySync.ts

---

## 4. Data model (high level)

Entities in `server/entity/` (TypeORM):

| Entity | Key fields | File |
|---|---|---|
| `User` | `id, email, username, displayName, password (local), userType (PLEX/LOCAL/JELLYFIN/EMBY), permissions (bitmask), plexId, plexToken, plexUsername, jellyfinUserId, jellyfinDeviceId, jellyfinAuthToken, avatar, movieQuotaLimit/Days, tvQuotaLimit/Days, resetPasswordGuid, recoveryLinkExpirationDate`; relations `requests, watchlists, settings, pushSubscriptions, createdIssues` | https://github.com/seerr-team/seerr/blob/develop/server/entity/User.ts |
| `UserSettings` | `locale, discoverRegion, streamingRegion, originalLanguage, pgpKey, discordIds[], pushbulletAccessToken, pushoverApplicationToken/UserKey/Sound, telegramChatId/MessageThreadId/SendSilently, watchlistSyncMovies, watchlistSyncTv, notificationTypes (per-agent bitmask)` | `server/entity/UserSettings.ts` |
| `UserPushSubscription` | `endpoint, p256dh, auth, userAgent` | `server/entity/UserPushSubscription.ts` |
| `Session` | express-session rows (`connect-typeorm`) | `server/entity/Session.ts` |
| `Media` | one row per TMDB title: `mediaType, tmdbId, tvdbId, imdbId, status, status4k, serviceId/serviceId4k (arr instance), externalServiceId/4k (arr item id), externalServiceSlug/4k, ratingKey/4k (Plex), jellyfinMediaId/4k, lastSeasonChange, mediaAddedAt`; relations `requests, seasons, issues, watchlists, blocklist` | https://github.com/seerr-team/seerr/blob/develop/server/entity/Media.ts |
| `Season` | `seasonNumber, status, status4k` → `Media` | `server/entity/Season.ts` |
| `MediaRequest` | `status, type, is4k, seasonCount, serverId, profileId, rootFolder, languageProfileId, tags[], isAutoRequest, ignoreQuota, requestedBy, modifiedBy` → `Media`; `seasons: SeasonRequest[]`; business logic (send to Radarr/Sonarr, notifications) lives here and in `server/subscriber/MediaRequestSubscriber.ts` | https://github.com/seerr-team/seerr/blob/develop/server/entity/MediaRequest.ts |
| `SeasonRequest` | `seasonNumber, status` → `MediaRequest` | `server/entity/SeasonRequest.ts` |
| `Issue` / `IssueComment` | `issueType, status, problemSeason, problemEpisode, createdBy, modifiedBy` / `message, user` | `server/entity/Issue.ts`, `server/entity/IssueComment.ts` |
| `Watchlist` | `mediaType, tmdbId, requestedBy` → `Media` | `server/entity/Watchlist.ts` |
| `Blocklist` | `mediaType, tmdbId, blocklistedTags, user` → `Media` | `server/entity/Blocklist.ts` |
| `OverrideRule` | `radarrServiceId, sonarrServiceId, users, genre, language, keywords, profileId, rootFolder, tags` | `server/entity/OverrideRule.ts` |
| `DiscoverSlider` | `type, order, isBuiltIn, enabled, title, data` | `server/entity/DiscoverSlider.ts` |

Settings are **not** in the DB: `AllSettings { clientId, sessionSecret, vapidPublic, vapidPrivate, main, plex, jellyfin, tautulli, radarr[], sonarr[], public, notifications.agents, jobs, network, metadataSettings, migrations[] }` is serialized to `config/settings.json`. Source: https://github.com/seerr-team/seerr/blob/develop/server/lib/settings/index.ts

Notification dispatch: `server/lib/notifications/` with one class per agent in `server/lib/notifications/agents/` (`discord, email, gotify, ntfy, pushbullet, pushover, slack, telegram, webhook, webpush`). Media availability is recomputed by scanners (`server/lib/scanners/{plex,jellyfin,radarr,sonarr}`) and `availabilitySync.ts`.

---

## 5. Top user asks and pain points

Ranked by GitHub reactions (GitHub search API, `sort=reactions`, 2026-09-21). "State" is as of that date.

| # | Issue | Reactions | State | Summary |
|---|---|---|---|---|
| 1 | seerr#183 [Feature Request] Support login with OIDC | 276 | open | Generic OIDC (Authelia/Authentik/Keycloak) login; PR #2715 open with preview image. https://github.com/seerr-team/seerr/issues/183 |
| 2 | overseerr#295 Add Emby Support | 236 | open (archived repo) | Delivered by Seerr. https://github.com/sct/overseerr/issues/295 |
| 3 | seerr#96 [Feature Request] Music / Audio support | 132 | open | Music requests (MusicBrainz/Lidarr); PR #2132 closed unmerged. https://github.com/seerr-team/seerr/issues/96 |
| 4 | overseerr#274 Run overseerr on subpath, Base URL | 120 | open | Serve under a sub-path behind a reverse proxy (also seerr#97, 37). https://github.com/sct/overseerr/issues/274 |
| 5 | overseerr#404 Customize the logo / Change the name | 106 | open | Branding; Seerr has "Application Title" but no logo upload (seerr#141 closed). https://github.com/sct/overseerr/issues/404 |
| 6 | seerr#308 add ability to create media removal requests | 93 | open | Users request deletion; threshold-based auto-delete. https://github.com/seerr-team/seerr/issues/308 |
| 7 | overseerr#789 Allow users to subscribe to TV episodes or Movie notifications | 92 | open | Notify non-requesters; also seerr#375 "Notify multiple users for same item" (37). https://github.com/sct/overseerr/issues/789 |
| 8 | overseerr#342 / seerr#264 Individual episode requests | 90 / 72 | open | Episode-level granularity. https://github.com/seerr-team/seerr/issues/264 |
| 9 | overseerr#352 Add support for Lidarr | 88 | closed wontimplement | Music via Lidarr. https://github.com/sct/overseerr/issues/352 |
| 10 | overseerr#605 eBook support / seerr#134 Implement bookshelf (Readarr fork) | 54 / 48 | closed wontimplement / open | Books. https://github.com/seerr-team/seerr/issues/134 |
| 11 | overseerr#405 Custom notification support | 53 | open | Custom notification content/templates; also seerr#1876. https://github.com/sct/overseerr/issues/405 |
| 12 | overseerr#1638 Login with other services | 50 | open | SSO/LDAP/other IdPs. https://github.com/sct/overseerr/issues/1638 |
| 13 | overseerr#2509 Calendar from Sonarr and Radarr / seerr#672 Release Calender | 49 / 20 | open | Upcoming releases calendar. https://github.com/seerr-team/seerr/issues/672 |
| 14 | seerr#2449 Add Tracearr Support as an Alternative to Tautulli | 49 | open | Watch stats for Plex/Jellyfin/Emby via Tracearr. https://github.com/seerr-team/seerr/issues/2449 |
| 15 | seerr#480 Notifications for new episodes of ongoing seasons | 48 | open | Per-episode availability notifications; also seerr#1361. https://github.com/seerr-team/seerr/issues/480 |
| 16 | overseerr#3750 Request/monitor Future Episodes/Seasons Only / seerr#332 | 45 / 19 | open | Monitor future only. https://github.com/sct/overseerr/issues/3750 |
| 17 | overseerr#3533 Parental controls / seerr#501 Adjustable Age Restrictions | 42 / 40 | open | Per-user certification/genre limits. https://github.com/seerr-team/seerr/issues/501 |
| 18 | overseerr#835 Auto-approve/deny by studio/network/streaming / seerr#1184 | 40 / 12 | open | Rule-based auto approval. https://github.com/sct/overseerr/issues/835 |
| 19 | seerr#1792 Allow users to delete their requested media | 39 | open | Self-service deletion. https://github.com/seerr-team/seerr/issues/1792 |
| 20 | overseerr#593 Optional message when denying / seerr#988 Decline with comment | 39 / 11 | open | Decline reasons. https://github.com/sct/overseerr/issues/593 |
| 21 | seerr#165 View/request planned media from providers (AniList/Trakt) | 35 | open | External list sync. https://github.com/seerr-team/seerr/issues/165 |
| 22 | overseerr#2876 Sonarr for anime only / seerr#232 Dedicated anime instances | 34 / 26 | open | Route anime to its own instance (today only profile/folder overrides). https://github.com/seerr-team/seerr/issues/232 |
| 23 | overseerr#1881 Custom request routing / overseerr#609 Different paths per user | 34 / 30 | open | Per-user/per-rule routing (partly covered by override rules). https://github.com/sct/overseerr/issues/1881 |
| 24 | seerr#100 Simultaneous multiple auth methods | 32 | open | Plex and Jellyfin at once. https://github.com/seerr-team/seerr/issues/100 |
| 25 | seerr#2248 Advanced Requests - Grant/Deny for Tags, Path, Quality Profile | 24 | open | Finer-grained advanced permission. https://github.com/seerr-team/seerr/issues/2248 |
| 26 | seerr#1737 Extend request profiles beyond Standard/4K | 18 | open | N request "profiles" (language, quality), not just 4K. https://github.com/seerr-team/seerr/issues/1737 |
| 27 | seerr#2235 Prioritize Radarr/Sonarr availability over media server | 18 | open | Configurable availability source. https://github.com/seerr-team/seerr/issues/2235 |
| 28 | seerr#804 Support multiple notification agent instances | 17 | open | e.g. two Discord webhooks. https://github.com/seerr-team/seerr/issues/804 |
| 29 | seerr#189 Multiple Plex servers / seerr#2182 Remove media server dependency | 13 / 14 | open | Multi-server and no-server modes. https://github.com/seerr-team/seerr/issues/189 , https://github.com/seerr-team/seerr/issues/2182 |
| 30 | seerr#981 Playback Reporting / Jellystat in sidebar / seerr#2582 Personal API keys | 10 / 9 | open | Stats for Jellyfin users; per-user API keys. https://github.com/seerr-team/seerr/issues/981 , https://github.com/seerr-team/seerr/issues/2582 |

Other notable items: overseerr#3510 "Overseerr loading times are excrutiatingly slow" (14 reactions, 63 comments, open); seerr#3053 "Improve user facing error messages" (11); seerr#1351 "Limit number of seasons per request" (20); seerr#1786 "Support Blocklisting Content via Genre" (10); seerr#2301 "Anime mode" (11, 24 comments); discussion #1808 "Implement a Plugin Architecture" (4 upvotes); discussion #3383 Foreseerr fork offering Trakt/discovery features upstream.

---

## 6. Gaps and opportunities for a rewrite (grounded in the above)

1. **Pluggable media servers, N at once.** Seerr supports exactly one server (`mediaServerType` scalar; "Only one integration can be used at a time"), which drives seerr#189, seerr#100, seerr#2182 and the migration PR #2539. A provider interface (auth, users, libraries, availability, deep links) with zero-to-many instances removes a whole class of asks.
2. **Pluggable request targets beyond Radarr/Sonarr.** Music (seerr#96, 132 reactions; PR #2132 died unmerged), books (seerr#134), anime-specific instances (seerr#232) all fail on the hard-coded Radarr/Sonarr + is4k model. Model "request profiles" as first-class (seerr#1737) instead of the `status`/`status4k` column pairs on `Media` and `Season`.
3. **Auth providers as plugins.** OIDC has 276 reactions and has been in flight since 2022 (Overseerr PR 1941 closed 2022; seerr PRs #184, #1823 closed; #2715 still open). Forward-auth (seerr#1221), LDAP, and multi-provider login (seerr#100) fit the same abstraction. Also per-user API keys (seerr#2582) — Seerr has one admin key.
4. **Episode granularity and richer notifications.** Season-only requests (seerr#264, 72) and no per-episode/"partially available" notifications (seerr#480, seerr#1361), no subscribe/"notify me too" (overseerr#789, seerr#375), no multiple instances per agent (seerr#804), no custom templates except webhook (overseerr#405).
5. **Rule engine.** Override rules exist but cannot select instance (seerr#1560), cannot auto-approve/deny (overseerr#835, seerr#1184), cannot do parental/certification limits (seerr#501, overseerr#3533), and "Advanced Requests" is all-or-nothing (seerr#2248).
6. **Lifecycle beyond acquisition.** Removal requests (seerr#308, 93), self-delete (seerr#1792), auto-expiry (overseerr#3386) are absent; Maintainerr exists as a separate tool for this.
7. **Stats plugins.** Tautulli is Plex-only; asks for Tracearr (seerr#2449, 49) and Jellystat (seerr#981).
8. **Deployment ergonomics.** Base-URL/sub-path (overseerr#274, seerr#97) still open; settings live in a JSON file rather than DB; hard-coded TMDB/TVDB keys in source.
9. **Availability source of truth** should be configurable (seerr#2235) rather than always merging media-server scans with *arr scans.
10. **Metadata providers.** TVDB is bolted on as an experimental toggle; a provider abstraction (TMDB, TVDB, MusicBrainz, Open Library, AniList/Trakt lists per seerr#165) generalizes it.
11. **Security posture worth matching:** signed containers/Helm, rootless container, CSRF option, API timeout; and avoiding the 3.1.0 class of bugs (unauthenticated registration via an inactive auth backend, IDOR on user profile).

---

## 7. Other tools in the ecosystem

One line each; descriptions quoted from each repo's GitHub description (via `gh repo view`) unless noted.

- **Ombi** (Ombi-app/Ombi, C#, 4.1k stars): "Want a Movie or TV Show on Plex/Emby/Jellyfin? Use Ombi!" — the other major request manager; supports Lidarr music requests (per Requestrr's description listing Ombi alongside Lidarr). https://github.com/Ombi-app/Ombi
- **Petio** (petio-team/petio, JS, 289 stars, last push 2025-01): "Petio Request, Discover, Review". https://github.com/petio-team/petio
- **Requestrr** (thomst08/requestrr, C#): "a chatbot used to simplify using services like Sonarr/Radarr/Lidarr/Ombi/Overseerr via the use of chat. Current platform is Discord only". https://github.com/thomst08/requestrr
- **Doplarr** (activexray/Doplarr, Clojure, archived): "An *arr request bot for Discord". https://github.com/activexray/Doplarr
- **Maintainerr** (Maintainerr/Maintainerr, TypeScript, 2.3k stars): "Looks and smells like Seerr, does the opposite. A library maintenance tool for Plex, Jellyfin and Emby." — rule-based deletion; covers seerr#308/#1792 space. https://github.com/Maintainerr/Maintainerr
- **Recommendarr** (fingerthief/recommendarr): AI recommendations from Sonarr/Radarr/Plex/Jellyfin libraries; `gh` could not resolve the repo on 2026-09-21 (may be renamed/removed); referenced by navilg/media-stack ("Includes Sonarr, Radarr, qBitTorrent, Prowlarr, Jellyfin, Seerr, Recommendarr"). Sources: https://github.com/navilg/media-stack , https://awesome.ecosyste.ms/projects/github.com/fingerthief/recommendarr . Related: Discovarr https://github.com/sqrlmstr5000/discovarr , SuggestArr https://giuseppe99barchetta.github.io/SuggestArr/ (both push requests into Seerr/Overseerr).
- **Huntarr** (formerly plexguide/Huntarr.io): automates searching for missing/upgradable items in *arr apps; original repo gone — an archive notes it "is preserved for posterity after the original author went scorched earth when significant security vulnerabilities were pointed out"; fork "newtarr" continues. Sources: https://github.com/MGHazz/huntarr.io-archive , https://github.com/elfhosted/newtarr
- **Lidarr** (C#, 5.7k): "Looks and smells like Sonarr but made for music." https://github.com/Lidarr/Lidarr
- **Readarr** (C#, archived 2025): "Book Manager and Automation (Sonarr for Ebooks)". Archived — book support would need a fork (seerr#134 mentions "bookshelf"). https://github.com/Readarr/Readarr
- **Whisparr** (C#, 1.1k): adult-content *arr (no repo description). https://github.com/Whisparr/Whisparr
- **Prowlarr** (C#, 7.2k): "an indexer manager/proxy ... supporting management of both Torrent Trackers and Usenet Indexers." https://github.com/Prowlarr/Prowlarr
- **Bazarr** (Python, 4.3k): "companion application to Sonarr and Radarr. It manages and downloads subtitles". https://github.com/morpheus65535/bazarr
- **Unpackerr** (Go, 1.5k): "Extracts downloads for Radarr, Sonarr, Lidarr, Readarr, and/or a Watch folder". Go codebase; useful reference for *arr API clients in Go. https://github.com/Unpackerr/unpackerr
- **Tdarr** (4.3k): "Distributed transcode automation using FFmpeg/HandBrake + Audio/Video library analytics + video health checking". https://github.com/HaveAGitGat/Tdarr
- **Tracearr** (TypeScript, 2.6k): "Real-time monitoring for Plex, Jellyfin, and Emby servers. Track streams, analyze playback, and detect account sharing" — requested as Tautulli alternative (seerr#2449). https://github.com/connorgallopo/Tracearr
- **Jellystat** (JS, 2.5k): "a free and open source Statistics App for Jellyfin" (seerr#981). https://github.com/CyferShepard/Jellystat
- **Wizarr** (Python, 3.2k): "an advanced user invitation and management system for Jellyfin, Plex, Emby etc." — onboarding adjacent to Seerr's user import. https://github.com/wizarrrr/wizarr
- **Foreseerr**: Seerr fork adding "Trakt, discovery, library browsing, ratings, filters/sliders" (maintainer's words). https://github.com/seerr-team/seerr/discussions/3383

---

## Unverified / not confirmed

- `Permission.VOTE` has no references outside the enum; treat as dead.
- Recommendarr's canonical repo could not be resolved via GitHub on the research date.
