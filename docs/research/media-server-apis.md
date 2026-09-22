# Media-server API research for Bloom

Date: 2026-09-21. Jellyfin figures come from the published stable OpenAPI document (`info.version` = `12.1.0`) and the `jellyfin/jellyfin` `master` branch (latest release `v12.1`, published 2026-09-15). Emby figures come from the Swagger 2.0 document shipped in `MediaBrowser/Emby.SDK`. Plex figures come from Plex support/developer pages and from the source of Tautulli, python-plexapi and Overseerr where Plex has no official document.

Conventions: quotes are verbatim from the cited source. "Not confirmed" means the item could not be found in any primary source consulted.

Primary sources used throughout:

- Jellyfin OpenAPI (stable): <https://api.jellyfin.org/openapi/jellyfin-openapi-stable.json>
- Jellyfin API browser (ReDoc): <https://api.jellyfin.org/>
- Jellyfin server source: <https://github.com/jellyfin/jellyfin>
- Emby Swagger 2.0: <https://github.com/MediaBrowser/Emby.SDK/blob/master/Documentation/Download/openapi_v2_noversion.json>
- Plex Media Server API reference: <https://developer.plex.tv/pms/>

---

## 1. Jellyfin authentication for a management app

### 1.1 Header scheme

The OpenAPI document declares one security scheme: `CustomAuthentication`, `"type": "apiKey"`, `"name": "Authorization"`, `"in": "header"` (source: `components.securitySchemes` in the stable OpenAPI).

The server parses the header in `AuthorizationContext.GetAuthorization`: the scheme name must equal `MediaBrowser` (case-insensitive); `Emby` is accepted only when `EnableLegacyAuthorization` is on. The remainder is parsed by `GetParts` as comma-separated `Key="Value"` pairs, and each value is URL-decoded (`WebUtility.UrlDecode(... .Trim('"'))`). Keys read: `DeviceId`, `Device`, `Client`, `Version`, `Token`. Legacy fallbacks (`X-Emby-Authorization` header, `X-Emby-Token`, `X-MediaBrowser-Token`, `api_key` query) are read only when `EnableLegacyAuthorization` is true; the `ApiKey` query parameter is read unconditionally.
Source: <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Server.Implementations/Security/AuthorizationContext.cs>

Jellyfin core developer Niels van Velzen documents the same format: "The Jellyfin scheme is named `MediaBrowser` and it uses named values separated by commas." and "Values must be wrapped in double quotes and should use url encoding." Example given: `Authorization: MediaBrowser Token="0381cf931f9e42d79fb9c89f729167df", Client="Android TV"...`. The gist states the legacy headers "have subsequently been removed in Jellyfin 12.0."
Source: <https://gist.github.com/nielsvanvelzen/ea047d9028f676185832e51ffaf12a6f>

Practical header for Bloom:

```
Authorization: MediaBrowser Client="Bloom", Device="bloom-server", DeviceId="<stable-uuid>", Version="<semver>", Token="<token>"
```

Jellyseerr builds the same string: `` `MediaBrowser Client="Seerr", Device="Seerr", DeviceId="${safeDeviceId}", Version="${version}"` `` and appends `Token` after login.
Source: <https://github.com/Fallenbagel/jellyseerr/blob/develop/server/api/jellyfin.ts>

### 1.2 API keys vs user access tokens

Both go in the `Token` value. The server distinguishes them after lookup:

- A token matching `ApiKeys.AccessToken` sets `authInfo.IsApiKey = true`, `authInfo.Client = key.Name`, and fills `DeviceId`/`Device`/`Version` from the server's own identity if the header omitted them (AuthorizationContext, above).
- `CustomAuthenticationHandler` assigns the `Administrator` role when `authorizationInfo.IsApiKey || (authorizationInfo.User?.HasPermission(PermissionKind.IsAdministrator) ?? false)`.
  Source: <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Auth/CustomAuthenticationHandler.cs>
- `DefaultAuthorizationHandler`: `if (isApiKey) { // Api keys are unrestricted. context.Succeed(requirement); ... }`. For user tokens it fails when the user is remote and lacks `EnableRemoteAccess`, succeeds for admins ("Admins can do everything"), and otherwise enforces the parental schedule.
  Source: <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Auth/DefaultAuthorizationPolicy/DefaultAuthorizationHandler.cs>

Consequence for Bloom: an API key is effectively an admin credential with no user identity. Endpoints that need a user context accept `userId` as a query parameter. The `/Items` parameter doc states: `userId` — "The user id supplied as query parameter; this is required when not using an API key." (stable OpenAPI, `/Items` GET parameters).

API-key management endpoints (all `[Authorize(Policy = Policies.RequiresElevation)]`): `GET /Auth/Keys`, `POST /Auth/Keys?app=<name>` ("Create a new api key."), `DELETE /Auth/Keys/{key}`.
Source: stable OpenAPI `/Auth/Keys`; <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/ApiKeyController.cs>

### 1.3 AuthenticateByName

`POST /Users/AuthenticateByName`, operationId `AuthenticateUserByName`, body schema `AuthenticateUserByName` with `Username` ("Gets or sets the username.") and `Pw` ("Gets or sets the plain text password."). Response `AuthenticationResult` has `User` (UserDto), `SessionInfo` (SessionInfoDto), `AccessToken`, `ServerId`.
Source: stable OpenAPI `/Users/AuthenticateByName`, schemas `AuthenticateUserByName`, `AuthenticationResult`.

The controller method carries no `[Authorize]` attribute, but the request must still carry a `MediaBrowser` header with `Client`/`Device`/`DeviceId`/`Version` so the server can register the device (the `AuthorizationContext` reads them from the header for every request).
Source: <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/UserController.cs> (line `[HttpPost("AuthenticateByName")]`).

### 1.4 Quick Connect

User-facing description: "Quick Connect is a feature that allows users to sign in to clients without entering a username or password. Instead, a temporary Quick Connect code is generated and used to authorize login from an already authenticated client." It is "enabled by default" and can be disabled under Dashboard > General.
Source: <https://jellyfin.org/docs/general/server/quick-connect/>

Endpoints (stable OpenAPI):

| Endpoint | opId | Notes |
|---|---|---|
| `GET /QuickConnect/Enabled` | `GetQuickConnectEnabled` | no auth |
| `POST /QuickConnect/Initiate` | `InitiateQuickConnect` | returns `QuickConnectResult` (`Secret`, `Code`, `Authenticated`, `DeviceId`, `DeviceName`, `AppName`, `AppVersion`, `DateAdded`) |
| `GET /QuickConnect/Connect?secret=` | `GetQuickConnectState` | poll until `Authenticated` is true |
| `POST /QuickConnect/Authorize?code=&userId=` | `AuthorizeQuickConnect` | `[Authorize]`; the `userId` param doc reads "The user the authorize. Access to the requested user is required." |
| `POST /Users/AuthenticateWithQuickConnect` | `AuthenticateWithQuickConnect` | body `QuickConnectDto { Secret }`, returns `AuthenticationResult` |

Source: stable OpenAPI; <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/QuickConnectController.cs>

Relevance for Bloom: with an admin token (or API key) Bloom can call `/QuickConnect/Authorize?code=...&userId=<target>` to approve a code on behalf of a user, because `RequestHelpers.GetUserId(User, userId)` resolves the target user and the default policy grants admins access.

### 1.5 What an admin token can do (policy summary)

Policies observed on the endpoints Bloom needs (controller source, `master`):

- `RequiresElevation`: `POST /Users/New`, `DELETE /Users/{userId}`, `POST /Users/{userId}/Policy`, `GET /System/ActivityLog/Entries` (whole controller), `GET /Library/MediaFolders`, `GET/POST/DELETE /Auth/Keys`.
- `FirstTimeSetupOrElevated`: whole `LibraryStructureController` (`/Library/VirtualFolders`).
- Plain `[Authorize]`: `GET /Users`, `GET /Sessions`, `POST /Users/Password`, `POST /Users/Configuration`, `POST /Users` (update), `POST /QuickConnect/Authorize`.

Sources: UserController, ActivityLogController, LibraryController, LibraryStructureController, SessionController, ApiKeyController under <https://github.com/jellyfin/jellyfin/tree/master/Jellyfin.Api/Controllers>. The OpenAPI also annotates operations, e.g. `/Users/New` POST has `"security": [{"CustomAuthentication": ["RequiresElevation"]}]`.

---

## 2. Jellyfin user management endpoints

All from the stable OpenAPI unless noted. Paths of the form `/Users/{userId}/Password`, `/Users/{userId}/Configuration`, `/Users/{userId}` (POST) and `/Users/{userId}/Items` still exist in the controller as `[Obsolete("Kept for backwards compatibility")]` with `[ApiExplorerSettings(IgnoreApi = true)]`, so they are absent from the OpenAPI document. Use the query-parameter forms below.
Source: <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/UserController.cs>, <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/ItemsController.cs>

| Operation | Endpoint | Body / params | Response |
|---|---|---|---|
| List users | `GET /Users?isHidden=&isDisabled=` | — | `UserDto[]` |
| Create user | `POST /Users/New` | `CreateUserByName { Name, Password }` | `UserDto` |
| Get user | `GET /Users/{userId}` | — | `UserDto` |
| Delete user | `DELETE /Users/{userId}` | — | 204 "User deleted." |
| Update user (name etc.) | `POST /Users?userId=` | `UserDto` | 204 |
| Update password | `POST /Users/Password?userId=` | `UpdateUserPassword { CurrentPassword (sha1, legacy), CurrentPw, NewPw, ResetPassword }` | 204 |
| Update policy | `POST /Users/{userId}/Policy` | `UserPolicy` | 204 "User policy updated." |
| Update configuration | `POST /Users/Configuration?userId=` | `UserConfiguration` | 204 |
| Current user | `GET /Users/Me` | — | `UserDto` |

`UserDto` fields: `Configuration, EnableAutoLogin, HasConfiguredEasyPassword, HasConfiguredPassword, HasPassword, Id, LastActivityDate, LastLoginDate, Name, Policy, PrimaryImageAspectRatio, PrimaryImageTag, ServerId, ServerName`. Note `LastActivityDate` and `LastLoginDate` are available here for cheap "last seen" reporting.

### 2.1 `UserPolicy` (12.1.0) — full property list

Types are as declared in `components.schemas.UserPolicy`.

- Role/visibility: `IsAdministrator` (bool), `IsHidden` (bool), `IsDisabled` (bool)
- Management rights: `EnableCollectionManagement`, `EnableSubtitleManagement`, `EnableLyricManagement`, `EnableLiveTvManagement`, `EnableLiveTvAccess`, `EnableUserPreferenceAccess` (all bool)
- Parental: `MaxParentalRating` (int), `MaxParentalSubRating` (int), `BlockedTags` (string[]), `AllowedTags` (string[]), `BlockUnratedItems` (`UnratedItem[]`: `Movie, Trailer, Series, Music, Book, LiveTvChannel, LiveTvProgram, ChannelContent, Other`), `AccessSchedules` (`AccessSchedule[]`: `Id, UserId, DayOfWeek, StartHour, EndHour`)
- Remote/control: `EnableRemoteControlOfOtherUsers`, `EnableSharedDeviceControl`, `EnableRemoteAccess`, `EnablePublicSharing` (bool)
- Playback: `EnableMediaPlayback`, `EnableAudioPlaybackTranscoding`, `EnableVideoPlaybackTranscoding`, `EnablePlaybackRemuxing`, `ForceRemoteSourceTranscoding`, `EnableSyncTranscoding`, `EnableMediaConversion` (bool); `RemoteClientBitrateLimit` (int)
- Content ops: `EnableContentDeletion` (bool), `EnableContentDeletionFromFolders` (string[]), `EnableContentDownloading` (bool)
- Scoping: `EnabledDevices` (string[]), `EnableAllDevices` (bool), `EnabledChannels` (string[]), `EnableAllChannels` (bool), `EnabledFolders` (string[]), `EnableAllFolders` (bool), `BlockedMediaFolders` (string[]), `BlockedChannels` (string[])
- Login/sessions: `InvalidLoginAttemptCount` (int), `LoginAttemptsBeforeLockout` (int), `MaxActiveSessions` (int)
- Providers: `AuthenticationProviderId` (string), `PasswordResetProviderId` (string)
- SyncPlay: `SyncPlayAccess` (`SyncPlayUserAccessType`: `CreateAndJoinGroups, JoinGroups, None`)

Source: stable OpenAPI `components.schemas.UserPolicy`, `UnratedItem`, `AccessSchedule`, `SyncPlayUserAccessType`.

Caveat: `POST /Users/{userId}/Policy` replaces the whole policy object (the schema is the full `UserPolicy`; there is no partial-update variant in the spec). Bloom should GET the user, mutate `Policy`, and POST it back.

### 2.2 `UserConfiguration`

`AudioLanguagePreference, PlayDefaultAudioTrack, SubtitleLanguagePreference, DisplayMissingEpisodes, GroupedFolders, SubtitleMode, DisplayCollectionsView, EnableLocalPassword, OrderedViews, LatestItemsExcludes, MyMediaExcludes, HidePlayedInLatest, RememberAudioSelections, RememberSubtitleSelections, EnableNextEpisodeAutoPlay, CastReceiverId`.
Source: stable OpenAPI `components.schemas.UserConfiguration`.

### 2.3 Library listing

- `GET /Library/VirtualFolders` → `VirtualFolderInfo[]` with `Name, Locations, CollectionType, LibraryOptions, ItemId, PrimaryImageItemId, RefreshProgress, RefreshStatus`. `ItemId` is the value to place in `UserPolicy.EnabledFolders`. Requires elevation (`LibraryStructureController` policy `FirstTimeSetupOrElevated`).
- `GET /Library/MediaFolders?isHidden=` → `BaseItemDtoQueryResult`; controller attribute `[Authorize(Policy = Policies.RequiresElevation)]`.
- `GET /Users/{userId}/Views` is what Jellyseerr falls back to for per-user views (`/Users/${this.userId ?? 'Me'}/Views`).

Sources: stable OpenAPI; LibraryStructureController and LibraryController (links in §1.5); Jellyseerr `jellyfin.ts` (link in §1.1).

---

## 3. Jellyfin playback and statistics data sources

### 3.1 `GET /Sessions`

Params: `controllableByUserId`, `deviceId`, `activeWithinSeconds` ("Optional. Filter by sessions that were active in the last n seconds."). Returns `SessionInfoDto[]`. Requires plain `[Authorize]`.

`SessionInfoDto` fields: `PlayState (PlayerStateInfo), AdditionalUsers, Capabilities, RemoteEndPoint, PlayableMediaTypes, Id, UserId, UserName, Client, LastActivityDate, LastPlaybackCheckIn, LastPausedDate, DeviceName, DeviceType, NowPlayingItem (BaseItemDto), NowViewingItem, DeviceId, ApplicationVersion, TranscodingInfo, IsActive, SupportsMediaControl, SupportsRemoteControl, NowPlayingQueue, HasCustomDeviceName, PlaylistItemId, ServerId, UserPrimaryImageTag, SupportedCommands`.

`PlayerStateInfo`: `PositionTicks, CanSeek, IsPaused, IsMuted, VolumeLevel, AudioStreamIndex, SubtitleStreamIndex, MediaSourceId, PlayMethod (Transcode | DirectStream | DirectPlay), RepeatMode, PlaybackOrder, LiveStreamId`.

`TranscodingInfo`: `AudioCodec, VideoCodec, Container, IsVideoDirect, IsAudioDirect, Bitrate, Framerate, CompletionPercentage, Width, Height, AudioChannels, HardwareAccelerationType, TranscodeReasons`.

Source: stable OpenAPI `/Sessions`, `SessionInfoDto`, `PlayerStateInfo`, `TranscodingInfo`, `PlayMethod`; <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/SessionController.cs>

Ticks are .NET `TimeSpan` ticks (100 ns; `TimeSpan.TicksPerSecond` = 10,000,000). The OpenAPI does not state the unit; `PositionTicks` is declared `public long? PositionTicks` in `MediaBrowser.Model/Session/PlayerStateInfo.cs`, and the server converts with `TimeSpan.TicksPerSecond` in `Emby.Server.Implementations/Library/UserDataManager.cs` (GitHub code search `TimeSpan.TicksPerSecond PositionTicks`, 2 hits).
Sources: <https://github.com/jellyfin/jellyfin/blob/master/MediaBrowser.Model/Session/PlayerStateInfo.cs>, <https://github.com/jellyfin/jellyfin/blob/master/Emby.Server.Implementations/Library/UserDataManager.cs>

Websocket alternative to polling `/Sessions`: `SessionInfoWebSocketListener` has `StartType => SessionMessageType.SessionsStart`, `StopType => SessionMessageType.SessionsStop`, and `Type => SessionMessageType.Sessions`. The base class parses the start message data as `"<initialDelayMs>,<periodMs>"` (`var vals = message.Data.Split(','); ... periodMs = long.Parse(vals[1] ...)`) and also pushes on session events (`SendData(true)` on start/stop/progress). Non-admin, non-API-key connections are filtered: "For non-admin users, filter the sessions to only include their own sessions". The websocket path itself was not confirmed from server source (the middleware only checks `httpContext.WebSockets.IsWebSocketRequest`); the conventional client path is not asserted here.
Sources: <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/WebSocketListeners/SessionInfoWebSocketListener.cs>, <https://github.com/jellyfin/jellyfin/blob/master/MediaBrowser.Controller/Net/BasePeriodicWebSocketListener.cs>, <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Middleware/WebSocketHandlerMiddleware.cs>

### 3.2 `GET /System/ActivityLog/Entries`

Requires elevation. Params: `startIndex, limit, minDate, maxDate, hasUserId, name, overview, shortOverview, type, itemId, username, severity, sortBy, sortOrder`. `ActivityLogEntry`: `Id, Name, Overview, ShortOverview, Type, ItemId, Date, UserId, UserPrimaryImageTag, Severity`.

`Type` strings written by the server's event consumers: `AudioPlayback` / `VideoPlayback` / `"Playback"` on start (`GetPlaybackNotificationType`), `AudioPlaybackStopped` / `VideoPlaybackStopped` on stop, `AuthenticationSucceeded`, `SessionStarted`, `UserCreated` (and the other consumers listed under `Events/Consumers`).
Sources: stable OpenAPI; <https://github.com/jellyfin/jellyfin/tree/master/Jellyfin.Server.Implementations/Events/Consumers> (`Session/PlaybackStartLogger.cs`, `Session/PlaybackStopLogger.cs`, `Security/AuthenticationSucceededLogger.cs`, `Session/SessionStartedLogger.cs`, `Users/UserCreatedLogger.cs`).

Caveat: the activity log records that playback started or stopped with a localized human string (`UserStartedPlayingItemWithValues`); it does not record position or duration. It is suitable for "who played what, when", not for watch-time.

### 3.3 Per-user item data (`UserData`)

`GET /Items?userId=<id>&enableUserData=true&fields=...` returns `BaseItemDto` with `UserData: UserItemDataDto`: `Rating, PlayedPercentage, UnplayedItemCount, PlaybackPositionTicks, PlayCount, IsFavorite, Likes, LastPlayedDate, Played, Key, ItemId`. Single item: `GET /UserItems/{itemId}/UserData?userId=`; update: `POST /UserItems/{itemId}/UserData?userId=`. Filters on `/Items`: `isPlayed`, `filters=IsPlayed|IsUnplayed|IsResumable|IsFavorite`.
Source: stable OpenAPI `/Items`, `/UserItems/{itemId}/UserData`, `UserItemDataDto`.

Caveat: `PlayCount` and `LastPlayedDate` are per (user, item); they do not give per-session durations.

### 3.4 Playback Reporting plugin

Repo: <https://github.com/jellyfin/jellyfin-plugin-playbackreporting> (latest release `v19`, 2026-09-08). README: "This plugin enables the collection and visualization of user and media activity on your server. This information can be viewed as a multitude of different graphs, and can also be queried straight from the Jellyfin database."

Storage (SQLite, `ActivityRepository.cs`):

```
create table if not exists PlaybackActivity (DateCreated DATETIME NOT NULL, UserId TEXT, ItemId TEXT, ItemType TEXT, ItemName TEXT, PlaybackMethod TEXT, ClientName TEXT, DeviceName TEXT, PlayDuration INT)
```

Source: <https://github.com/jellyfin/jellyfin-plugin-playbackreporting/blob/master/Jellyfin.Plugin.PlaybackReporting/Data/ActivityRepository.cs>

Duration model (`PlaybackTracker.cs`): it records `START/STOP/PAUSE/UNPAUSE` events with wall-clock timestamps and sums intervals where "the client was actually playing and not paused" (`(START || UNPAUSE) -> (STOP || PAUSE)`). Pause transitions are detected from `PlaybackProgressEventArgs.IsPaused`. Progress is processed at most every 20 seconds: `if (now.Subtract(tracker.LastUpdated).TotalSeconds > 20) // update every 20 seconds`. The plugin subscribes to `ISessionManager.PlaybackStart`, `PlaybackStopped`, `PlaybackProgress` in-process.
Sources: <https://github.com/jellyfin/jellyfin-plugin-playbackreporting/blob/master/Jellyfin.Plugin.PlaybackReporting/Data/PlaybackTracker.cs>, <https://github.com/jellyfin/jellyfin-plugin-playbackreporting/blob/master/Jellyfin.Plugin.PlaybackReporting/EventMonitorEntryPoint.cs>

HTTP API (`[Route("user_usage_stats")]`, `[Authorize(Policy = Policies.RequiresElevation)]`): `GET type_filter_list`, `GET user_activity?days&endDate&timezoneOffset`, `GET user_list`, `GET {userId}/{date}/GetItems?filter&timezoneOffset`, `GET PlayActivity?days&endDate&filter&dataType` (`dataType` "Data type to return (count,time)"), `GET HourlyReport`, `GET {breakdownType}/BreakdownReport`, `GET DurationHistogramReport`, `GET GetTvShowsReport`, `GET MoviesReport`, `GET user_manage/{prune|add|remove}`, `GET load_backup`, `GET save_backup`, `POST submit_custom_query` (body `CustomQueryData { CustomQueryString, ReplaceUserId }`; returns `{ "colums": [...], "results": [[...]] }`).
Source: <https://github.com/jellyfin/jellyfin-plugin-playbackreporting/blob/master/Jellyfin.Plugin.PlaybackReporting/Api/PlaybackReportingActivityController.cs>

`submit_custom_query` lets Bloom run arbitrary SQL against `PlaybackActivity` — the most direct route to per-user watch seconds if the plugin is installed. It is admin-only and plugin-specific.

### 3.5 Webhook plugin

Repo: <https://github.com/jellyfin/jellyfin-plugin-webhook> (latest release `v22`, 2026-09-08). README: "Use Handlebars templating engine to format notifications however you wish." Destinations present in source: `Discord, Generic, GenericForm, Gotify, Mqtt, Pushbullet, Pushover, Slack, Smtp`.

`NotificationType` enum: `None, ItemAdded, Generic, PlaybackStart, PlaybackProgress, PlaybackStop, SubtitleDownloadFailure, AuthenticationFailure, AuthenticationSuccess, SessionStart, PendingRestart, TaskCompleted, PluginInstallationCancelled, PluginInstallationFailed, PluginInstalled, PluginInstalling, PluginUninstalled, PluginUpdated, UserCreated, UserDeleted, UserLockedOut, UserPasswordChanged, UserUpdated, UserDataSaved, ItemDeleted`.
Source: <https://github.com/jellyfin/jellyfin-plugin-webhook/blob/main/Jellyfin.Plugin.Webhook/Destinations/NotificationType.cs>

Template variables per README: Server (`ServerId, ServerName, ServerVersion, ServerUrl, NotificationType`), User (`NotificationUsername, Username, UserId, LastLoginDate, LastActivityDate`), Session (`DeviceName, DeviceId, ClientName, RemoteEndPoint`), BaseItem (`Name, ItemId, ItemType, RunTime, Year, ...` plus provider ids), Playback (`PlaybackPosition, PlayMethod, PlayedToCompletion, IsPaused, ...`).
Source: README at <https://github.com/jellyfin/jellyfin-plugin-webhook>

`PlaybackProgressNotifier` is an `IEventConsumer<PlaybackProgressEventArgs>`; it fires once per server-side progress event and emits one webhook per user in the session. `PlaybackStopNotifier` adds `dataObject[nameof(eventArgs.PlayedToCompletion)]`.
Sources: <https://github.com/jellyfin/jellyfin-plugin-webhook/blob/main/Jellyfin.Plugin.Webhook/Notifiers/PlaybackProgressNotifier.cs>, <https://github.com/jellyfin/jellyfin-plugin-webhook/blob/main/Jellyfin.Plugin.Webhook/Notifiers/PlaybackStopNotifier.cs>

Progress events originate from clients calling `POST /Sessions/Playing`, `POST /Sessions/Playing/Progress`, `POST /Sessions/Playing/Stopped` (stable OpenAPI opIds `ReportPlaybackStart`, `ReportPlaybackProgress`, `ReportPlaybackStopped`). `PlaybackStopInfo` carries `ItemId, SessionId, MediaSourceId, PositionTicks, PlaySessionId, Failed, ...`. Progress frequency therefore depends on the client, not the server.

### 3.6 Polling vs webhooks for watch-time accuracy

| Approach | Signal | Accuracy properties | Failure modes |
|---|---|---|---|
| Poll `GET /Sessions` every N s | `PlayState.PositionTicks`, `IsPaused`, `PlayMethod`, `NowPlayingItem` | Watch-time = sum of intervals where a session is present and not paused; resolution = N. Independent of plugins. Works with an API key. | Missing short plays < N; unpaused-but-stalled clients counted as playing; needs Bloom's own state machine (this is what Playback Reporting does in-process with a 20 s cadence). |
| Websocket `SessionsStart` | Same DTO, pushed on session events plus periodic | Same data as polling with lower latency; still needs Bloom's state machine. | Path not confirmed from server source in this research; reconnect handling required. |
| Webhook plugin (`PlaybackStart/Progress/Stop`) | `PlaybackPosition`, `IsPaused`, `PlayedToCompletion` | Event-driven; `PlaybackStop` gives an authoritative end and `PlayedToCompletion`. `PlaybackProgress` cadence = client cadence. | Requires plugin install and per-server webhook config; no delivery guarantees or retries documented in the README; Bloom must expose an HTTPS endpoint; events lost while Bloom is down are not replayed. |
| Playback Reporting SQL | `PlayDuration` seconds per (user, item, start) | Server-side, plugin-computed, survives Bloom downtime. | Admin-only plugin endpoint; SQL string over HTTP; 20 s granularity; schema is plugin-private. |
| `UserData.PlayCount/LastPlayedDate` | per (user, item) | Zero infrastructure, always available. | No durations, no per-session detail. |

Recommendation for Bloom: poll `/Sessions` (or use the websocket) as the baseline collector that works with an API key on any Jellyfin, and optionally ingest webhook `PlaybackStop` events when the admin has the plugin, using `PlayedToCompletion` and `PlaybackPosition` to correct the session's final position. Do not depend on Playback Reporting for correctness; offer it as an import source.

---

## 4. Jellyfin library and availability lookup

### 4.1 Provider-id lookup

There is no server-side provider-id filter on `/Items` in 12.1.0. The stable OpenAPI contains zero occurrences of `anyProviderIdEquals`, and the only provider-related `/Items` parameters are `hasImdbId`, `hasTmdbId`, `hasTvdbId` ("Optional filter by items that have a TMDb id or not.") plus `fields=ProviderIds`. A GitHub code search for `AnyProviderIdEquals` in `jellyfin/jellyfin` returns 0 results. (Emby still has it; see §6.)
Source: stable OpenAPI `/Items` parameters; <https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Api/Controllers/ItemsController.cs>

Working pattern (Jellyseerr): enumerate each library with
`/Items?SortBy=SortName&SortOrder=Ascending&IncludeItemTypes=Series,Movie,Others&Recursive=true&StartIndex=0&ParentId=${id}&collapseBoxSetItems=false` and `fields: 'ProviderIds,MediaSources,Width,Height,IsHD,DateCreated'`, then index `ProviderIds.Tmdb / Imdb / Tvdb` locally.
Source: <https://github.com/Fallenbagel/jellyseerr/blob/develop/server/api/jellyfin.ts>

`BaseItemDto.ProviderIds` is declared as `object` ("Gets or sets the provider ids.") — a string→string map. Key names (`Tmdb`, `Imdb`, `Tvdb`) come from the Jellyseerr type definition; they are not enumerated in the OpenAPI.

Title search: `/Items?searchTerm=` ("Optional. Filter based on a search term.") and `GET /Search/Hints`. The controller comment says "Use search providers when searchTerm is provided." Search is a fallback, not a deterministic key match.
Source: stable OpenAPI; ItemsController (link above).

### 4.2 Seasons and episodes

- `GET /Shows/{seriesId}/Seasons?userId&fields&isSpecialSeason&isMissing&enableUserData` → `BaseItemDtoQueryResult`
- `GET /Shows/{seriesId}/Episodes?userId&fields&season&seasonId&isMissing&startIndex&limit&enableUserData&sortBy` → `BaseItemDtoQueryResult`

`BaseItemDto` carries `IndexNumber` (episode number), `ParentIndexNumber` (season number), `SeriesId`, `SeasonId`, `ChildCount`, `RecursiveItemCount`, `RunTimeTicks`, `MediaSources`, `Path`, `LocationType`.
Source: stable OpenAPI `/Shows/{seriesId}/Seasons`, `/Shows/{seriesId}/Episodes`, `BaseItemDto`.

### 4.3 Detecting availability

- `isMissing` on `/Items`, `/Shows/{id}/Seasons`, `/Shows/{id}/Episodes`: "Optional filter by items that are missing episodes or not." Pass `isMissing=false` to exclude placeholder episodes created from metadata.
- `LocationType` enum: `FileSystem, Remote, Virtual, Offline`. `Virtual` items are metadata-only placeholders; `/Items` accepts `locationTypes` and `excludeLocationTypes`.
- `MediaSources` (request `fields=MediaSources`) is empty for items without a playable file (Jellyseerr requests it for exactly this purpose).

Sources: stable OpenAPI `/Items` parameters, `LocationType`; Jellyseerr `jellyfin.ts` (link above).

Caveats:

1. Provider ids are only as good as the server's metadata match. Items with no `ProviderIds` cannot be reconciled without title matching.
2. Per-user visibility: with an API key, `/Items` without `userId` returns the admin/global view. To answer "can user X see this?", pass `userId=X` so `EnabledFolders`, `BlockedTags` and parental limits apply.
3. `isMissing` and `Virtual` depend on the library's "display missing episodes" behavior and metadata refresh state; a freshly added file may show as missing until the library scan completes (`VirtualFolderInfo.RefreshStatus` / `RefreshProgress` expose scan state).

---

## 5. Jellyfin client SDKs for Go

### 5.1 OpenAPI publication

Stable spec: <https://api.jellyfin.org/openapi/jellyfin-openapi-stable.json> (`info.title` = "Jellyfin API", `info.version` = "12.1.0", 1.9 MB). The ReDoc browser is at <https://api.jellyfin.org/>; it is a single-page app and its prose is not fetchable without JavaScript.

### 5.2 Existing Go clients

- **sj14/jellyfin-go** — <https://github.com/sj14/jellyfin-go>. MIT. Latest release `v0.5.0` (2026-09-09), pushed 2026-09-18. `api/README.md`: "This API client was generated by the OpenAPI Generator project." — "API version: 12.1.0", "Generator version: 7.25.0", "Build package: org.openapitools.codegen.languages.GoClientCodegen". Auth is set by putting `MediaBrowser Token="<API_TOKEN>"` into the configuration's `DefaultHeader`. The README notes `configuration.go` was hand-edited (`ServerConfiguration` renamed `APIServerConfiguration`) and "ignored from further generations to avoid conflicts." Built for the author's `jellyctl`. 19 stars.
  Source: <https://github.com/sj14/jellyfin-go/blob/main/api/README.md>
- **shamelin/go-jellyfin-api-client** — <https://github.com/shamelin/go-jellyfin-api-client>. openapi-generator output; last push 2024-11-18; no releases; no license file detected via the GitHub API. Not suitable as a dependency.
- No Jellyfin-organization Go SDK exists under <https://github.com/jellyfin> (the official SDKs are Kotlin, TypeScript and .NET; not enumerated here).

### 5.3 Generate vs hand-write

oapi-codegen (<https://github.com/oapi-codegen/oapi-codegen>) supports "OpenAPI 3.0 and OpenAPI 3.1", generates client code, types and server boilerplate, and adds headers via `RequestEditorFn` ("use the client's generated `WithRequestEditorFn` function to pass in a given request editor `RequestEditorFn`"). Configuration is "a YAML configuration file, to simplify the number of flags that users need to remember", invoked via `//go:generate`.

Trade-offs for Bloom:

- Full generation from the 1.9 MB spec yields hundreds of operations and types; Bloom needs roughly 20. oapi-codegen's configuration schema defines `output-options.include-tags`, `exclude-tags`, `include-operation-ids` and `exclude-operation-ids`, so generation can be scoped to the operations Bloom uses.
  Source: <https://github.com/oapi-codegen/oapi-codegen/blob/main/configuration-schema.json>
- Generated types keep the `UserPolicy` field list in lockstep with the spec, which matters because `POST /Users/{userId}/Policy` is whole-object replace (§2.1): a hand-written struct that omits a new field would silently reset it to the zero value on write.
- The `MediaBrowser` header is one `RequestEditorFn`; no generator complication.
- Emby and Plex still need hand-written clients (Emby's document is Swagger 2.0 with trailing-comma JSON errors as shipped; Plex has no machine-readable spec at the cited URL).

Recommendation: generate Jellyfin **types** (at minimum `UserPolicy`, `UserDto`, `UserConfiguration`, `SessionInfoDto`, `PlayerStateInfo`, `TranscodingInfo`, `BaseItemDto`, `UserItemDataDto`, `ActivityLogEntry`, `VirtualFolderInfo`, `AuthenticationResult`, `QuickConnectResult`) from the pinned stable spec with oapi-codegen, and hand-write a thin transport (header, pagination, timeouts) around a scoped generated client or `net/http`. Pin the spec file in-repo and diff on upgrade.

---

## 6. Emby: differences from Jellyfin

Emby's developer docs: <https://dev.emby.media/doc/restapi/index.html>; API browser: <http://swagger.emby.media/?staticview=true>; Swagger 2.0 JSON: <https://github.com/MediaBrowser/Emby.SDK/blob/master/Documentation/Download/openapi_v2_noversion.json> (repo pushed 2026-09-16). Note the shipped JSON has trailing commas (`jq` fails at line 10) and must be repaired before tooling.

### 6.1 Auth

- Scheme name is `Emby`, not `MediaBrowser`. The `X-Emby-Authorization` parameter on `/Users/AuthenticateByName` reads: "The authorization header can be either named 'Authorization' or 'X-Emby-Authorization'. It must be of the following schema: Emby UserId="(guid)", Client="(string)", Device="(string)", DeviceId="(string)", Version="string", Token="(string)"".
- Token transport: "The return result object will have an AccessToken property, which should be included in all subsequent Http requests using the header X-Emby-Token." (<https://dev.emby.media/doc/restapi/User-Authentication.html>). Logout: `POST /Sessions/Logout`.
- API keys: created in Dashboard "Advanced > Security"; sent as header `X-Emby-Token` or query `api_key` (<https://dev.emby.media/doc/restapi/API-Key-Authentication.html>; Swagger `securityDefinitions.apikeyauth` = `api_key` in query).
- Jellyfin accepts `X-Emby-Token`/`X-Emby-Authorization` only under `EnableLegacyAuthorization` (§1.1), so Bloom needs two header strategies.

### 6.2 Users and policy

- Same endpoint shapes with different casing: `POST /Users/New` (body `CreateUserByName { Name, CopyFromUserId, UserCopyOptions }` — no `Password`; set it afterwards), `POST /Users/{Id}/Policy` ("Requires authentication as administrator"), `POST /Users/{Id}/Password`, `POST /Users/{Id}/Configuration`, `DELETE /Users/{Id}` and `POST /Users/{Id}/Delete`, `GET /Users/Query`.
- Emby `UserPolicy` fields: `IsAdministrator, IsHidden, IsHiddenRemotely, IsHiddenFromUnusedDevices, IsDisabled, LockedOutDate, MaxParentalRating, AllowTagOrRating, BlockedTags, IsTagBlockingModeInclusive, IncludeTags, EnableUserPreferenceAccess, AccessSchedules, BlockUnratedItems, EnableRemoteControlOfOtherUsers, EnableSharedDeviceControl, EnableRemoteAccess, EnableLiveTvManagement, EnableLiveTvAccess, EnableMediaPlayback, EnableAudioPlaybackTranscoding, EnableVideoPlaybackTranscoding, EnableTranscodingQuality, AutoRemoteQuality, EnablePlaybackRemuxing, EnableContentDeletion, RestrictedFeatures, EnableContentDeletionFromFolders, EnableContentDownloading, EnableSubtitleDownloading, EnableSubtitleManagement, EnableSyncTranscoding, EnableMediaConversion, EnabledChannels, EnableAllChannels, EnabledFolders, EnableAllFolders, InvalidLoginAttemptCount, EnablePublicSharing, RemoteClientBitrateLimit, AuthenticationProviderId, ExcludedSubFolders, SimultaneousStreamLimit, EnabledDevices, EnableAllDevices, AllowCameraUpload, AllowSharingPersonalItems`.
- Mapping notes: Emby `SimultaneousStreamLimit` ≈ Jellyfin `MaxActiveSessions`; Emby has no `LoginAttemptsBeforeLockout`, `SyncPlayAccess`, `EnableCollectionManagement`, `EnableLyricManagement`, `PasswordResetProviderId`; Jellyfin has no `IncludeTags`, `ExcludedSubFolders`, `AllowCameraUpload`, `RestrictedFeatures`.
- Emby keeps `/Users/{UserId}/Items`, `/Users/{UserId}/Views`, `/Users/{UserId}/Items/{ItemId}/UserData` as documented routes (Jellyfin hides them).

Source: Emby Swagger 2.0 `definitions.UserPolicy`, `definitions.CreateUserByName`, listed paths.

### 6.3 Sessions and playback

- `GET /Sessions` params `ControllableByUserId, DeviceId, Id`; "Requires authentication as user". `Session.SessionInfo` adds `Protocol, PlaylistIndex, PlaylistLength, PartyId, InternalDeviceId, AppIconUrl` over Jellyfin's DTO and lacks `IsActive`, `LastPlaybackCheckIn`, `LastPausedDate`, `NowPlayingQueue`, `HasCustomDeviceName`.
- `PlayerStateInfo` adds `MediaSource, SleepTimerMode, SleepTimerEndTime, SubtitleOffset, Shuffle, PlaybackRate`. `TranscodingInfo` is much richer (`CurrentCpuUsage, VideoDecoderHwAccel, ...`).
- Playback report endpoints are identical: `/Sessions/Playing`, `/Sessions/Playing/Progress`, `/Sessions/Playing/Stopped`.
- `GET /System/ActivityLog/Entries` exists ("Requires authentication as administrator").
- Provider-id lookup **is** server-side on Emby: `/Items?AnyProviderIdEquals=` — "Each provider ID must be in the form 'prov.id', e.g. 'imdb.tt123456'. This allows multiple, comma delimeted value pairs."

Source: Emby Swagger 2.0 (`/Sessions`, `Session.SessionInfo`, `PlayerStateInfo`, `TranscodingInfo`, `/Items` parameters).

Not confirmed: Emby equivalents of the Jellyfin Webhook or Playback Reporting plugins were not researched; Emby has no Quick Connect endpoints in the Swagger document (it has `/Connect/*` for Emby Connect linking instead).

---

## 7. Plex equivalents

### 7.1 Authentication (plex.tv PIN flow)

Official guide: <https://forums.plex.tv/t/authenticating-with-plex/609370>.

1. `POST https://plex.tv/api/v2/pins` with headers `X-Plex-Product` and `X-Plex-Client-Identifier`; response has `id` and `code`.
2. Send the user to `https://app.plex.tv/auth#?clientID=<id>&code=<code>&context%5Bdevice%5D%5Bproduct%5D=<appName>&forwardUrl=<url>` ("Auth App urls are encoded as parameters to the url fragment.").
3. Poll `GET https://plex.tv/api/v2/pins/<pinID>` with `code` and `X-Plex-Client-Identifier`; `authToken` appears when authorized.
4. Validate with `GET https://plex.tv/api/v2/user` and header `X-Plex-Token` (200 valid, 401 invalid).

The guide states the client identifier is opaque and that "once one is generated the client should store and re-use this identifier for subsequent requests." The PMS reference confirms "Most endpoints require token based authentication, and the token is expected to be sent in the `X-Plex-Token` header. Tokens are obtained from plex.tv." and "X-Plex-Client-Identifier is typically required, as is X-Plex-Token for authentication" (<https://developer.plex.tv/pms/>).

Consequence: Bloom needs the **server owner's** token to manage users and read sessions; there is no server-local admin API key. Tokens are account-scoped, not server-scoped.

### 7.2 Users: Plex Home, managed users, friend sharing

- Plex Home: "Free Plex accounts may have up to 14 managed users total in your Home (15 total users including the Home Admin account). Plex Pass users can also add regular Plex accounts to the home in addition to creating managed users (still bound by 15 total member limit)". "Managed Users do not have any email address associated". (<https://support.plex.tv/articles/203815766-what-is-plex-home/>)
- Managed accounts: "The administrator for a Plex Home has the ability to create Managed Accounts within that home." "Plex Pass users will be able to set custom restrictions based on label or content ratings." (<https://support.plex.tv/articles/203948776-managed-users/>)
- Friend sharing: "From inside the Manage Library Access settings, use the Grant Library Access button to open the invite wizard." "If you have a Plex Pass subscription you can set restrictions ... limit access by selecting specific content ratings from your library, as well as content that you've set with a specific 'label'." (<https://support.plex.tv/articles/201105738-creating-and-managing-server-shares/>). Restrictions: "Setting custom access restrictions when granting access to content requires an active Plex Pass subscription for the admin Plex Media Server account." (<https://support.plex.tv/articles/204232573-restricting-the-shares/>)

No official REST documentation exists for these plex.tv operations; python-plexapi is the de-facto reference (<https://github.com/pkkid/python-plexapi/blob/master/plexapi/myplex.py>):

- `HOMEUSERS = 'https://plex.tv/api/home/users'`; `createHomeUser` → `POST https://plex.tv/api/home/users?title={title}` then share via `FRIENDINVITE`.
- `createExistingUser` → `POST https://plex.tv/api/home/users?invitedEmail={username}`.
- `FRIENDINVITE = 'https://plex.tv/api/servers/{machineId}/shared_servers'` (POST to invite).
- `FRIENDSERVERS = 'https://plex.tv/api/servers/{machineId}/shared_servers/{serverId}'` (PUT/POST/DELETE), `FRIENDUPDATE = 'https://plex.tv/api/v2/sharings/{userId}'` (PUT/DELETE).
- Removal: `HOMEUSER = 'https://plex.tv/api/home/users/{userId}'` (DELETE) and `FRIENDUPDATE` DELETE.
- Sharing options: `allowSync`, `allowCameraUpload`, `allowChannels` ('1'/'0'), `filterMovies`/`filterTelevision` (`contentRating`/`label`), `filterMusic` (`label`).

Caveat: these are reverse-engineered, XML-returning endpoints subject to change without notice; Plex has changed sharing endpoints before (v1 `shared_servers` vs v2 `sharings`).

### 7.3 Sessions and history

- `GET /status/sessions` on the server returns "Video/Track elements with User, Player, Session, TranscodeSession, viewOffset" (<https://developer.plex.tv/pms/>).
- The reference lists "getList Playback History" and "getGet Single History Item" under Status; the exact path (`/status/sessions/history/all`) was not confirmed from the fetched page text.
- Tautulli approach (<https://github.com/Tautulli/Tautulli/blob/master/plexpy/web_socket.py>): connect to `ws://<host>:<port>/:/websockets/notifications` (or `wss://`) with `X-Plex-Token`; handle `playing`, `timeline`, `reachability` notification types; on `playing` it extracts `PlaySessionStateNotification` and hands it to `ActivityHandler.process()`, which then calls `pms_connect.get_current_activity()` (i.e. `/status/sessions`) to fetch full session detail and schedules per-`session_key` callbacks for start/stop/pause/resume/buffer (<https://github.com/Tautulli/Tautulli/blob/master/plexpy/activity_handler.py>). The websocket is not documented at <https://developer.plex.tv/pms/> ("Not present").

### 7.4 Library sections and provider ids

- `GET /library/sections` → "Directory elements with key, type, title"; `GET /library/metadata/{id}/children` "should return a MediaContainer object with an array of Metadata Objects for their Seasons and Episodes respectively." Paging via `X-Plex-Container-Size` / `X-Plex-Container-Start`.
- GUIDs: format "`{scheme}://{metadataType}/{ratingKey}`"; "Internally supported providers include: `imdb` - IMDb, `tmdb` - TheMovieDB, `tvdb` - TVDB"; metadata carries a "`Guid` Array (Optional)" with entries like "`imdb://tt0088763`".
  Source: <https://developer.plex.tv/pms/>
- Overseerr requests `/library/sections/${id}/all?includeGuids=1` and parses `metadata.Guid[].id` against `imdb://`, `tmdb://`, `tvdb://` regexes, falling back to legacy agent `guid` strings (`com.plexapp.agents.*`, HAMA) — and skips items with "No Guid metadata for this title." (<https://github.com/sct/overseerr/blob/develop/server/api/plexapi.ts>, <https://github.com/sct/overseerr/blob/develop/server/lib/scanners/plex/index.ts>). Whether the server supports a `guid=` query filter was not confirmed at the developer reference.

### 7.5 Webhooks (Plex Pass)

"Webhooks are a premium feature and require an active Plex Pass subscription for admin/owner account of the Plex Media Server." Events: `library.on.deck`, `library.new`, `media.pause`, `media.play`, `media.rate`, `media.resume`, `media.scrobble` ("Media is viewed (played past the 90% mark)."), `media.stop`, and owner-only `admin.database.backup`, `admin.database.corrupted`, `device.new`, `playback.started`. "the payload is sent in JSON format inside a multipart HTTP POST request." Payload parts: top-level `event`/`user`/`owner`, `Account` (id, title, thumb), `Server` (title, uuid), `Player` (local, publicAddress, title, uuid), `Metadata`. Webhooks "are tied to a specific user" and "travel" with the account; "Servers receive webhooks for the user who is signed into the server, as well as webhooks for shared users."
Source: <https://support.plex.tv/articles/115002267687-webhooks/>

Caveat: no position/progress event exists; watch-time must still come from session polling or the websocket.

---

## 8. Proposed minimal `MediaServer` interface (prose)

Bloom's adapter contract should expose the following operations. Each row notes support per server, based on the sections above.

| Operation | Jellyfin | Emby | Plex |
|---|---|---|---|
| **Connect / verify credentials** (server info + who-am-I) | `GET /Users/Me` (user token) or `GET /Auth/Keys` (API key, admin) | `GET /System/Info` + `X-Emby-Token` | `GET https://plex.tv/api/v2/user` with `X-Plex-Token`; server via `X-Plex-Token` |
| **Interactive login for an end user** | `POST /Users/AuthenticateByName`; Quick Connect initiate/poll | `POST /Users/AuthenticateByName` (`Emby` scheme) | plex.tv PIN flow (`/api/v2/pins`) — browser redirect required |
| **List users** | `GET /Users` | `GET /Users/Query` | plex.tv home users + shared_servers (undocumented) |
| **Create user with password** | `POST /Users/New {Name, Password}` | `POST /Users/New {Name}` then `POST /Users/{Id}/Password` | **Awkward**: managed user (no email, no password, Home-limited to 15) or invite by email (friend accepts) |
| **Delete user** | `DELETE /Users/{userId}` | `DELETE /Users/{Id}` | DELETE home user / sharing (undocumented) |
| **Set password / reset** | `POST /Users/Password?userId=` | `POST /Users/{Id}/Password` | **Unsupported** (accounts belong to plex.tv) |
| **Enable / disable** | `UserPolicy.IsDisabled` | `UserPolicy.IsDisabled` | Remove share / re-invite only |
| **Set library access** | `EnableAllFolders` / `EnabledFolders` (ids from `/Library/VirtualFolders`) | same | share `sections` on `shared_servers` |
| **Set permissions** (download, transcode, remote, sessions, bitrate, parental) | `UserPolicy` fields (§2.1) | `UserPolicy` (§6.2; `SimultaneousStreamLimit`) | `allowSync`, `allowChannels`, `filter*` (Plex Pass for restrictions); **no** transcoding/bitrate/session-limit per user |
| **Approve Quick Connect on behalf of user** | `POST /QuickConnect/Authorize?code&userId` | **Unsupported** | **Unsupported** |
| **List libraries** | `GET /Library/VirtualFolders` | `GET /Library/VirtualFolders/Query` | `GET /library/sections` |
| **Find item by provider id** | **Awkward**: enumerate with `fields=ProviderIds`, index locally | `GET /Items?AnyProviderIdEquals=tmdb.123` | **Awkward**: `.../all?includeGuids=1` and parse `Guid[]` |
| **Get seasons / episodes** | `/Shows/{id}/Seasons`, `/Shows/{id}/Episodes` | same | `/library/metadata/{id}/children` (twice) |
| **Availability check** | `isMissing=false`, `LocationType != Virtual`, `MediaSources` non-empty | `IsMissing`, `LocationType` (same ancestry; field-level parity not verified) | presence of `Media`/`Part` on metadata (not verified here) |
| **Active sessions snapshot** | `GET /Sessions` (`PositionTicks`, `IsPaused`, `PlayMethod`, `TranscodingInfo`) | `GET /Sessions` | `GET /status/sessions` (`viewOffset`, `Player`, `TranscodeSession`) |
| **Session push stream** | websocket `SessionsStart` (path not confirmed here) | not researched | `/:/websockets/notifications` (undocumented; Tautulli) |
| **Playback event webhooks** | Webhook plugin (`PlaybackStart/Progress/Stop`, `PlayedToCompletion`) | not researched | Plex Pass webhooks (`media.play/pause/resume/stop/scrobble`); no progress |
| **Historical per-user watch data** | `UserData` (`PlayCount`, `LastPlayedDate`); Activity Log types `VideoPlayback*`; Playback Reporting `PlaybackActivity` if installed | `UserData`; Activity Log | history endpoint listed in reference (path unconfirmed) |
| **Audit / auth events** | `/System/ActivityLog/Entries` (`AuthenticationSucceeded`, `SessionStarted`, `UserCreated`, ...) or webhook `AuthenticationSuccess` | `/System/ActivityLog/Entries` | `device.new` webhook (owner only) |

Design notes:

1. Model credentials as a tagged union: `{APIKey}` for Jellyfin/Emby (admin, no user identity, must pass `userId` explicitly) vs `{UserToken}` (user identity, permission-limited) vs `{PlexAccountToken, ClientIdentifier, MachineIdentifier}`.
2. Make "create user with password" return a capability error on Plex and expose "invite by email" and "create managed user" as separate optional operations.
3. Keep policy writes read-modify-write on Jellyfin/Emby because `POST .../Policy` replaces the entire object.
4. Treat the provider-id index as Bloom-owned state for Jellyfin and Plex; only Emby can answer it server-side.
5. The stats collector should be a per-server poller with an optional push source (webhook or websocket) layered on top; on all three servers the only universally available, plugin-free signal is the sessions snapshot.
