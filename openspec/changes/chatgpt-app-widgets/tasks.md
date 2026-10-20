## 1. Resource

- [ ] 1.1 Embed `internal/api/mcpapp/widget/jobs.html`; derive the content-hashed URI once.
      Test that the URI changes when the bytes do.
- [ ] 1.2 Register the resource on the anonymous server with its MIME type and CSP `_meta`.
      Test over the in-memory SDK transport: `resources/list` names it, `resources/read`
      returns the page with `text/html;profile=mcp-app` and the CSP block.
- [ ] 1.3 Add `_meta.ui.resourceUri` to `search_jobs` and `get_job` only. Test that every
      `resourceUri` a listed tool carries is a listed resource, and that only those two
      carry one.

## 2. Page

- [ ] 2.1 Bridge: `ui/initialize` → `ui/notifications/initialized`; read
      `ui/notifications/tool-result`'s `structuredContent`; report height with
      `ui/notifications/size-changed`; apply `hostContext.theme` and style variables.
- [ ] 2.2 List view: the job card and the ignored-params notice.
- [ ] 2.3 Detail view: header, liveness, requirements, ATS, collapsible description.
- [ ] 2.4 Links via `ui/open-link`, falling back to `<a target="_blank">`; `https:` only.
- [ ] 2.5 Logo with monogram fallback.

## 3. Guards

- [ ] 3.1 Field guard: every `job.<field>` / `result.<field>` the page reads is a JSON field
      of a `wire.go` type. Prove it by renaming a field and watching it fail.
- [ ] 3.2 Safety guard: the page source contains no `innerHTML`, `outerHTML`,
      `insertAdjacentHTML`, `document.write`, `fetch(` or `XMLHttpRequest`.

## 4. Verify and ship

- [ ] 4.1 Render both views in the MCP Apps reference host locally against a real server;
      screenshot light and dark.
- [ ] 4.2 Update `internal/api/mcpapp/AGENTS.md`.
- [ ] 4.3 PR → CI → merge → deploy; probe `resources/read` on production.
- [ ] 4.4 Owner: check in ChatGPT developer mode, then resubmit the app for review.
