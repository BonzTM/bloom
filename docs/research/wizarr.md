# Wizarr research (for Bloom)

Research date: 2026-09-21. Sources: GitHub repo `wizarrrr/wizarr` (README, `docs/`, `app/` source at `main`, releases, 531 issues, 47 discussions) and https://docs.wizarr.dev. Every claim cites its source. Where a fact could not be confirmed, the text says so.

Source shorthand: `src:<path>` = `https://github.com/wizarrrr/wizarr/blob/main/<path>`.

---

## 1. What Wizarr is

| Fact | Value | Source |
|---|---|---|
| Purpose | "Wizarr is an advanced user invitation and management system for Jellyfin, Plex, Emby etc." | [repo description](https://github.com/wizarrrr/wizarr) |
| Pitch | "Create a unique invite link and share it with users — they'll be automatically added to your media server and guided through downloading apps, accessing request systems, and more!" | [README](https://github.com/wizarrrr/wizarr/blob/main/README.md) |
| License | MIT | [repo `licenseInfo`](https://github.com/wizarrrr/wizarr/blob/main/LICENSE.md) |
| Language | Python (primary); HTML templates; small JS/CSS | GitHub languages API for `wizarrrr/wizarr` |
| Runtime | `requires-python = ">=3.13"` | [`src:pyproject.toml`](https://github.com/wizarrrr/wizarr/blob/main/pyproject.toml) |
| Web framework | Flask 3 (`flask>=3.1.3`), Jinja2, `flask-htmx`, `flask-wtf`, `flask-login`, `flask-limiter`, `flask-session`, `flask-babel`, `flask-restx` (OpenAPI), `flask-apscheduler` | [`src:pyproject.toml`](https://github.com/wizarrrr/wizarr/blob/main/pyproject.toml) |
| Frontend | Server-rendered Jinja + HTMX + Alpine.js + Tailwind (repo ships `.claude/agents/htmx-frontend-specialist.md` and `tailwind-ui-stylist.md`; release notes bump `alpinejs` in `/app/static`) | [repo tree](https://github.com/wizarrrr/wizarr/tree/main/.claude/agents), [v2026.4.0 notes](https://github.com/wizarrrr/wizarr/releases/tag/v2026.4.0) |
| ORM / DB | SQLAlchemy 2 via Flask-SQLAlchemy; Alembic via Flask-Migrate; SQLite only: `SQLALCHEMY_DATABASE_URI = f"sqlite:///{DATABASE_DIR / 'database.db'}"` | [`src:app/config.py`](https://github.com/wizarrrr/wizarr/blob/main/app/config.py) |
| Server | gunicorn; packaged with `uv` | [`src:pyproject.toml`](https://github.com/wizarrrr/wizarr/blob/main/pyproject.toml), [`src:gunicorn.conf.py`](https://github.com/wizarrrr/wizarr/blob/main/gunicorn.conf.py) |
| Other deps | `plexapi`, `ldap3`, `webauthn`, `apprise`, `nh3` (HTML sanitizer), `cryptography`, `structlog` | [`src:pyproject.toml`](https://github.com/wizarrrr/wizarr/blob/main/pyproject.toml) |
| Versioning | "this project uses Calendar Versioning" | [`src:CHANGELOG.md`](https://github.com/wizarrrr/wizarr/blob/main/CHANGELOG.md) |
| Distribution | Docker image `ghcr.io/wizarrrr/wizarr`, port 5690, volume `/data`, env `PUID`/`PGID`/`TZ`/`DISABLE_BUILTIN_AUTH` | [docs: installation](https://github.com/wizarrrr/wizarr/blob/main/docs/getting-started/installation.md) |
| Created | 2022-09-12 | GitHub repo API |
| Stars / forks | 3,196 stars, 200 forks (2026-09-21) | GitHub repo API |
| Contributors | 136 (GitHub contributors API, paginated) | https://github.com/wizarrrr/wizarr/graphs/contributors |
| Latest release | `v2026.9.1`, published 2026-09-14 | [release](https://github.com/wizarrrr/wizarr/releases/tag/v2026.9.1) |
| Release cadence (last 12 months) | 2025.10.7, .10.8, .11.0–.11.3, .12.0, 2026.2.0, .2.1, .3.0, .4.0, .7.0, .7.1, .9.0, .9.1 | `gh release list` |
| Last push | 2026-09-21 | GitHub repo API `pushedAt` |
| History note | The project was once headed for a TypeScript rewrite ("The magic of Python has gracefully passed its wand to the dynamic charm of TypeScript") which did not ship; README now says "Development Relaunched" and the code is Flask. | [discussion #323](https://github.com/orgs/wizarrrr/discussions/323), [README](https://github.com/wizarrrr/wizarr/blob/main/README.md) |

### Supported media servers

- README: "Automatic invitations for Plex, Jellyfin, Emby, AudiobookShelf, Komga, Kavita and Romm" — [README](https://github.com/wizarrrr/wizarr/blob/main/README.md).
- Code registers nine clients via `@register_media_client(...)`: `plex`, `jellyfin`, `emby`, `audiobookshelf`, `romm`, `komga`, `kavita`, `navidrome`, `drop` — [`src:app/services/media/`](https://github.com/wizarrrr/wizarr/tree/main/app/services/media) (`navidrome.py`: "Navidrome wrapper using the Subsonic API"; `drop.py`: "Drop is a digital media management platform").
- The invitation account-manager registry only lists seven types (`plex, jellyfin, emby, audiobookshelf, romm, kavita, komga`) with a `FormBasedAccountManager` fallback for anything else — [`src:app/services/invitation_flow/server_registry.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invitation_flow/server_registry.py).
- Unconfirmed: whether Navidrome/Drop are exposed in the admin UI. Issue [#765 "Add Navidrome"](https://github.com/wizarrrr/wizarr/issues/765) is still OPEN despite the client file existing.
- Docs drift: the docs.wizarr.dev intro still says only "Plex, Jellyfin and Emby" — [docs/README.md](https://github.com/wizarrrr/wizarr/blob/main/docs/README.md), https://docs.wizarr.dev.
- Plex account creation is OAuth-based ("OAuth token required for Plex"); all others are form-based username/password — [`src:app/services/invitation_flow/server_registry.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invitation_flow/server_registry.py).

---

## 2. Feature inventory

### Invites
| Feature | Description | Source |
|---|---|---|
| Invite link `/j/<code>` | Public invite URL; code stored server-side in session | [`src:app/blueprints/public/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/public/routes.py), [`src:app/services/invite_code_manager.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invite_code_manager.py) |
| Random or custom code | Generated: 10 chars `A-Z0-9`; admin may supply 6–10 chars; case-insensitive lookup | [`src:app/services/invites.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invites.py) |
| Invite expiry | `day` / `week` / `month` / `never` | [`src:app/services/invites.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invites.py) |
| Unlimited vs single-use | `unlimited` flag; limited invite marked `used` once all servers consumed | [`src:app/services/invites.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invites.py) |
| Membership duration | `duration` = days until user expiry ("Time-limited membership options") | [README](https://github.com/wizarrrr/wizarr/blob/main/README.md), [`src:app/services/expiry.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/expiry.py) |
| Multi-server invites | One invite → many servers; per-server `used`/`expires` in `invitation_server`; Plex servers ordered first | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py), [`src:app/services/invites.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invites.py) |
| Library scoping per invite | `invite_library` M2M; picker validation rejects "opened and then cleared"; default = all enabled libraries | [`src:app/services/invites.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invites.py) |
| Per-invite permission toggles | `allow_downloads`, `allow_live_tv`, `allow_mobile_uploads`, `plex_home`, `max_active_sessions` (Jellyfin) | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |
| Wizard bundle per invite | `wizard_bundle_id` FK | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |
| Create LDAP user flag | `create_ldap_user` per invite | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |
| Delete-invite confirmation modal | "confirm invitation deletion with a modal" | [v2026.9.0](https://github.com/wizarrrr/wizarr/releases/tag/v2026.9.0) |

### User creation (join flow)
| Feature | Description | Source |
|---|---|---|
| Join form validation | Username 3–15 chars `^[\w'.-]+$`; email required; password ≥8 with upper+lower+digit; code 6–10 | [`src:app/forms/join.py`](https://github.com/wizarrrr/wizarr/blob/main/app/forms/join.py), [`src:app/forms/validators.py`](https://github.com/wizarrrr/wizarr/blob/main/app/forms/validators.py) |
| Plex OAuth join | `/join` legacy route; popup closed so token isn't lost | [`src:app/blueprints/public/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/public/routes.py), [v2026.9.0](https://github.com/wizarrrr/wizarr/releases/tag/v2026.9.0) |
| Password prompt / auto-generate | `/j/<code>/password`: "generate strong password if checkbox ticked or blank" | [`src:app/blueprints/public/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/public/routes.py) |
| Identity linking | `Identity` groups accounts across servers by email/username | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |
| Companion-app user creation | Ombi / Overseerr(Jellyseerr) / AudiobookRequest connections per media server | [`src:app/services/companions/`](https://github.com/wizarrrr/wizarr/tree/main/app/services/companions), [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) (`Connection`) |

### User management
| Feature | Description | Source |
|---|---|---|
| User list, delete, enable/disable | API `DELETE /users/{id}`, `POST /users/{id}/enable|disable` | [`src:app/blueprints/api/api_routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/api/api_routes.py) |
| Extend / set expiry | `POST /users/{id}/extend`, `PUT /users/{id}/update-expiry` | same |
| Password reset link for media users | `POST /users/{id}/reset-password`; public `/reset/<code>`; admin modal `password-reset-link.html` | same; [`src:app/services/password_reset.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/password_reset.py); [templates](https://github.com/wizarrrr/wizarr/tree/main/app/templates/modals) |
| Notes per user | `User.notes` | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |
| Cached user metadata | `is_admin`, `allow_downloads`, `allow_live_tv`, `allow_camera_upload`, `accessible_libraries` synced from server | same |
| User sync guard | "don't delete every local user when Plex returns an empty user list"; "apply the empty-remote user-sync guard to every backend" | [v2026.9.0](https://github.com/wizarrrr/wizarr/releases/tag/v2026.9.0) |
| Companion cleanup on delete | `_delete_from_companion_apps` removes user from Ombi/Overseerr | [`src:app/services/media/service.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/media/service.py) |

### Permissions / libraries
| Feature | Description | Source |
|---|---|---|
| Library scan per server | `Library(external_id, name, enabled, server_id)`, unique per server; startup scan | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py), [v2026.9.0](https://github.com/wizarrrr/wizarr/releases/tag/v2026.9.0) |
| Server-level default toggles | `MediaServer.allow_downloads/allow_live_tv/allow_mobile_uploads` | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |
| Update user libraries/permissions | `update_user_libraries`, `update_user_permissions` in client base | [`src:app/services/media/client_base.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/media/client_base.py) |

### Expiry / auto-delete
| Feature | Description | Source |
|---|---|---|
| Scheduled expiry job | Runs every 15 min in production (1 min dev) | [`src:app/tasks/maintenance.py`](https://github.com/wizarrrr/wizarr/blob/main/app/tasks/maintenance.py) |
| Delete or disable on expiry | `expiry_action` setting: `delete` (default) or `disable` where server supports it, with delete fallback | [`src:app/services/expiry.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/expiry.py) |
| Expired-user audit | `ExpiredUser` table, unique per `(original_user_id, expired_at)` | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |
| "Expiring this week" dashboard card | `get_expiring_this_week_users` | [`src:app/services/expiry.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/expiry.py) |

### Onboarding wizard
| Feature | Description | Source |
|---|---|---|
| DB-backed Markdown steps per server type | "Database-backed steps (recommended)" | [docs: customise-steps](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/customise-steps.md) |
| Pre-invite and post-invite categories | "Users cannot skip pre-invite steps if they're configured. The system enforces completion through server-side validation" | [docs: pre-post-invite-steps](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/pre-post-invite-steps.md) |
| Wizard bundles | "custom onboarding sequences that can be assigned to specific invitations" | [docs: customise-steps](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/customise-steps.md) |
| Multi-server combined flow | "Wizard automatically combines steps from each server type" | same |
| Conditional steps (`requires`) | "Comma-separated setting keys that must be truthy for the step to display" | same |
| Require interaction | "users must click a link or button in the step before they can proceed" | same |
| Drag-and-drop ordering | "Changes are saved automatically" | [docs: pre-post-invite-steps](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/pre-post-invite-steps.md) |
| Sandboxed templating | Variables `server_name, server_type, server_url, external_url`; "Templates cannot use loops, assignments, macros, template imports, or arbitrary function calls"; "Wizarr removes scripts, event handlers, executable links" | [docs: customise-steps](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/customise-steps.md), [v2026.9.1 "restrict wizard template rendering"](https://github.com/wizarrrr/wizarr/releases/tag/v2026.9.1) |
| Widgets | `widget:button`, `widget:recently_added_media`, Discord widget iframe | same docs; [#940](https://github.com/wizarrrr/wizarr/issues/940) |
| Import/export bundles, presets | `wizard_export_import.py`, `wizard_presets.py` | [`src:app/services/`](https://github.com/wizarrrr/wizarr/tree/main/app/services) |
| Legacy file-based fallback | `wizard_steps/<type>/NN_name.md` | [docs: customise-steps](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/customise-steps.md) |
| Wizard ACL | "Set the `wizard_acl_enabled` setting to `false` to allow unrestricted access" | same |

### Admin auth / SSO
| Feature | Description | Source |
|---|---|---|
| Multi-admin accounts | `AdminAccount` with scrypt hash | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |
| Passkeys (WebAuthn) as 2FA | If account has passkeys, login requires 2FA | [`src:app/blueprints/auth/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/auth/routes.py), [`src:app/blueprints/webauthn/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/webauthn/routes.py) |
| Proxy SSO | "Wizarr supports SSO via disabling its inbuilt authentication" (`DISABLE_BUILTIN_AUTH=True`) with path whitelist | [docs: SSO](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/single-sign-on-sso.md) |
| LDAP admin login + LDAP user creation | "Admin Login via LDAP", "Automatic User Creation", "Group-based Authorization" | [docs: LDAP](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/ldap-authentication.md), [v2026.3.0](https://github.com/wizarrrr/wizarr/releases/tag/v2026.3.0) |
| OIDC | Not confirmed. `AdminAccount.auth_source` comment says "LDAP/OIDC" and a Fernet key is named `LDAP_OIDC_ENCRYPTION_KEY`, but no OIDC flow found in `auth/routes.py`. | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py), [`src:app/services/ldap/encryption.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/ldap/encryption.py) |
| Recovery CLI | `uv run recovery_tool.py`: reset password, remove passkeys, create emergency admin | [docs: recovery](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/password-passkey-recovery.md) |

### Integrations
| Feature | Description | Source |
|---|---|---|
| Ombi / Overseerr / Jellyseerr | `Connection.connection_type` 'ombi' or 'overseerr'; "info-only" connection allowed | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py), [v2026.9.0](https://github.com/wizarrrr/wizarr/releases/tag/v2026.9.0) |
| AudiobookRequest | `companions/audiobookrequest.py` | [`src:app/services/companions/`](https://github.com/wizarrrr/wizarr/tree/main/app/services/companions) |
| Discord | Server-ID widget: "the Discord API dynamically generate an invitation link for the purpose of the widget" | [docs: Discord](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/discord-integration.md) |
| Update check | `update_check.py` task; `update_available` notification event | [`src:app/tasks/update_check.py`](https://github.com/wizarrrr/wizarr/blob/main/app/tasks/update_check.py), [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |

### API
| Feature | Description | Source |
|---|---|---|
| REST API with Swagger | `/api/docs/`, `/api/swagger.json`; header `X-API-Key` | [docs/API.md](https://github.com/wizarrrr/wizarr/blob/main/docs/API.md) |
| Namespaces | status, admins, users (list/delete/enable/disable/extend/update-expiry/reset-password), invitations (list/create/delete), libraries, servers, api-keys | [`src:app/blueprints/api/api_routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/api/api_routes.py) |
| API key management | Settings → API Keys; "Copy the generated key (shown only once)" | [docs/API.md](https://github.com/wizarrrr/wizarr/blob/main/docs/API.md) |

### Notifications
| Feature | Description | Source |
|---|---|---|
| Agents | discord, ntfy, apprise, notifiarr, telegram | [`src:app/services/notifications.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/notifications.py) |
| Events | default `user_joined,update_available` | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |
| Email/SMTP | Not present (see asks) | [discussion #737](https://github.com/orgs/wizarrrr/discussions/737) |

### Activity / stats (scope creep relative to invites)
| Feature | Description | Source |
|---|---|---|
| Now-playing + session history | `ActivitySession`, `ActivitySnapshot`; collectors for jellyfin/emby/audiobookshelf; historical importers incl. Plex | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py), [`src:app/activity/`](https://github.com/wizarrrr/wizarr/tree/main/app/activity) |
| Toggle | "add activity tracking toggle" after RAM complaints | [v2026.9.0](https://github.com/wizarrrr/wizarr/releases/tag/v2026.9.0), [#1363](https://github.com/wizarrrr/wizarr/issues/1363) |
| Dashboard cards | server health, stats, recent content, library breakdown, accepted invites | [`src:app/templates/admin/`](https://github.com/wizarrrr/wizarr/tree/main/app/templates/admin) |

### i18n
| Feature | Description | Source |
|---|---|---|
| 25 locales | en, ca, cs, da, de, es, fa, fr, gsw, he, hr, hu, is, it, lt, nb_NO, nl, pl, pt, pt_BR, ro, ru, sv, zh_Hans, zh_Hant; `FORCE_LANGUAGE` env | [`src:app/config.py`](https://github.com/wizarrrr/wizarr/blob/main/app/config.py) |
| Weblate | "We use Weblate to make Wizarr accessible in many languages." | [README](https://github.com/wizarrrr/wizarr/blob/main/README.md) |

### Ops
| Feature | Description | Source |
|---|---|---|
| `/health` endpoint | public | [`src:app/blueprints/public/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/public/routes.py) |
| Image proxy | `/image-proxy` for artwork | same |
| `HOST`/`PORT` env | "lets users override the address on which gunicorn binds" | [v2026.7.0](https://github.com/wizarrrr/wizarr/releases/tag/v2026.7.0) |
| SQLite WAL checkpoint task | daily `PRAGMA wal_checkpoint(PASSIVE)` | [`src:app/tasks/maintenance.py`](https://github.com/wizarrrr/wizarr/blob/main/app/tasks/maintenance.py) |

---

## 3. Data model

All from [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) unless noted. Migrations: [`src:migrations/versions/`](https://github.com/wizarrrr/wizarr/tree/main/migrations/versions) (51 files, from `20250522_create_database.py` to `20260901_make_expired_user_events_unique.py`).

| Table | Key columns / notes |
|---|---|
| `invitation` | `code`, `used`, `used_at`, `created`, `expires`, `unlimited`, `duration` (String, days), `specific_libraries` (legacy String), `plex_allow_sync`, `plex_home`, `plex_allow_channels`, `server_id` (legacy single server), `wizard_bundle_id`, `allow_downloads`, `allow_live_tv`, `allow_mobile_uploads`, `max_active_sessions`, `create_ldap_user`, `used_by_id` ("DEPRECATED") |
| `invitation_server` (M2M) | `invite_id`, `server_id`, `used`, `used_at`, `expires` (per-server override) |
| `invitation_user` (M2M) | `invite_id`, `user_id`, `used_at`, `server_id` |
| `invite_library` (M2M) | `invite_id`, `library_id` |
| `user` | `token` (server-side user id), `username`, `email` (nullable), `code` (invite code), `photo`, `expires`, `server_id`, `identity_id`, `notes`, `is_disabled`, `is_ldap_user`, `is_admin`, `allow_downloads`, `allow_live_tv`, `allow_camera_upload`, `accessible_libraries` (JSON text), `created_at`, `library_access_json` (legacy) |
| `identity` | `primary_email`, `primary_username`, `nickname` — groups one person's accounts across servers |
| `media_server` | `name`, `server_type`, `url`, `api_key` (plain String), `external_url`, `allow_downloads`, `allow_live_tv`, `allow_mobile_uploads`, `verified`, `created_at` |
| `library` | `external_id`, `name`, `enabled`, `server_id`; unique `(external_id, server_id)` |
| `settings` | key/value String rows (e.g. `admin_username`, `expiry_action`, `wizard_acl_enabled`, `discord_id`, `overseerr_url`, `ombi_api_key`, `server_name`) — keys from [`src:app/forms/settings.py`](https://github.com/wizarrrr/wizarr/blob/main/app/forms/settings.py), [`src:app/services/expiry.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/expiry.py) |
| `admin_account` | `username` (unique), `password_hash` (scrypt), `auth_source` ('local'), `external_id` (LDAP DN) |
| `webauthn_credential` | `admin_account_id`, `credential_id`, `public_key`, `sign_count`, `name`, `last_used_at` |
| `api_key` | `name`, `key_hash` (unique), `created_by_id`, `last_used_at`, `is_active` |
| `password_reset_token` | `code` (unique), `user_id`, `expires_at`, `used`, `used_at` |
| `expired_user` | `original_user_id`, `username`, `email`, `invitation_code`, `server_id`, `expired_at`, `deleted_at` |
| `notification` | `name`, `type`, `url`, `username`, `password` (plain), `channel_id`, `telegram_bot_token`, `telegram_chat_id`, `notification_events` |
| `ombi_connection` (model `Connection`) | `connection_type` ('ombi'/'overseerr'), `name`, `url`, `api_key`, `media_server_id` |
| `wizard_step` | `server_type`, `category` ('pre_invite'/'post_invite'), `position`, `title`, `markdown`, `requires` (JSON), `require_interaction`; unique `(server_type, category, position)` |
| `wizard_bundle` / `wizard_bundle_step` | named ordered collection of steps; unique `(bundle_id, position)` |
| `activity_session` / `activity_snapshot` / `historical_import_job` | playback tracking (see §2 Activity) |
| `ldap_configuration` / `ldap_group` | connection, service account (`service_account_password_encrypted`), search filters, attribute mappings, `admin_group_dn`, `allow_admin_bind` |

Observations grounded in the model:
- Library selection is stored twice (`invite_library` rows and legacy `specific_libraries` String); user library access is stored as JSON text in two columns. Related bugs: [#1103](https://github.com/wizarrrr/wizarr/issues/1103), [#546](https://github.com/wizarrrr/wizarr/issues/546), [#1032](https://github.com/wizarrrr/wizarr/issues/1032).
- Three generations of admin auth coexist: `Settings` rows (`admin_username`/`admin_password`), `AdminAccount`, and the `DISABLE_BUILTIN_AUTH` pseudo-admin — [`src:app/blueprints/auth/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/auth/routes.py), [`src:app/middleware.py`](https://github.com/wizarrrr/wizarr/blob/main/app/middleware.py).
- Datetimes are naive in SQLite and re-tagged as UTC at read time ("Make database datetime timezone-aware (assumes UTC)") — [`src:app/services/invites.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invites.py). Related bug: [#586](https://github.com/wizarrrr/wizarr/issues/586).

---

## 4. User asks (ranked)

Ranking: GitHub reactions (👍 etc.) then comment count. Issue totals per GitHub search API (`repo:wizarrrr/wizarr is:issue`): 106 open, 425 closed, 531 total. The `gh issue list --state all --limit 1000` sample analyzed here returned 333 issues (177 open / 156 closed); the two APIs disagree on the open count and the discrepancy is unexplained. Discussions from GraphQL (47 total). "D" = discussion upvotes.

| # | Ref | Title | Reacts | Cmts | State | Summary |
|---|---|---|---|---|---|---|
| 1 | [#934](https://github.com/wizarrrr/wizarr/issues/934) | Grimmory (formerly BookLore) integration | 22 | 2 | OPEN | Another book server; see also #1094 |
| 2 | [#484](https://github.com/wizarrrr/wizarr/issues/484) | Password reset for users | 21 | 6 | OPEN | "so Users can reset their own password or at least that an admin can send a password reset link… Currently I have to also run jfa-go". Admin-generated links now exist (PR #1020 referenced in thread); self-service by email does not. |
| 3 | [#1033](https://github.com/wizarrrr/wizarr/issues/1033) | Authentik integration | 21 | 4 | OPEN | Create IdP users on invite accept via Authentik API with groups |
| 4 | [#1094](https://github.com/wizarrrr/wizarr/issues/1094) | Add support for Booklore | 21 | 1 | OPEN | Duplicate of #934 |
| 5 | [D#737](https://github.com/orgs/wizarrrr/discussions/737) | Integrate SMTP | 17 D | 1 | OPEN | "Send Invitations… Expiration Reminders… Join Notifications… password resets" |
| 6 | [#757](https://github.com/wizarrrr/wizarr/issues/757) | Calibre-Web integration | 14 | 1 | OPEN | eBook server |
| 7 | [#509](https://github.com/wizarrrr/wizarr/issues/509) | Stripe / PayPal integration | 14 | 0 | CLOSED | Closed 2025-06 with no comment |
| 8 | [D#398](https://github.com/orgs/wizarrrr/discussions/398) | password reset | 14 D | 2 | OPEN | Same as #484 |
| 9 | [#869](https://github.com/wizarrrr/wizarr/issues/869) | Jellyseerr account emails not populating | 13 | 2 | OPEN | Bug: "imported into Jellyseer does not have an email linked"; resubmission of #526; also #332 (2024) |
| 10 | [#765](https://github.com/wizarrrr/wizarr/issues/765) | Add Navidrome | 12 | 2 | OPEN | Client file exists in `main`; issue still open |
| 11 | [#522](https://github.com/wizarrrr/wizarr/issues/522) | Support for Audiobookshelf | 11 | 6 | CLOSED | Shipped |
| 12 | [#309](https://github.com/wizarrrr/wizarr/issues/309) | Auto-apply restrictions to invited user | 10 | 4 | OPEN | Plex content ratings / labels per invite (since 2023) |
| 13 | [#728](https://github.com/wizarrrr/wizarr/issues/728) | Remove email requirement | 8 | 8 | OPEN | "in a small setup no one wants to deal with email"; also #108 |
| 14 | [#652](https://github.com/wizarrrr/wizarr/issues/652) | LLDAP account creation | 8 | 5 | CLOSED | Shipped v2026.3.0 |
| 15 | [D#1030](https://github.com/orgs/wizarrrr/discussions/1030) / [D#1031](https://github.com/orgs/wizarrrr/discussions/1031) | Booklore / Navidrome support | 8 D each | — | OPEN | Duplicates of above |
| 16 | [#927](https://github.com/wizarrrr/wizarr/issues/927) | Send step content via mail | 7 | 0 | OPEN | Email wizard content after accept |
| 17 | [#141](https://github.com/wizarrrr/wizarr/issues/141) | Telegram integration | 7 | 0 | CLOSED | Telegram notification agent exists |
| 18 | [#940](https://github.com/wizarrrr/wizarr/issues/940) + [#1167](https://github.com/wizarrrr/wizarr/issues/1167) | Select libraries for recently-added widget | 6+4 | 1 | OPEN | Widget shows first library alphabetically |
| 19 | [#487](https://github.com/wizarrrr/wizarr/issues/487) + [D#1079](https://github.com/orgs/wizarrrr/discussions/1079) | Existing users generate invites (referrals) | 6 | 0 | OPEN | Tiered invite quotas by "distance" from admin |
| 20 | [D#443](https://github.com/orgs/wizarrrr/discussions/443) | Personalize Wizarr (frontend) | 6 D | 1 | OPEN | Branding; see also #804 |
| 21 | [#483](https://github.com/wizarrrr/wizarr/issues/483) | White Screen of Death | 5 | 19 | CLOSED | Pain: service-worker cache lock-in; fixed v2026.7.0 |
| 22 | [#693](https://github.com/wizarrrr/wizarr/issues/693) | Komga 401 on setup | 5 | 8 | CLOSED | Integration auth bug |
| 23 | [#412](https://github.com/wizarrrr/wizarr/issues/412) | Emby Connect | 5 | 3 | OPEN | Link Emby Connect email at invite |
| 24 | [#1155](https://github.com/wizarrrr/wizarr/issues/1155) + [#113](https://github.com/wizarrrr/wizarr/issues/113) | SMTP: send invite link / announcements | 5+5 | 1 | OPEN/CLOSED | "migration to wizarr from jfa-go… missing a feature to send out the invitation link" |
| 25 | [#671](https://github.com/wizarrrr/wizarr/issues/671) | Nextcloud/Tautulli or generic "Other" server type | 5 | 0 | OPEN | "server type of 'Other' without any integration, where you could create steps" |
| 26 | [#1240](https://github.com/wizarrrr/wizarr/issues/1240) | Shelfmark support | 5 | 1 | OPEN | Another book app |
| 27 | [#488](https://github.com/wizarrrr/wizarr/issues/488) | Admin extend user expiration | 5 | 0 | CLOSED | Shipped (`/users/{id}/extend`) |
| 28 | [D#704](https://github.com/orgs/wizarrrr/discussions/704) | Login log path for Fail2Ban | 5 D | 1 | OPEN | "Wizarr does not have native MFA support either" (passkeys later added) |
| 29 | [D#515](https://github.com/orgs/wizarrrr/discussions/515) | External address for invites | 5 D | 2 | OPEN | Invite links use internal URL |
| 30 | [#804](https://github.com/wizarrrr/wizarr/issues/804) | Customize invite acceptance screens / Plex OAuth app name | 4 | 4 | OPEN | "hardcoded to say 'Wizarr would like to sign in to your Plex account'" |
| 31 | [#1103](https://github.com/wizarrrr/wizarr/issues/1103), [#546](https://github.com/wizarrrr/wizarr/issues/546), [#1032](https://github.com/wizarrrr/wizarr/issues/1032) | Library selection not saved / not honored / UI shows all checked | 4/2/1 | 2/3/5 | CLOSED/CLOSED/OPEN | "can be frightening if you have libraries that should not be shared" |
| 32 | [#588](https://github.com/wizarrrr/wizarr/issues/588) | Plex label exclusion per invite | 4 | 2 | OPEN | Related to #309 |
| 33 | [#684](https://github.com/wizarrrr/wizarr/issues/684) | Dashboard for users | 4 | 1 | OPEN | Self-service portal: expiry, permissions, libraries |
| 34 | [D#669](https://github.com/orgs/wizarrrr/discussions/669) | Protect settings with MFA | 4 D | 0 | OPEN | Passkey 2FA since added; TOTP not found |
| 35 | [#771](https://github.com/wizarrrr/wizarr/issues/771), [#738](https://github.com/wizarrrr/wizarr/issues/738), [D#1080](https://github.com/orgs/wizarrrr/discussions/1080) | Copy settings from template user (Emby/Jellyfin) | 3/3/2 | 1/1/1 | OPEN | "similar to JFA-Go, so that all settings from this select user are applied" |
| 36 | [#1208](https://github.com/wizarrrr/wizarr/issues/1208) | Generic invite landing page with manual code entry | 3 | 0 | OPEN | "/join or /invite… consistent entry point" |
| 37 | [#1172](https://github.com/wizarrrr/wizarr/issues/1172) | API endpoint for server version | 3 | 0 | OPEN | For Argus release monitoring |
| 38 | [#621](https://github.com/wizarrrr/wizarr/issues/621) | Inactive user deletion & notifications | 2 | 5 | OPEN | Purge after N months inactivity with "trusted" exclusions |
| 39 | [#476](https://github.com/wizarrrr/wizarr/issues/476) | Postgres as database | 2 | 5 | CLOSED | "For users using kubernetes this is a crucial feature"; config still SQLite-only |
| 40 | [#586](https://github.com/wizarrrr/wizarr/issues/586) | Expire time resets to 00:00 | 2 | 2 | OPEN | Datetime handling bug |
| 41 | [#732](https://github.com/wizarrrr/wizarr/issues/732) | Name/custom variables in invite for wizard | 2 | 1 | OPEN | "Hi {name}!" personalization |
| 42 | [#1261](https://github.com/wizarrrr/wizarr/issues/1261) | LDAP user filter | 2 | 0 | OPEN | Sync imports service accounts |
| 43 | [#1189](https://github.com/wizarrrr/wizarr/issues/1189) | Add email to existing users | 2 | 0 | OPEN | Needed for password reset |
| 44 | [D#477](https://github.com/orgs/wizarrrr/discussions/477) | Run under subpath | 2 D | 0 | OPEN | Docs rely on Caddy `replace-response` rewriting |

### Recurring pain-point clusters (from issues, not ranked)
- Container startup / permissions / migrations after upgrade: [#590](https://github.com/wizarrrr/wizarr/issues/590) (`/.cache/uv` denied), [#802](https://github.com/wizarrrr/wizarr/issues/802) (`uv.lock` not writable as non-root), [#748](https://github.com/wizarrrr/wizarr/issues/748) / [#747](https://github.com/wizarrrr/wizarr/issues/747) (schema errors on latest image), [#991](https://github.com/wizarrrr/wizarr/issues/991) ("Docker crashes on startup after update, unable to roll back").
- Library scoping correctness: #1103, #546, #1032 (above) plus v2026.9.0 fixes "stop library scans resetting the invite library defaults", "don't disable libraries on a partial or empty scan" — [release](https://github.com/wizarrrr/wizarr/releases/tag/v2026.9.0).
- Companion sync (Jellyseerr email): #869, #332.
- Resource use from activity tracking: #1363 → toggle in v2026.9.0.
- Frontend caching WSOD: #483 → v2026.7.0.
- Telemetry surprise: [D#401 "What is sentry.wizarr.dev?"](https://github.com/orgs/wizarrrr/discussions/401) (2024; Sentry not found in current `pyproject.toml`).
- Translations stalled at one point: [#516](https://github.com/wizarrrr/wizarr/issues/516) "It looks like Weblate is not being used anymore" (Weblate active again per README and v2026.3.0 i18n commits).

---

## 5. Gaps and opportunities for Bloom (grounded in the asks above)

1. **Email as a first-class channel.** SMTP is the top discussion (D#737, 17 upvotes) and recurs in #1155, #927, #113, #484, #1189. Wizarr has five push agents but no email. Bloom: SMTP settings + templates for invite delivery, expiry reminders, password reset, and post-accept "here are your steps" mail.
2. **Self-service password reset and a user portal.** #484/D#398 (21 + 14) and #684. Wizarr only has admin-generated reset links ([`src:app/services/password_reset.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/password_reset.py)). Bloom: user-initiated reset by verified email, and a read-only user page (expiry, libraries, permissions).
3. **Pluggable provider model incl. a "generic" provider.** Server requests dominate the top 10 (#934/#1094 Booklore, #757 Calibre-Web, #765 Navidrome, #1240 Shelfmark, #824 Open WebUI, #671 Nextcloud, D#1193/D#1176/D#966/D#740). #671 explicitly asks for "server type of 'Other' without any integration". Bloom: a provider interface (create/delete/disable/list/libraries/reset-password) plus a no-op "instructions-only" provider.
4. **Identity-provider account creation.** #1033 Authentik (21) and shipped LDAP (#652). Bloom: LDAP and OIDC/SCIM-style user provisioning as providers, with group assignment and a sync filter (#1261).
5. **User templates / profiles per invite.** #738, #771, D#1080, #309, #588: "copy settings from" a template user (jfa-go parity), Plex ratings/labels. Bloom: invite → profile (libraries, permissions, duration, template user, labels/ratings).
6. **Referral invites with quotas.** #487/D#1079: existing users create limited invites.
7. **Configurable public surface.** #1208 (landing page with manual code entry), #804 (acceptance-screen text, OAuth app name), #732 (per-invite variables), D#443 (branding), D#515 (external URL for links).
8. **Optional email at signup.** #728 (8 reacts, 8 comments), #108.
9. **Database choice and datetime correctness.** #476 Postgres for Kubernetes; #586 time-reset bug and the "assumes UTC" re-tagging in [`src:app/services/invites.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invites.py). Bloom (Go): store `TIMESTAMPTZ`/RFC 3339 UTC; support SQLite and Postgres from day one.
10. **Library scoping as an explicit, single-source model.** #1103/#546/#1032 and repeated release fixes. Bloom: one join table, an explicit "all libraries" sentinel distinct from "none selected", and a UI that renders saved state.
11. **Ops ergonomics.** Single static binary, non-root by default (#590, #802), migrations that fail closed with rollback guidance (#991), a `/api/version` endpoint (#1172), structured auth audit log to file/stdout for fail2ban (D#704), subpath/base-URL support (D#477), and activity tracking either absent or off by default (#1363).
12. **Inactivity lifecycle.** #621: disable/delete after N days inactive, with exclusions and admin notification.
13. **Companion sync reliability.** #869/#332: ensure Jellyseerr/Overseerr import carries email; verify after create.
14. **Keep the wizard model.** Pre/post steps, bundles, require-interaction, and sandboxed templates are the differentiator and are well-received (no notable complaints found). Preserve import/export compatibility if feasible.

Not grounded in issues (so not claimed here): payments (#509 was closed silently; only 14 reactions, 0 comments).

---

## 6. Security-relevant behaviors

### Worth mirroring
| Behavior | Detail | Source |
|---|---|---|
| Invite code entropy | `secrets.choice` over `A-Z0-9`, 10 chars (36^10 ≈ 3.7e15); admin-supplied codes 6–10 chars; duplicate check | [`src:app/services/invites.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invites.py) |
| Invite validation | Checks existence, `expires <= now`, `used and not unlimited`; length sanity check before DB hit | same; [`src:app/services/invite_code_manager.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invite_code_manager.py) |
| Server-side flow state | Invite code and pre-wizard completion kept in server-side session "to prevent client-side tampering"; pre-invite steps enforced server-side | [`src:app/services/invite_code_manager.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invite_code_manager.py), [docs](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/pre-post-invite-steps.md) |
| Rate limits | `/login` 10/min, `/complete-2fa` 10/min, `/j/<code>` 50/min, `/invitation/process` 20/min, `/join` 20/min, `/reset/<code>` 10/min (flask-limiter) | [`src:app/blueprints/auth/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/auth/routes.py), [`src:app/blueprints/public/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/public/routes.py) |
| Admin password hashing | Werkzeug `generate_password_hash(raw, "scrypt")` | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |
| API keys | `secrets.token_urlsafe(32)`, stored as SHA-256 hex, shown once, `is_active`, `last_used_at` | [`src:app/blueprints/api_keys/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/api_keys/routes.py), [docs/API.md](https://github.com/wizarrrr/wizarr/blob/main/docs/API.md) |
| Password reset tokens | 10-char `A-Z0-9`, 24 h expiry, single-use, prior unused tokens invalidated on issue; new password 8–128 chars | [`src:app/services/password_reset.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/password_reset.py), [`src:app/blueprints/public/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/public/routes.py) |
| Join-form password policy | ≥8 chars, upper + lower + digit; usernames restricted to `^[\w'.-]+$` 3–15 | [`src:app/forms/join.py`](https://github.com/wizarrrr/wizarr/blob/main/app/forms/join.py) |
| Passkeys as admin 2FA | Login with password then WebAuthn if any credential registered | [`src:app/blueprints/auth/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/auth/routes.py) |
| Secrets bootstrap | `SECRET_KEY` = `secrets.token_hex(32)` auto-generated to `/data/database/secrets.json`; LDAP service password Fernet-encrypted with a key in the same file | [`src:app/config.py`](https://github.com/wizarrrr/wizarr/blob/main/app/config.py), [`src:app/services/ldap/encryption.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/ldap/encryption.py) |
| Sessions | Server-side filesystem cache, `mode=0o600`, 24 h timeout | [`src:app/config.py`](https://github.com/wizarrrr/wizarr/blob/main/app/config.py) |
| Wizard template sandbox | Limited variable set, no loops/macros/imports/calls; `nh3` strips scripts, handlers, executable links, non-Discord iframes, style blocks | [docs](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/customise-steps.md), [`src:pyproject.toml`](https://github.com/wizarrrr/wizarr/blob/main/pyproject.toml), [v2026.9.1](https://github.com/wizarrrr/wizarr/releases/tag/v2026.9.1) |
| Destructive-sync guard | "don't delete every local user when Plex returns an empty user list"; guard applied to every backend | [v2026.9.0](https://github.com/wizarrrr/wizarr/releases/tag/v2026.9.0) |
| Expiry as disable-or-delete with audit | `expiry_action` setting; `ExpiredUser` rows; retry on failure "Will retry on next run." | [`src:app/services/expiry.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/expiry.py) |
| Failed-login logging | `AUTH FAIL` with client IP | [`src:app/blueprints/auth/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/auth/routes.py) |

### Worth avoiding or improving
| Behavior | Detail | Source |
|---|---|---|
| Media-user passwords once stored in plaintext | "The passwords of userers created by the application are saved in plain text in the sqlite database"; column removed by migration | [#673](https://github.com/wizarrrr/wizarr/issues/673), [`migrations/versions/20250708_removed_password_plainstring.py`](https://github.com/wizarrrr/wizarr/blob/main/migrations/versions/20250708_removed_password_plainstring.py) |
| Media-server and companion API keys stored unencrypted | `MediaServer.api_key`, `Connection.api_key`, `Notification.password` are plain `String` columns; only the LDAP service password uses Fernet | [`src:app/models.py`](https://github.com/wizarrrr/wizarr/blob/main/app/models.py) |
| `DISABLE_BUILTIN_AUTH=true` logs any visitor to `/login` in as admin | `login_user(AdminUser(), ...)` with no header check; security depends entirely on the proxy and on whitelisting only public paths | [`src:app/blueprints/auth/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/auth/routes.py), [docs: SSO](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/single-sign-on-sso.md) |
| Client IP from `CF-Connecting-IP` / `X-Forwarded-For` without trusted-proxy config | Header trusted unconditionally for logging | [`src:app/blueprints/auth/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/auth/routes.py) |
| `/j/<code>/password` has no rate-limit decorator | Other public routes do | [`src:app/blueprints/public/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/public/routes.py) |
| Global CSRF protection not confirmed | `flask-wtf` `FlaskForm` used for `JoinForm`; no `CSRFProtect` found in `app/__init__.py` or `app/extensions.py` | [`src:app/forms/join.py`](https://github.com/wizarrrr/wizarr/blob/main/app/forms/join.py), [`src:app/extensions.py`](https://github.com/wizarrrr/wizarr/blob/main/app/extensions.py) |
| Recovery CLI grants admin to anyone with container access | "Anyone with server access can use this tool to gain admin privileges" | [docs: recovery](https://github.com/wizarrrr/wizarr/blob/main/docs/using-wizarr/password-passkey-recovery.md) |
| No TOTP; passkey-only 2FA | Users asked for MFA (D#669) and fail2ban logs (D#704) | [D#669](https://github.com/orgs/wizarrrr/discussions/669), [D#704](https://github.com/orgs/wizarrrr/discussions/704) |
| Legacy admin credentials in `settings` rows | Three auth code paths in one handler | [`src:app/blueprints/auth/routes.py`](https://github.com/wizarrrr/wizarr/blob/main/app/blueprints/auth/routes.py) |
| Plex sign-in prompt exposes host IP | "it also shows the IP address which the instance is hosted on" (2023, closed) | [#68](https://github.com/wizarrrr/wizarr/issues/68) |
| Session storage on filesystem | Corrupt 0-byte session files had to be handled ("remove corrupt 0-byte session cache files instead of logging forever") | [v2026.3.0](https://github.com/wizarrrr/wizarr/releases/tag/v2026.3.0) |
| Naive datetimes | "Make database datetime timezone-aware (assumes UTC)" at every comparison | [`src:app/services/invites.py`](https://github.com/wizarrrr/wizarr/blob/main/app/services/invites.py), [#586](https://github.com/wizarrrr/wizarr/issues/586) |
