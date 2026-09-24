/**
 * Compile-time drift check between the hand-written feature types and the
 * types generated from docs/openapi.yaml (`pnpm gen:api`). The spec marks
 * few fields required, so features keep their own stricter types; this only
 * fails `tsc` when a feature type has a field the spec doesn't document —
 * i.e. a rename or removal on one side that the other missed.
 */
import type { components } from '@/lib/openapi.gen'
import type { ProjectMember, User } from '@/features/access/types'
import type { AlertRule } from '@/features/alerting/types'
import type { Credential, TestCredentialResult } from '@/features/credentials/types'
import type { FederationMember } from '@/features/federation/types'
import type { NotebookExecution } from '@/features/notebook-executions/types'
import type { NotebookServer, NotebookVolume } from '@/features/notebooks/types'
import type { PipelineTemplate } from '@/features/pipelines/types'
import type { Project } from '@/features/projects/types'
import type { ExperimentSummary, Run, Step } from '@/features/runs/types'
import type { Schedule } from '@/features/schedules/types'
import type { Service } from '@/features/serving/types'
import type { StorageObjectInfo } from '@/features/storage/types'
import type { SystemSettings } from '@/features/system/types'
import type { Viewer } from '@/features/viewers/types'

type Schemas = components['schemas']

/** Resolves to `true`, or to the undocumented field names (a type error). */
type Documented<Local, Name extends keyof Schemas> =
  Exclude<keyof Local, keyof Schemas[Name]> extends never ? true : Exclude<keyof Local, keyof Schemas[Name]>

/** Fails to compile, naming the undocumented fields, unless `T` is `true`. */
type Check<T extends true> = T

export type OpenAPIContract = [
  Check<Documented<User, 'User'>>,
  Check<Documented<ProjectMember, 'ProjectMember'>>,
  Check<Documented<AlertRule, 'AlertRule'>>,
  Check<Documented<Credential, 'Credential'>>,
  Check<Documented<TestCredentialResult, 'TestCredentialResult'>>,
  Check<Documented<FederationMember, 'FederationMember'>>,
  Check<Documented<NotebookExecution, 'NotebookExecution'>>,
  Check<Documented<NotebookServer, 'NotebookServer'>>,
  Check<Documented<NotebookVolume, 'NotebookVolume'>>,
  Check<Documented<PipelineTemplate, 'PipelineTemplate'>>,
  Check<Documented<Project, 'Project'>>,
  Check<Documented<ExperimentSummary, 'ExperimentSummary'>>,
  // Run.steps is present in list responses requested with include_steps=true.
  Check<Documented<Run, 'RunWithSteps'>>,
  Check<Documented<Step, 'Step'>>,
  Check<Documented<Schedule, 'Schedule'>>,
  Check<Documented<Service, 'Service'>>,
  Check<Documented<StorageObjectInfo, 'StorageObjectInfo'>>,
  Check<Documented<SystemSettings, 'SystemSettings'>>,
  Check<Documented<Viewer, 'Viewer'>>,
]

