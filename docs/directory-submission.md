# Directory submissions

How the hosted instance, `https://mcp.google-contacts-mcp.samber.dev/mcp`, gets listed in two directories:

- Claude: Connectors Directory, submitted at [claude.ai/directory/manage](https://claude.ai/directory/manage) as an **MCP connector**.
- ChatGPT and Codex: Plugins Directory, submitted at [platform.openai.com/plugins](https://platform.openai.com/plugins) as a ZIP package.

Both submissions happen in the vendors' portals. This repository holds what the portals ask for.

## What the repository already provides

| Requirement                                        | Where                                                                     |
| -------------------------------------------------- | ------------------------------------------------------------------------- |
| Remote HTTPS Streamable HTTP endpoint              | `POST /mcp`                                                               |
| OAuth 2.1 with PKCE, DCR and CIMD                  | `internal/oauth`                                                          |
| `title` on every tool                              | `internal/tools/handlers.go`                                              |
| `readOnlyHint`, `destructiveHint`, `openWorldHint` | `internal/tools/handlers.go`, pinned by `TestToolAnnotations`             |
| Privacy policy, terms, support contact             | `site/src/pages/privacy.astro`, `terms.astro`, `OPERATOR_EMAIL`           |
| OpenAI domain verification                         | `--openai-apps-challenge`, served at `/.well-known/openai-apps-challenge` |
| OpenAI plugin package                              | `plugin/openai/`, built by `make plugin-openai`                           |

## Blockers before either review can pass

1. **Google OAuth app must be verified.** In Testing mode only 100 listed test users can connect, and their grant expires after 7 days. See [self-hosting](self-hosting.md#google-cloud-setup). Reviewers and the public hit this wall first.
2. **Reviewer account.** Both vendors need a Google account that holds sample contacts and needs no MFA prompt, SMS code or private network. Create a dedicated one. Add it to the test users until the app is verified. Enter its credentials only in the portals, never in this repository or the ZIP.
3. **Demo video.** OpenAI asks for a recording URL that shows the test cases below.
4. **Identity.** OpenAI requires a verified individual or business identity under the exact name it publishes. The package says `Samuel Berthe`.

## Claude connector

Portal steps and the answers this project gives:

| Step       | Answer                                                                                            |
| ---------- | ------------------------------------------------------------------------------------------------- |
| Connection | `https://mcp.google-contacts-mcp.samber.dev/mcp`, one URL for every user.                         |
| Tools      | Synced from the server. All ten tools carry a title and hints.                                    |
| Listing    | Text below. Documentation URL: `https://google-contacts-mcp.samber.dev`. Privacy: `/privacy`.     |
| Use cases  | Reads and writes. Users need a Google account with Google Contacts.                               |
| Company    | Samuel Berthe, `https://github.com/samber`, contact `hello@samuel-berthe.fr`.                     |
| Auth       | Required. OAuth 2.0, server speaks CIMD and DCR, so no client id is entered in the portal.        |
| Data       | First-party: the server calls Google's People API for the signed-in user. No health data. No ads. |
| Test       | Run every tool in MCP Inspector and as a custom connector in Claude, then tick the box.           |

Listing text (limits: name 100, one-liner 200, description 2,000 characters):

- Name: `Google Contacts`
- One-liner: `Search, add, update and organize your Google Contacts from Claude.`
- Description: `Ask Claude to find a phone number or e-mail, add a contact, fix a job title, merge new details into an existing contact by e-mail, or sort contacts into groups. Sign in with your Google account and grant access to Google Contacts only. Contacts are read and written live through the Google People API and are never stored on the server. Tools that change your address book say so, and deleting a contact is flagged as destructive so Claude asks first.`

Claude's OAuth callback is `https://claude.ai/api/mcp/auth_callback`. The server accepts any `https` redirect URI from a registered or CIMD client, so nothing to allow-list.

## OpenAI plugin

1. Deploy with `GCMCP_OPENAI_APPS_CHALLENGE` unset. In the portal, create the plugin and copy its domain-verification token.
2. Set `GCMCP_OPENAI_APPS_CHALLENGE=<token>`, redeploy, and check the body is exactly the token:

   ```sh
   curl -s https://mcp.google-contacts-mcp.samber.dev/.well-known/openai-apps-challenge
   ```

3. Build the package and upload it:

   ```sh
   make plugin-openai   # writes dist/google-contacts-openai-plugin.zip
   ```

4. Fix validation findings, fill the review details, submit.

Both listings are named `Google Contacts`. Either review may flag that name as generic or as implying affiliation with Google; rename it in `plugin.json` (bump `version`) and in the Claude portal if so.

Package rules worth remembering: one remote MCP server, no `headers`, no credentials in the ZIP, no `.app.json`. The MCP URL cannot change through the update flow, so the host stays `mcp.google-contacts-mcp.samber.dev`. A change to the package needs a new `version` in `plugin/openai/plugin.json`.

`publication.countries` is `[]`, which OpenAI documents as "no country restriction". Omitting the field does not mean the same: it keeps existing targeting. List uppercase country codes in `plugin.json` to restrict availability. Projects with EU data residency cannot submit MCP plugins today.

### Test cases

Five that should work:

| Prompt                                                    | Tools                                                                     | Expected behavior                                  |
| --------------------------------------------------------- | ------------------------------------------------------------------------- | -------------------------------------------------- |
| Find the e-mail address of Alex Martin.                   | `search_contacts`                                                         | Returns the matching contact's e-mail.             |
| Add a contact: Jane Doe, jane@example.com, works at Acme. | `search_contacts`, `create_contact`                                       | Checks for a duplicate, then creates the contact.  |
| Change Jane Doe's job title to CTO.                       | `search_contacts`, `update_contact`                                       | Updates only the job title.                        |
| Create a group called Conference and add Jane Doe to it.  | `create_contact_group`, `search_contacts`, `modify_contact_group_members` | Creates the group, then adds the contact.          |
| List my contact groups.                                   | `list_contact_groups`                                                     | Returns user and system groups with member counts. |

Three that should not trigger the plugin:

| Prompt                                    | Why                                                    |
| ----------------------------------------- | ------------------------------------------------------ |
| Send an e-mail to Jane Doe.               | The plugin cannot send mail.                           |
| What is on my calendar tomorrow?          | No calendar access.                                    |
| Find the phone number of Acme on the web. | The plugin reads the user's own contacts, not the web. |
