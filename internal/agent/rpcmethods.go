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

	MethodFSListFiles = "fs.list_files"
)
