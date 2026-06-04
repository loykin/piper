# Notebook to Pipeline Promotion Roadmap

This document narrows the notebook-to-pipeline direction into an implementable roadmap.
The high-level approach is correct:

- do not infer a DAG from notebook internals automatically
- use Piper Pipeline YAML as the source of truth
- make promotion a validation and export workflow
- keep environment installation out of export
- avoid a separate draft database as the canonical source of truth

## Summary

Notebook promotion is a bridge from an interactive workspace to a repeatable pipeline definition.
The bridge should be explicit, reviewable, and file-based.

Recommended flow:

```text
Notebook workspace
  -> Pipeline YAML editor
  -> Validate
  -> Export / Download / Save / Upload
  -> Piper Pipeline YAML
```

The user can start from a notebook `work_dir`, but the final result should always be a Piper Pipeline YAML and an export bundle or manifest.

## What This Feature Is

Notebook promotion is:

- a promotion review screen
- a YAML editing and validation workflow
- a source-to-pipeline export path
- a way to freeze parameters, runtime, and artifact refs

It is not:

- notebook AST analysis
- automatic DAG inference
- runtime package installation
- a separate long-lived draft system
- a replacement for the pipeline engine

## MVP Scope

The MVP should be narrow.

### Input

- one notebook `work_dir`
- or one Git source root
- selected files within that single source root
- resolved parameters
- runtime metadata
- artifact references

### Output

- Piper Pipeline YAML
- validation result
- export bundle or manifest
- optional object-store upload

### UI

- notebook detail page
- promote page
- YAML editor / preview
- validation panel
- export target selector
- recent exports list

### Backend

- validate promotion input
- render pipeline YAML
- write export bundle and manifest
- upload bundle to object store when requested
- download bundle when requested

## Phase 1

Start with notebook promotion only.

Allowed sources:

- notebook `work_dir`
- Git repo path

Allowed targets:

- draft preview
- download
- object store

Rules:

- use a single source root per promotion
- do not cross notebook workspaces
- do not install dependencies during export
- do not infer pipeline structure from notebook code
- keep pipeline YAML editable and reviewable

## Phase 2

Expand promotion into a more general source-to-pipeline builder.

Possible additions:

- local directory source
- object-store prefix source
- richer file selection
- graph canvas view over YAML
- server-side packaging for source bundles

These are extensions, not required for the first release.

## Phase 3

Only after promotion is stable:

- deploy shortcuts from validated pipeline artifacts
- Git write-back support
- source bundle import/export
- richer source inspection APIs

Deploy should remain separate from promotion.
Promotion stops at a validated pipeline definition and exportable bundle.

## UI Model

The UI should treat YAML as the source of truth.

Recommended projections:

- graph view: visual representation of `steps` and `depends_on`
- step inspector: edit `run`, `params`, `inputs`, `outputs`, `env`, `resources`
- file panel: choose source files and artifact paths
- validation panel: show missing or unsupported fields
- export panel: choose target and download/upload result

The UI should not create or depend on a separate builder draft record.

## Source Handling Rules

Promotion should use exactly one source root at a time.

Notebook mode:

- source root is one notebook `work_dir`
- files must stay inside that `work_dir`
- `../` traversal and symlink escapes should be blocked

Git mode:

- source root is one repo + branch/SHA + base path
- files must stay inside that repository path

Object-store or local-directory modes can be added later, but they should follow the same single-root rule.

## Storage Rules

The object store should hold artifacts and export bundles, not the whole workspace by default.

Recommended storage model:

- `work_dir` stays runtime-local
- artifacts are uploaded by explicit path
- export bundle and manifest are stored separately
- full-workspace archive is optional and off by default

## What To Avoid

Avoid these patterns:

- `POST /api/pipeline-drafts`
- separate draft DB as source of truth
- hidden source analysis
- package installation during export
- implicit notebook-to-DAG conversion
- mixing deploy into the promotion flow

## Implementation Order

1. keep notebook promotion validate/export working
2. keep YAML as the canonical editable representation
3. keep export bundles file-based
4. keep object-store uploads optional and explicit
5. add graph UI projection only after the YAML path is stable

## Implementation Checklist

This is the concrete build order for the first usable version.

### Phase 1: Promotion backend

- validate promotion input on the server
- render a Piper Pipeline YAML draft
- generate an export manifest
- write a downloadable export bundle
- upload the bundle to object store when requested
- keep the existing notebook promotion route working end to end

### Phase 2: Promotion UI

- show source summary
- show runtime snapshot
- show resolved parameters
- show validation results
- allow export target selection
- show recent exports
- let users download bundles

### Phase 3: YAML editor projection

- render step list from YAML
- render dependency edges from `depends_on`
- edit node properties in an inspector
- keep the YAML as the source of truth

### Phase 4: Source expansion

- allow Git source as a first-class source root
- allow local directory source as an extension
- allow object-store prefix source as a later extension

## Acceptance Criteria

The roadmap is ready to implement when the following are true:

- one notebook workspace can be promoted without reading notebook internals automatically
- validation fails early when the source is unsupported or incomplete
- export produces a reproducible YAML bundle
- the result can be downloaded or uploaded without a separate draft DB
- the UI can reopen the YAML and reconstruct the view from it
- no package installation occurs during promotion
- source roots do not leak outside the chosen workspace

## Conclusion

This is the right direction for Piper.
The first implementation should be a narrow notebook-to-pipeline promotion flow that is explicit, reviewable, and file-based.
The current broader "source-to-pipeline builder" concept is valid, but it should be implemented in phases.
