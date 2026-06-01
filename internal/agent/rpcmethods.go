package agent

const (
	MethodNotebookProvisionVolume = "notebook.provision_volume"
	MethodNotebookStart           = "notebook.start"
	MethodNotebookStop            = "notebook.stop"
	MethodNotebookDeprovision     = "notebook.deprovision_volume"
	MethodNotebookSyncStatus      = "notebook.sync_status"

	MethodServingDeploy  = "serving.deploy"
	MethodServingStop    = "serving.stop"
	MethodServingRestart = "serving.restart"

	MethodPipelineDispatch  = "pipeline.dispatch"
	MethodPipelineCancelRun = "pipeline.cancel_run"
)
