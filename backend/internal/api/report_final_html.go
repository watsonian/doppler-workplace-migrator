package api

import (
	"html/template"
	"io"
	"strings"
	"time"
)

// finalReportHTML renders the report as one self-contained page: no scripts,
// no external fonts or styles, so it opens the same from a laptop, an email
// attachment or a ticket years later, and prints cleanly.
var finalReportHTML = template.Must(template.New("report").Funcs(template.FuncMap{
	"when": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.UTC().Format("Jan 2, 2006 15:04 UTC")
	},
	"whenp": func(t *time.Time) string {
		if t == nil || t.IsZero() {
			return "—"
		}
		return t.UTC().Format("Jan 2, 2006 15:04 UTC")
	},
	"mode": func(m string) string {
		if m == "partial" {
			return "Partial"
		}
		return "Full"
	},
	"join":   strings.Join,
	"plural": pluralize,
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Migration Report{{if .SourceWorkplace}} — {{.SourceWorkplace}}{{end}}</title>
<style>
  :root {
    --ink: #131416; --ink-2: #4A4E5A; --ink-3: #6B7280; --line: #E9EBED; --ground: #FAFAFA; --card: #FFFFFF;
    --purple: #6B13F5; --purple-bg: #F9F5FF;
    --good: #0E9455; --good-bg: #EBFBF3; --warn: #C48310; --warn-bg: #FEF8EB; --bad: #C81A10; --bad-bg: #FEF1F0;
    --mono: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--ground); color: var(--ink);
    font: 14px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, Roboto, sans-serif; }
  main { max-width: 880px; margin: 0 auto; padding: 40px 24px 64px; }
  header { border-bottom: 1px solid var(--line); padding-bottom: 20px; margin-bottom: 8px; }
  .eyebrow { font-size: 11px; font-weight: 600; letter-spacing: .06em; text-transform: uppercase; color: var(--purple); }
  h1 { font-size: 26px; line-height: 1.2; margin: 6px 0 4px; text-wrap: balance; }
  h2 { font-size: 16px; margin: 0 0 10px; }
  .meta { color: var(--ink-3); font-size: 13px; }
  .meta b { color: var(--ink-2); font-weight: 600; }
  .verdict { display: inline-flex; align-items: center; gap: 8px; margin-top: 14px; padding: 6px 12px;
    border-radius: 6px; font-weight: 600; font-size: 14px; }
  .good { color: var(--good); background: var(--good-bg); }
  .warn { color: var(--warn); background: var(--warn-bg); }
  .bad  { color: var(--bad);  background: var(--bad-bg); }
  section { margin-top: 28px; }
  .card { background: var(--card); border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
  .wrap { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th { text-align: left; font-size: 11px; font-weight: 600; letter-spacing: .05em; text-transform: uppercase;
    color: var(--ink-3); background: var(--ground); padding: 8px 12px; border-bottom: 1px solid var(--line); }
  td { padding: 8px 12px; border-bottom: 1px solid var(--line); vertical-align: top; }
  tr:last-child td { border-bottom: 0; }
  td.n, th.n { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .mono { font-family: var(--mono); font-size: 12.5px; }
  .muted { color: var(--ink-3); }
  .tiles { display: grid; grid-template-columns: repeat(4, 1fr); gap: 1px;
    background: var(--line); border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
  .tile { background: var(--card); padding: 12px 14px; }
  .tile b { display: block; font-size: 20px; font-variant-numeric: tabular-nums; }
  .tile span { font-size: 12px; color: var(--ink-3); }
  .pill { display: inline-block; padding: 1px 8px; border-radius: 6px; font-size: 12px; font-weight: 500; white-space: nowrap; }
  .note { padding: 10px 14px; border-radius: 8px; font-size: 13px; margin-top: 8px; }
  details { border-top: 1px solid var(--line); }
  details > summary { cursor: pointer; padding: 8px 12px; font-size: 13px; color: var(--ink-2); list-style-position: inside; }
  ul.plain { margin: 0; padding: 0 12px 10px 28px; font-size: 13px; }
  .empty { padding: 12px 14px; color: var(--ink-3); font-size: 13px; }
  footer { margin-top: 40px; color: var(--ink-3); font-size: 12px; border-top: 1px solid var(--line); padding-top: 14px; }
  @media (max-width: 560px) { .tiles { grid-template-columns: repeat(2, 1fr); } }
  @media print {
    body { background: #fff; }
    main { padding: 0; max-width: none; }
    .card, .tiles { break-inside: avoid; }
    details > summary { display: none; }
    details > * { display: block !important; }
  }
</style>
</head>
<body>
<main>
<header>
  <div class="eyebrow">Doppler migration report</div>
  <h1>{{if .SourceWorkplace}}{{.SourceWorkplace}}{{else}}Workplace migration{{end}}</h1>
  <div class="meta">
    <b>Source</b> <span class="mono">{{.SourceHost}}</span> &nbsp;→&nbsp;
    <b>Target</b> <span class="mono">{{.TargetHost}}</span><br>
    Generated {{when .GeneratedAt}}{{if .DevMode}} · <b>Dev mode</b>: nothing was written to the target{{end}}
  </div>
  <div class="verdict {{.StatusTone}}">{{.Status}}</div>
</header>

<section>
  <h2>What the source holds</h2>
  <div class="tiles">
    <div class="tile"><b>{{.Source.ProjectCount}}</b><span>Projects</span></div>
    <div class="tile"><b>{{.Source.EnvironmentCount}}</b><span>Environments</span></div>
    <div class="tile"><b>{{.Source.ConfigCount}}</b><span>Configs</span></div>
    <div class="tile"><b>{{.Source.SecretCount}}</b><span>Secrets</span></div>
    <div class="tile"><b>{{.Source.GroupCount}}</b><span>Groups</span></div>
    <div class="tile"><b>{{.Source.ServiceAccountCount}}</b><span>Service accounts</span></div>
    <div class="tile"><b>{{.Source.RoleCount}}</b><span>Custom roles</span></div>
    <div class="tile"><b>{{.Source.WebhookCount}}</b><span>Webhooks</span></div>
  </div>
</section>

<section>
  <h2>Runs</h2>
  <div class="card">
  {{if .Runs}}
    <div class="wrap"><table>
      <tr><th>When</th><th>Run</th><th class="n">Projects</th><th class="n">Created</th><th class="n">Updated</th><th class="n">Failed</th></tr>
      {{range .Runs}}
      <tr><td>{{when .At}}</td><td>{{if eq .Kind "top_up"}}Top-up{{else}}{{mode .Mode}}{{end}}</td><td class="n">{{len .Projects}}</td>
        <td class="n">{{.Created}}</td><td class="n">{{.Updated}}</td><td class="n">{{if .Failed}}<span class="pill bad">{{.Failed}}</span>{{else}}0{{end}}</td></tr>
      {{end}}
    </table></div>
    {{range .Runs}}{{if eq .Mode "partial"}}
    <details><summary>Projects in the partial migration of {{when .At}}</summary>
      <ul class="plain mono">{{range .Projects}}<li>{{.}}</li>{{end}}</ul>
    </details>
    {{end}}{{end}}
  {{else}}<div class="empty">No run has finished.</div>{{end}}
  </div>
</section>

<section>
  <h2>Phases of the last run</h2>
  <div class="card">
  {{if .Phases}}
    <div class="wrap"><table>
      <tr><th>Phase</th><th class="n">Done</th><th class="n">Skipped</th><th class="n">Failed</th></tr>
      {{range .Phases}}
      <tr><td>{{.Label}}</td><td class="n">{{.Completed}}</td><td class="n muted">{{.Skipped}}</td>
        <td class="n">{{if .Failed}}<span class="pill bad">{{.Failed}}</span>{{else}}0{{end}}</td></tr>
      {{end}}
    </table></div>
  {{else}}<div class="empty">No phase has run.</div>{{end}}
  </div>
  {{with .TopUp}}
  <div class="note good" style="background:var(--purple-bg);color:var(--ink-2)">
    Projects from an earlier partial migration were brought up to date:
    {{.Created}} created, {{.Updated}} updated, {{.Skipped}} skipped{{if .Failed}}, <b>{{.Failed}} failed</b>{{end}}.
  </div>
  {{end}}
</section>

<section>
  <h2>Service tokens</h2>
  <div class="card">
  {{if .Tokens.Records}}
    <div class="tiles" style="border:0;border-radius:0;border-bottom:1px solid var(--line)">
      <div class="tile"><b>{{.Tokens.Counts.Issued}}</b><span>Issued on the target</span></div>
      <div class="tile"><b>{{.Tokens.Counts.ToRecreate}}</b><span>Skipped, to recreate</span></div>
      <div class="tile"><b>{{.Tokens.Counts.NotNeeded}}</b><span>Not needed</span></div>
      <div class="tile"><b>{{.Tokens.Counts.Pending}}</b><span>Undecided{{if .Tokens.Counts.Deferred}} · {{.Tokens.Counts.Deferred}} set aside{{end}}</span></div>
    </div>
    <details open><summary>{{plural (len .Tokens.Records) "token" "tokens"}}</summary>
    <div class="wrap"><table>
      <tr><th>Token</th><th>Placement</th><th>Access</th><th>Outcome</th><th>Issued</th></tr>
      {{range .Tokens.Records}}
      <tr><td class="mono">{{.Name}}</td><td class="mono">{{.Placement}}</td><td class="muted">{{if .Access}}{{.Access}}{{else}}—{{end}}</td>
        <td>{{.Disposition}}{{if .Note}}<div class="muted" style="font-size:12px">{{.Note}}</div>{{end}}</td>
        <td class="muted">{{whenp .IssuedAt}}</td></tr>
      {{end}}
    </table></div>
    </details>
  {{else}}<div class="empty">The source has no service tokens or service account tokens.</div>{{end}}
  </div>
</section>

<section>
  <h2>Cutover</h2>
  <div class="card"><div class="empty" style="color:var(--ink)">
  {{if eq .Cutover.State "ran"}}{{with .Cutover.Result}}
    Ran {{when .FinishedAt}}: {{plural .ConfigsMirrored "config" "configs"}} mirrored, {{.RangesAdded}} ranges added, {{.RangesRemoved}} removed{{if .Failed}}, <b>{{.Failed}} failed</b>{{end}}.
    {{if .SelfTrustOutstanding}}<div class="note bad">The migrator's address <span class="mono">{{.MigratorCIDR}}</span> is still trusted by: <span class="mono">{{join .SelfTrustOutstanding ", "}}</span>. Remove it by hand.</div>{{end}}
  {{end}}
  {{else if eq .Cutover.State "not_needed"}}Not needed: every config's trusted IP ranges on the target already match the source.
  {{else if eq .Cutover.State "done"}}Finished {{whenp .Cutover.At}}.
  {{else if eq .Cutover.State "ready_to_finish"}}Nothing to apply: the target's trusted IP ranges already match the source. Finish the migration from the Cutover page to close it.
  {{else if eq .Cutover.State "waiting"}}Not yet: the source is still in use, and the target is being topped up until cutover.
  {{else if eq .Cutover.State "blocked"}}Not available: only a partial migration has run.
  {{else}}Not run yet.{{end}}
  </div></div>
</section>

<section>
  <h2>Errors</h2>
  <div class="card">
  {{if .Errors.Classes}}
    <div class="wrap"><table>
      <tr><th>Cause</th><th>Phase</th><th class="n">Count</th><th class="n">Resolved</th><th class="n">Open</th></tr>
      {{range .Errors.Classes}}
      <tr><td>{{.Title}}</td><td class="muted">{{.Phase}}</td><td class="n">{{.Count}}</td><td class="n muted">{{.Resolved}}</td>
        <td class="n">{{if .Open}}<span class="pill bad">{{.Open}}</span>{{else}}0{{end}}</td></tr>
      {{end}}
    </table></div>
    {{if .Errors.OpenItems}}
    <details open><summary>{{plural (len .Errors.OpenItems) "open error" "open errors"}}</summary>
    <div class="wrap"><table>
      <tr><th>Item</th><th>Cause</th><th>Message</th></tr>
      {{range .Errors.OpenItems}}
      <tr><td class="mono">{{.Item}}</td><td class="muted">{{.Class}}</td><td>{{.Message}}</td></tr>
      {{end}}
    </table></div>
    </details>
    {{end}}
  {{else}}<div class="empty">No errors were recorded.</div>{{end}}
  </div>
</section>

{{if or .ManualSteps .UsersMissing .Notes}}
<section>
  <h2>Still to do by hand</h2>
  <div class="card">
    {{range .ManualSteps}}
    <details open><summary><b>{{.Title}}</b> · {{len .Items}}</summary>
      <ul class="plain">{{range .Items}}<li><span class="mono">{{.Name}}</span>{{if .Detail}} <span class="muted">— {{.Detail}}</span>{{end}}</li>{{end}}</ul>
    </details>
    {{end}}
    {{if .UsersMissing}}
    <details open><summary><b>Users not on the target</b> · {{len .UsersMissing}} — their access was skipped; provision them and run again</summary>
      <ul class="plain mono">{{range .UsersMissing}}<li>{{.}}</li>{{end}}</ul>
    </details>
    {{end}}
    {{if .Notes}}
    <details open><summary><b>Not migrated</b></summary>
      <ul class="plain">{{range .Notes}}<li>{{.}}</li>{{end}}</ul>
    </details>
    {{end}}
  </div>
</section>
{{end}}

<footer>
  This report lists names, counts and outcomes only. It contains no secret values and no token values.
</footer>
</main>
</body>
</html>
`))

func renderFinalReport(w io.Writer, r finalReport) error {
	return finalReportHTML.Execute(w, r)
}
