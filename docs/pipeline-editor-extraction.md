# Pipeline editor component boundary

Status: proposed

## Context

Piper already has a functional pipeline editor built on `@xyflow/react`:

- `frontend/src/shared/components/PipelineCanvas.tsx` owns the graph canvas,
  custom task nodes, edges, drag/drop, selection, positioning, and connection
  gestures.
- `frontend/src/pages/pipelines/PipelineEditorPage.tsx` owns the surrounding
  product workflow: source setup, volume and credential lookup, task palette,
  inspector forms, YAML editing, validation, and submission/navigation.
- `frontend/src/features/pipelines/editor.ts` owns the draft types plus YAML
  parse/build and draft validation.

The editor is useful beyond one Piper route, including declarative embedding
through resourcekit. It should not move into designkit: a DAG canvas, pipeline
task types, graph validation, and execution-oriented inspector state are
domain behavior rather than general-purpose design-system primitives.

## Decision

Treat the editor as a future `@loykin/pipelinekit` package, but establish the
package boundary inside Piper before creating a separate repository.

Pipelinekit may depend on designkit for buttons, panels, forms, and layout,
and on `@xyflow/react` for graph interaction. Designkit must not depend on
pipelinekit or expose pipeline-specific components.

The intended ownership is:

| Owner | Responsibilities |
| --- | --- |
| designkit | General UI primitives and templates |
| pipelinekit | Pipeline draft model, canvas, nodes/edges, palette, inspector, editor-local state and validation |
| Piper | Project context, API calls, credentials/volumes, persistence, execution, routing and permissions |
| resourcekit adapter | Map a scoped `PipelineEditor` resource spec and events onto pipelinekit props |

## Target component contract

The reusable editor must be controlled by its host and must not import Piper
router, project context, React Query hooks, or API clients.

```ts
interface PipelineEditorProps {
  value: PipelineDraft
  selectedNodeId?: string
  readOnly?: boolean
  availableSources?: PipelineSourceOption[]
  validationIssues?: PipelineValidationIssue[]

  onChange(value: PipelineDraft): void
  onSelectionChange(nodeId: string | undefined): void
  onValidate?(value: PipelineDraft): PipelineValidationIssue[]
  onSave?(value: PipelineDraft): void | Promise<void>
  onRun?(value: PipelineDraft): void | Promise<void>
}
```

The graph is one owned value. Individual nodes must not be modeled as nested
resourcekit children: node positions and edges cross-reference the entire
graph, so pipelinekit must own that graph invariant.

## Extraction plan

### 1. Stabilize inside Piper

- Move editor-only components out of `PipelineEditorPage.tsx` into a pipeline
  editor component area under `frontend/src/features/pipelines/components/`.
- Keep source setup, volume browsing, credential lookup, `createPipeline`,
  `getPipeline`, URL parameters, and navigation in the Piper page.
- Make the editor receive draft data and source/file options through props.
- Move node identifiers, layout, connection/disconnection, selection, and
  inspector editing behind one controlled editor contract.
- Keep `parsePipelineDraftYaml`, `buildPipelineDraftYaml`, and
  `validatePipelineDraft` pure and independent of Piper API state.

### 2. Verify the boundary

- Render the editor in an isolated development route or story without a
  project provider or backend.
- Cover add/move/connect/disconnect/delete/select operations with tests.
- Round-trip representative command, Python, and notebook pipelines through
  YAML parse/build.
- Confirm read-only rendering for run/history views can share the same graph
  model without editor controls.

### 3. Extract `@loykin/pipelinekit`

Create a separate package/repository only after the controlled props and
pipeline draft schema stop changing with every Piper page iteration. Export:

- `PipelineEditor`
- `PipelineCanvas`
- pipeline draft and graph types
- pipeline JSON Schema
- pure parse/build/validation helpers where they are not Piper-format-specific
- package styles

Piper then consumes the package and provides API-backed host adapters for
sources, save, and run behavior.

### 4. Add resourcekit integration

Add `@loykin/resourcekit/adapters/pipelinekit` as an optional adapter subpath.
It should register one graph-owning `PipelineEditor` kind and map declarative
events such as `nodeSelect`, `change`, `save`, and `run` to resourcekit event,
variable, action, and mutation dispatch. Resourcekit core remains unchanged.

## Non-goals

- Do not move pipeline behavior into designkit.
- Do not reimplement pan, zoom, drag, connection, or edge rendering outside
  `@xyflow/react` without a demonstrated need.
- Do not make pipelinekit call Piper endpoints or know project IDs.
- Do not expose credentials, storage paths, or execution authority in an
  AI-facing resource schema. Those remain host-owned capabilities.
- Do not split into a separate repository before the Piper-local component
  boundary is proven.

## Initial completion criteria

The first extraction phase is complete when `PipelineEditorPage.tsx` is a thin
Piper composition layer, the reusable editor renders without Piper providers,
and Piper's current create/edit/YAML workflows continue to work unchanged.
