package agent

const (
	MethodNotebookProvisionVolume = "notebook.provision_volume"
	MethodNotebookStart           = "notebook.start"
	MethodNotebookStop            = "notebook.stop"
	MethodNotebookDeprovision     = "notebook.deprovision_volume"
	MethodNotebookSyncStatus      = "notebook.sync_status"

	MethodNotebookStatusUpdate = "notebook.status_update"

	MethodServingDeploy       = "serving.deploy"
	MethodServingStop         = "serving.stop"
	MethodServingSyncStatus   = "serving.sync_status"
	MethodServingStatusUpdate = "serving.status_update"

	MethodPipelineDispatch   = "pipeline.dispatch"
	MethodPipelineCancelRun  = "pipeline.cancel_run"
	MethodPipelineLeaseRenew = "pipeline.lease_renew"
	MethodPipelineTaskResult = "pipeline.task_result"
	MethodPipelineResultAck  = "pipeline.task_result_ack"

	// MethodPipelineRunDispatch sends an entire run's DAG (pipeline manifest,
	// params, per-step env) to the bound worker in a single message, rather
	// than one MethodPipelineDispatch per step. The worker's local scheduler
	// (pkg/pipeline/worker/scheduler) owns DAG promotion/retry/timeout for
	// everything sent this way. Not yet called in production — see
	// docs/backend/develop.md's State Ownership section.
	MethodPipelineRunDispatch = "pipeline.run_dispatch"

	// Worker-initiated (RPCRequest, not RPCCommand) — the DB access
	// interface a worker uses to persist state through master's DB rather
	// than the master deciding and pushing it down. See docs/backend/develop.md.
	MethodPipelineStepUpsert          = "pipeline.step_upsert"
	MethodPipelineRunFinalize         = "pipeline.run_finalize"
	MethodPipelineWorkerRecoveryQuery = "pipeline.worker_recovery_query"

	MethodFSListFiles      = "fs.list_files"
	MethodFSUploadSnapshot = "fs.upload_snapshot"

	MethodLogAppend = "log.append"
)
