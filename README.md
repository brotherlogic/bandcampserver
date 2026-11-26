# bandcampserver

`bandcampserver` is a Go gRPC service that syncs Bandcamp collection purchases, tracks Bandcamp-to-Discogs release mappings, files GitHub issues for unmapped releases, and resolves direct WAV download links using an authenticated Bandcamp session.

## CLI (`bandcampserver_cli`)

Build or run the CLI from the repository root:

```bash
go build ./bandcampserver_cli
```

### Subcommands

- **`token`**: Set the Bandcamp collection API token.
  ```bash
  bandcampserver_cli token -token '<token>'
  ```
- **`cookie`**: Supply or rotate the Bandcamp `identity` session cookie via the `SetCookie` RPC.
  ```bash
  bandcampserver_cli cookie -cookie '<value>'
  ```
- **`mapping`**: Map a Bandcamp album ID to a Discogs release ID.
  ```bash
  bandcampserver_cli mapping -bandcamp <bandcamp_id> -discogs <discogs_id>
  ```
- **`lookup`**: Look up stored Bandcamp item metadata by Bandcamp album ID.
  ```bash
  bandcampserver_cli lookup -id <bandcamp_id>
  ```
- **`reset`**: Clear stored Bandcamp server state via `Reset`.
  ```bash
  bandcampserver_cli reset
  ```
- **`ping`**: Trigger `ClientUpdate` for a `recordcollection` instance ID.
  ```bash
  bandcampserver_cli ping <instance_id>
  ```

## Retrieving the Bandcamp `identity` Cookie in Chrome

1. Open `https://bandcamp.com` in Chrome while signed in to your Bandcamp account.
2. Open Chrome DevTools (`F12`, or `Cmd+Opt+I` on macOS / `Ctrl+Shift+I` on Linux/Windows) -> **Application** tab -> **Storage** -> **Cookies** -> `https://bandcamp.com`.
3. Locate the cookie named `identity` and copy its **Value** (URL-encoded value; treat as a secret and never share or commit it).
4. Run `bandcampserver_cli cookie -cookie '<value>'` (quote the value to prevent shell expansion of special characters).
