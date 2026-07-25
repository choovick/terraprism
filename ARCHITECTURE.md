# Terraprism Architecture

This document describes how Terraprism is put together: its external
context, its internal packages and how they depend on each other, the
data model that flows between them, and the rendering pipeline that turns
a decoded plan into the interactive TUI.

For user-facing usage, see [README.md](README.md). For contributor
conventions, see [CLAUDE.md](CLAUDE.md).

## System Context

Terraprism is a CLI/TUI wrapper around `terraform`/`tofu`. It never talks
to cloud providers or Terraform state directly — it shells out to the
real `terraform`/`tofu` binary for everything infrastructure-related, and
its own job is limited to decoding that binary's JSON plan output into an
interactive view.

```mermaid
flowchart LR
    User(["User"])
    TF[("terraform / tofu CLI")]
    GH[("GitHub Releases")]
    FS[("~/.terraprism/*.json<br/>history files")]

    User -- "plan / apply / history / -p" --> Terraprism["terraprism"]
    Terraprism -- "plan → show -json → apply" --> TF
    Terraprism -- "version check / download" --> GH
    Terraprism -- "read / write" --> FS
    Terraprism -- "interactive TUI" --> User
```

## Package Architecture

Each internal package has one job. Dependencies only point one direction
— `internal/tfplan` is a leaf package with no dependencies on anything
else in this repo, which keeps the decode logic testable and reusable
independent of the TUI or the runner.

```mermaid
flowchart TD
    main["cmd/terraprism<br/><i>main.go — CLI entry point, command dispatch</i>"]

    runner["internal/runner<br/><i>shells out to terraform/tofu:<br/>plan → show -json → apply</i>"]
    tfplan["internal/tfplan<br/><i>decodes show -json into a<br/>pre-diffed Attribute tree</i>"]
    history["internal/history<br/><i>JSON envelope files<br/>in ~/.terraprism/</i>"]
    tui["internal/tui<br/><i>Bubble Tea interactive TUI +<br/>non-interactive print mode</i>"]
    updater["internal/updater<br/><i>GitHub release<br/>self-update</i>"]

    main --> runner
    main --> tfplan
    main --> history
    main --> tui
    main --> updater

    runner --> tfplan
    tui --> tfplan
    tui --> history
    tui --> updater
```

| Package | Responsibility | Depends on |
|---|---|---|
| `cmd/terraprism` | Parses CLI args, dispatches to one of the run modes below, owns the "no changes" early exits | `history`, `runner`, `tfplan`, `tui`, `updater` |
| `internal/runner` | The **only** place that invokes the real `terraform`/`tofu` binary for plan/show/apply | `tfplan` |
| `internal/tfplan` | Decodes `terraform show -json` via `hashicorp/terraform-json`, builds the pre-diffed `Attribute` tree, exposes plan-wide summary counts | *(none — leaf package)* |
| `internal/history` | Persists/lists/renames plan & apply runs as JSON envelope files | *(none)* |
| `internal/tui` | Renders a `*tfplan.Plan` — either interactively (Bubble Tea `Model`) or flat (`PrintPlan`) | `tfplan`, `history` (picker only), `updater` (update nudge) |
| `internal/updater` | Checks GitHub Releases for newer versions and self-updates the binary | *(none)* |

## Command Dispatch

`main()` routes `os.Args` to one of several run modes. `plan`/`apply`/
`destroy` invoke the runner; bare/file input goes through the JSON-sniffing
view mode; anything not recognized as a Terraprism subcommand passes
straight through to `terraform`/`tofu`.

```mermaid
flowchart TD
    Start(["terraprism [args]"]) --> NoArgs{"no args?"}
    NoArgs -- yes --> ViewStdin["runViewMode(nil)<br/>read stdin"]
    NoArgs -- no --> Flag{"-h/--help or<br/>-v/--version?"}
    Flag -- yes --> Usage["printUsage /<br/>runVersionMode"]
    Flag -- no --> StateCmd{"state list/show/rm?"}
    StateCmd -- yes --> StateMode["runStateMode"]
    StateCmd -- no --> Passthrough{"init/validate/fmt/<br/>output/import/...?"}
    Passthrough -- yes --> PassMode["runPassthroughMode<br/>(exec straight through)"]
    Passthrough -- no --> Known{"plan / apply / destroy /<br/>history / version / upgrade?"}
    Known -- apply / destroy --> ApplyMode["runApplyMode"]
    Known -- plan --> PlanMode["runPlanMode"]
    Known -- history --> HistMode["runHistoryMode"]
    Known -- version --> VerMode["runVersionMode"]
    Known -- upgrade --> UpgMode["runUpgradeMode"]
    Known -- no match --> ViewFile["runViewMode(args)<br/>read file arg"]
```

## Data Flow

### `terraprism plan` / `terraprism apply`

The runner always writes a binary plan file first (`show -json` requires
one — there's no way to get JSON plan output without it), then decodes
its JSON representation. The raw JSON bytes (not the parsed Go struct)
are what get persisted to history, so history stays re-decodable by any
future version of `tfplan.Decode`.

```mermaid
sequenceDiagram
    actor User
    participant Main as cmd/terraprism
    participant Runner as internal/runner
    participant TF as terraform/tofu CLI
    participant TFPlan as internal/tfplan
    participant Hist as internal/history
    participant TUI as internal/tui

    User->>Main: terraprism plan
    Main->>Runner: RunPlan(Options)
    Runner->>TF: plan -out=tmp.tfplan -no-color
    TF-->>Runner: exit 0
    Runner->>TF: show -json tmp.tfplan
    TF-->>Runner: JSON bytes
    Runner->>TFPlan: DecodeBytes(json)
    TFPlan-->>Runner: *tfplan.Plan
    Runner-->>Main: PlanResult{Plan, RawJSON, PlanFile}
    Main->>Hist: CreateHistoryFile(meta, RawJSON)
    Main->>TUI: NewModel(plan) / NewModelWithApply(...)
    TUI-->>User: interactive diff view
```

Confirming an apply inside the TUI hands control back to `main.go`, which
runs the apply against the plan file the runner already produced:

```mermaid
sequenceDiagram
    actor User
    participant TUI as internal/tui
    participant Main as cmd/terraprism
    participant Runner as internal/runner
    participant TF as terraform/tofu CLI
    participant Hist as internal/history

    User->>TUI: 'a' then 'y' (confirm apply)
    TUI-->>Main: ShouldApply() == true
    Main->>Runner: Apply(ctx, cmd, planFile)
    Runner->>TF: apply <planFile>
    TF-->>User: streamed apply output (stdout/stderr passthrough)
    Main->>Hist: UpdateFilenameWithStatus(success/failed)
```

### Piped / file input (view mode)

Terraprism only accepts real `terraform show -json` output here — it
sniffs the first non-whitespace byte and fails with a corrective message
rather than trying to interpret plain plan text or a binary `-out=` file.

```mermaid
sequenceDiagram
    actor User
    participant Shell
    participant Main as cmd/terraprism
    participant TFPlan as internal/tfplan
    participant TUI as internal/tui

    User->>Shell: terraform show -json plan.bin | terraprism
    Shell->>Main: stdin bytes
    Main->>Main: looksLikeJSON(raw)?
    alt not JSON
        Main-->>User: "run terraform show -json ..." error
    else is JSON
        Main->>TFPlan: DecodeBytes(raw)
        TFPlan-->>Main: *tfplan.Plan
        Main->>TUI: NewModel(plan) or PrintPlan(plan)
        TUI-->>User: rendered plan
    end
```

## Data Model

`tfplan.Decode` walks Terraform's JSON `before`/`after`/`after_unknown`/
`before_sensitive`/`after_sensitive` trees **once**, in parallel, and
produces a single pre-diffed `Attribute` tree per resource. This is the
core design choice of the whole rewrite: every downstream consumer
(interactive TUI, print mode) reads this tree directly and never
re-parses or re-diffs anything.

```mermaid
classDiagram
    class Plan {
        +string FormatVersion
        +string TerraformVersion
        +[]Resource Resources
        +[]OutputChange OutputChanges
        +int TotalAdd
        +int TotalChange
        +int TotalDestroy
        +int OutputCount
        +[]byte RawJSON
        +DisplayResources() []Resource
    }

    class Resource {
        +string Address
        +string Type
        +string Name
        +Action Action
        +ReplacePattern ReplacePattern
        +[]string ActionReasons
        +bool IsDataSource
        +[]Attribute Attributes
        +ChangedAttributeCount() int
    }

    class OutputChange {
        +string Name
    }

    class Attribute {
        +string Name
        +string Path
        +ValueKind Kind
        +Action Action
        +any Old
        +any New
        +[]Attribute Children
        +bool Computed
        +bool Sensitive
    }

    Plan "1" *-- "many" Resource : Resources
    Plan "1" *-- "many" OutputChange : OutputChanges
    Resource "1" *-- "many" Attribute : Attributes
    Attribute "1" *-- "many" Attribute : Children (recursive)
    OutputChange --|> Attribute : embeds
```

Notable properties of this model:

- **Attribute is recursive.** A leaf (`Kind` = string/number/bool/null)
  carries `Old`/`New`; a container (`Kind` = map/list) carries `Children`
  instead — each already diffed the same way. There is no separate
  "raw text" representation anywhere.
- **Existence, not nil-ness, drives `Action`.** A JSON `null` value and a
  genuinely absent key both decode to Go `nil`, so `Action` is derived
  from explicit `beforeExists`/`afterExists` booleans tracked through the
  recursive walk — otherwise an attribute explicitly set to `null` on a
  newly-created resource would be misclassified as unchanged instead of
  created.
- **Sensitivity and unknown-ness are per-node, not per-attribute.**
  Terraform's `before_sensitive`/`after_sensitive`/`after_unknown` are
  themselves value-shaped trees of booleans (e.g. `{"tags":{"Name":true}}`
  marks only `tags.Name` sensitive, not the whole map), so `Sensitive`/
  `Computed` are resolved at the same path-by-path granularity.
- **Outputs become synthetic resources for display.** `OutputChange`
  is a real, separate concept in the data model, but `Plan.DisplayResources()`
  flattens it into the same `[]Resource` shape (labeled `ActionOutput`)
  that the TUI already knows how to render, navigate, filter, and sort —
  so the rendering pipeline needs no separate code path for outputs.

## Rendering Pipeline

`internal/tui` walks the `Attribute` tree recursively
(`renderAttributeTree` for the interactive TUI, `printAttributeTree` for
flat print mode — both follow the same logic). Because Terraform's JSON
plan always carries the resource's *full* before/after state, most
attributes of a partially-changed resource are unchanged context rather
than part of the actual diff; those collapse into a single muted
`# (N unchanged attributes hidden)` note (matching Terraform CLI's own
convention) so the real change stays visible.

```mermaid
flowchart TD
    A["renderAttributeTree(attrs)"] --> B{"run of consecutive<br/>ActionNoOp attributes?"}
    B -- yes --> C["emit one '# (N unchanged<br/>attributes hidden)' line"]
    B -- no, next attr --> D{"name is<br/>user_data / user_data_base64?"}
    D -- yes --> E["decode (base64/gzip/hex)<br/>+ line diff"]
    D -- no --> F{"container?<br/>(Map/List with Children)"}
    F -- yes --> G["render fold header<br/>(+/-/~, collapsed by size/override)"]
    G -- expanded --> A
    F -- no --> H{"Sensitive?"}
    H -- yes --> I["render '(sensitive value)'"]
    H -- no --> J{"multi-line string?<br/>(Old or New contains \n)"}
    J -- yes --> K["ComputeDiff + ContextDiff<br/>line-by-line, no marker parsing needed"]
    J -- no --> L["render 'name = value',<br/>old → new arrow if updated"]
```

Fold/collapse state is keyed by `address + "#" + attribute.Path` (e.g.
`aws_instance.web#tags`) rather than by line offset, so it stays stable
across re-renders regardless of terminal width, diff-context setting, or
search/sort/filter state — the previous text-based renderer's fold keys
were line-offset-based and could drift.

## Key Design Decisions

1. **One JSON decode boundary, no regex.** `internal/tfplan` is the only
   place that interprets Terraform's plan format; nothing downstream
   re-parses text.
2. **The runner is the only thing that shells out.** `internal/runner`
   isolates every `terraform`/`tofu` invocation, making the plan→decode
   pipeline mockable in tests without a real Terraform binary.
3. **History persists raw JSON, not a parsed struct.** This keeps history
   files re-decodable by any future `tfplan.Decode` version and directly
   inspectable with `jq`.
4. **JSON-only input, with a corrective error.** Piping plain
   `terraform plan` text (instead of `terraform show -json` output) fails
   fast with an actionable message instead of misbehaving silently.
5. **Outputs are resources for display purposes.** `DisplayResources()`
   keeps `Resources`/`OutputChanges` as accurate, separate data while
   giving the TUI one unified, navigable list — and makes "plan has
   output-only changes" correctly count as "has changes."

## Testing Strategy

Terraprism's own tests never need real cloud infrastructure — it only
parses/renders plan JSON, it doesn't provision anything:

- `internal/tfplan/testdata/*.json` — hand-authored fixtures for specific
  edge cases (nested sensitivity paths, replace-pattern direction,
  zero-change plans) plus real fixtures captured once from `terraform`
  and `tofu` against no-network providers (`null_resource`, `random_id`),
  to catch schema drift between the hand-written fixtures and what the
  engines actually emit.
- `internal/runner` tests point `Options.Cmd` at a stand-in shell script
  instead of shimming `PATH`, since `Cmd` is just an executable path.
- `internal/history` tests redirect `$HOME` to a `t.TempDir()`.
